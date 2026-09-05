// Package supervisor owns the privileged bootstrap operations. It imports only
// the standard library and the shared wire model, never controller or SQLite code.
package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emmatown/config/minivm/internal/model"
)

type Runner interface {
	Run(context.Context, string, ...string) (string, error)
}

type Commands struct{}

func (Commands) Run(ctx context.Context, program string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, program, args...).CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s: %w: %s", program, err, output)
	}
	return string(output), nil
}

type Host struct {
	Root    string
	Catalog model.Catalog
	Runner  Runner
	mu      sync.Mutex
}

func (h *Host) Init() error {
	if !filepath.IsAbs(h.Root) || filepath.Clean(h.Root) != h.Root || strings.ContainsAny(h.Root, ",\n\r") {
		return errors.New("invalid state directory")
	}
	if h.Runner == nil {
		h.Runner = Commands{}
	}
	for _, dir := range []string{"instances", "manifests", "receipts", "retained"} {
		if err := os.MkdirAll(filepath.Join(h.Root, dir), 0700); err != nil {
			return err
		}
	}
	// Instance parents must be traversable by their distinct VMM users.
	return os.Chmod(filepath.Join(h.Root, "instances"), 0711)
}

func writeJSON(path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func readRequest(path string) (model.HostRequest, error) {
	var req model.HostRequest
	f, err := os.Open(path)
	if err != nil {
		return req, err
	}
	defer f.Close()
	err = model.Decode(io.LimitReader(f, 16384), &req)
	return req, err
}

func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (h *Host) cmd(ctx context.Context, name string, args ...string) error {
	_, err := h.Runner.Run(ctx, name, args...)
	return err
}

func (h *Host) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /execute", func(w http.ResponseWriter, r *http.Request) {
		var req model.HostRequest
		if err := model.Decode(http.MaxBytesReader(w, r.Body, 16384), &req); err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if err := req.Validate(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if _, ok := h.Catalog[req.Spec.TemplateRevision]; !ok {
			http.Error(w, "unknown template", 400)
			return
		}
		if err := h.Apply(r.Context(), req); err != nil {
			log.Printf("operation %s: %v", req.OperationID, err)
			http.Error(w, "execution pending; inspect supervisor logs", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"operation_id": req.OperationID, "state": "succeeded"})
	})
	return mux
}

func (h *Host) Apply(ctx context.Context, req model.HostRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	template, ok := h.Catalog[req.Spec.TemplateRevision]
	if !ok {
		return errors.New("unknown template")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	receipt := filepath.Join(h.Root, "receipts", req.OperationID)
	if old, err := readRequest(receipt); err == nil {
		if old != req {
			return errors.New("operation identity conflict")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Deleted instances cannot be resurrected through the privileged API.
	retained, err := exists(filepath.Join(h.Root, "retained", req.MachineID))
	if err != nil {
		return err
	}
	if retained && req.Action != "delete" {
		return errors.New("instance retired")
	}
	dir := filepath.Join(h.Root, "instances", req.MachineID)
	manifest := filepath.Join(h.Root, "manifests", req.MachineID)
	username := "v" + strings.ReplaceAll(req.MachineID, "-", "")[:31]
	if req.Action == "create" {
		if err := h.create(ctx, req, template, dir, manifest, username); err != nil {
			return err
		}
	} else {
		original, err := readRequest(manifest)
		if err != nil {
			return err
		}
		if original.Spec != req.Spec || original.MachineID != req.MachineID {
			return errors.New("instance specification mismatch")
		}
		if err := h.action(ctx, req, template, dir, username); err != nil {
			return err
		}
	}
	return writeJSON(receipt, req)
}

func (h *Host) admit(ctx context.Context, req model.HostRequest) error {
	entries, err := os.ReadDir(filepath.Join(h.Root, "manifests"))
	if err != nil {
		return err
	}
	reserved := uint64(req.Spec.MemoryMiB) + 256
	for _, entry := range entries {
		if !model.ValidID(entry.Name()) {
			continue
		}
		retired, err := exists(filepath.Join(h.Root, "retained", entry.Name()))
		if err != nil {
			return err
		}
		if retired {
			continue
		}
		prior, err := readRequest(filepath.Join(h.Root, "manifests", entry.Name()))
		if err != nil {
			return err
		}
		reserved += uint64(prior.Spec.MemoryMiB) + 256
	}
	if reserved > 20480 {
		return errors.New("host memory admission limit exceeded")
	}
	text, err := h.Runner.Run(ctx, "df", "--output=avail", "-B1", h.Root)
	if err != nil {
		return err
	}
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return errors.New("cannot determine free disk space")
	}
	free, err := strconv.ParseUint(fields[len(fields)-1], 10, 64)
	if err != nil {
		return err
	}
	if free < 16*1024*1024*1024 {
		return errors.New("host disk reserve would be violated")
	}
	return nil
}

func (h *Host) create(ctx context.Context, req model.HostRequest, t model.Template, dir, manifest, user string) error {
	if original, err := readRequest(manifest); err == nil {
		if original != req {
			return errors.New("instance belongs to another request")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := h.admit(ctx, req); err != nil {
			return err
		}
		if err := writeJSON(manifest, req); err != nil {
			return err
		}
	} else {
		return err
	}
	present, err := exists(dir)
	if err != nil {
		return err
	}
	if !present {
		if err := h.cmd(ctx, "btrfs", "subvolume", "create", dir); err != nil {
			return err
		}
	}
	disk := filepath.Join(dir, "disk.raw")
	present, err = exists(disk)
	if err != nil {
		return err
	}
	if !present {
		partial := filepath.Join(dir, "disk.partial")
		if err := h.cmd(ctx, "cp", "--reflink=always", "--sparse=auto", t.Disk, partial); err != nil {
			return err
		}
		f, err := os.Open(partial)
		if err != nil {
			return err
		}
		err = f.Sync()
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if err := os.Rename(partial, disk); err != nil {
			return err
		}
		if err := syncDir(dir); err != nil {
			return err
		}
	}
	if _, err := h.Runner.Run(ctx, "id", "-u", user); err != nil {
		if err := h.cmd(ctx, "useradd", "--system", "--user-group", "--no-create-home", "--shell", "/run/current-system/sw/bin/nologin", user); err != nil {
			return err
		}
	}
	runtime := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(runtime, 0700); err != nil {
		return err
	}
	for _, cmd := range [][]string{
		{"chown", "root:" + user, dir}, {"chown", user + ":" + user, runtime, disk},
		{"chmod", "0750", dir}, {"chmod", "0700", runtime}, {"chmod", "0600", disk},
	} {
		if err := h.cmd(ctx, cmd[0], cmd[1:]...); err != nil {
			return err
		}
	}
	return nil
}

func (h *Host) unitState(ctx context.Context, unit string) (string, error) {
	out, err := h.Runner.Run(ctx, "systemctl", "show", "--property=LoadState", "--property=ActiveState", unit)
	if err != nil {
		return "", err
	}
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			props[k] = v
		}
	}
	if props["LoadState"] == "not-found" {
		return "inactive", nil
	}
	switch props["ActiveState"] {
	case "active", "inactive", "failed", "activating", "deactivating", "reloading":
		return props["ActiveState"], nil
	}
	return "", errors.New("unknown service state")
}

func (h *Host) action(ctx context.Context, req model.HostRequest, t model.Template, dir, user string) error {
	unit := "minivm-" + req.MachineID + ".service"
	state, err := h.unitState(ctx, unit)
	if err != nil {
		return err
	}
	stopped := state == "inactive" || state == "failed"
	switch req.Action {
	case "start":
		if state == "active" {
			return nil
		}
		if !stopped {
			return errors.New("unit transition in progress")
		}
		if state == "failed" {
			if err := h.cmd(ctx, "systemctl", "reset-failed", unit); err != nil {
				return err
			}
		}
		return h.cmd(ctx, "systemd-run", startArgs(req, t, dir, user)...)
	case "stop":
		if stopped {
			return nil
		}
		if state == "active" || state == "reloading" {
			if err := h.cmd(ctx, "ch-remote", "--api-socket", filepath.Join(dir, "runtime/vmm.sock"), "power-button"); err != nil {
				return err
			}
		}
		for i := 0; i < 60; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
			state, err = h.unitState(ctx, unit)
			if err != nil {
				return err
			}
			if state == "inactive" || state == "failed" {
				return nil
			}
		}
		return errors.New("guest has not shut down")
	case "delete":
		if !stopped {
			return errors.New("stop before deleting")
		}
		return writeJSON(filepath.Join(h.Root, "retained", req.MachineID), req)
	}
	return errors.New("unsupported action")
}

func startArgs(req model.HostRequest, t model.Template, dir, user string) []string {
	return []string{
		"--collect", "--unit=minivm-" + req.MachineID + ".service", "--service-type=exec",
		"--property=User=" + user, "--property=Group=" + user, "--property=SupplementaryGroups=kvm",
		"--property=MemorySwapMax=0", "--property=NoNewPrivileges=yes", "--property=ProtectSystem=strict",
		"--property=ProtectHome=yes", "--property=PrivateTmp=yes", "--property=PrivateNetwork=yes",
		"--property=DevicePolicy=closed", "--property=DeviceAllow=/dev/kvm rw", "--property=CapabilityBoundingSet=",
		"--property=ReadWritePaths=" + dir, fmt.Sprintf("--property=MemoryMax=%dM", req.Spec.MemoryMiB+256),
		fmt.Sprintf("--property=CPUQuota=%d%%", req.Spec.VCPUs*100), "--property=TasksMax=128", "--property=UMask=0077",
		"cloud-hypervisor", "--firmware", t.Firmware, "--disk", "path=" + filepath.Join(dir, "disk.raw"),
		"--cpus", fmt.Sprintf("boot=%d", req.Spec.VCPUs), "--memory", fmt.Sprintf("size=%dM", req.Spec.MemoryMiB),
		"--serial", "file=" + filepath.Join(dir, "runtime/serial.log"), "--console", "off",
		"--vsock", "cid=3,socket=" + filepath.Join(dir, "runtime/vsock"), "--api-socket", filepath.Join(dir, "runtime/vmm.sock"),
	}
}
