package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emmatown/config/minivm/internal/model"
)

type fakeCommands struct {
	calls      []string
	queryError bool
}

func (f *fakeCommands) Run(ctx context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	switch name {
	case "df":
		return "Avail\n1000000000000\n", nil
	case "btrfs":
		return "", os.MkdirAll(args[len(args)-1], 0700)
	case "cp":
		return "", os.WriteFile(args[len(args)-1], []byte("test disk"), 0600)
	case "systemctl":
		if f.queryError {
			return "", errors.New("system bus unavailable")
		}
		return "LoadState=not-found\nActiveState=inactive\n", nil
	}
	return "", nil
}

func newRequest(t *testing.T) model.HostRequest {
	t.Helper()
	id, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	op, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return model.HostRequest{
		MachineID:   id,
		OperationID: op,
		Action:      "create",
		Spec:        model.CreateMachine{Name: "one", TemplateRevision: "dev", MemoryMiB: 1024, VCPUs: 1},
	}
}

func TestReceiptReplayAndRetirement(t *testing.T) {
	commands := &fakeCommands{}
	host := &Host{
		Root:    t.TempDir(),
		Runner:  commands,
		Catalog: model.Catalog{"dev": {Disk: "/nix/store/test/disk.img", Firmware: "/nix/store/test/fw", Mode: "development"}},
	}
	if err := host.Init(); err != nil {
		t.Fatal(err)
	}
	req := newRequest(t)
	if err := host.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	count := len(commands.calls)
	if err := host.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if count != len(commands.calls) {
		t.Fatal("successful replay repeated host commands")
	}
	changed := req
	changed.Spec.MemoryMiB = 2048
	if err := host.Apply(context.Background(), changed); err == nil {
		t.Fatal("changed request reused a receipt")
	}
	deleted := req
	deleted.OperationID, _ = model.NewID()
	deleted.Action = "delete"
	if err := host.Apply(context.Background(), deleted); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(host.Root, "instances", req.MachineID, "disk.raw")); err != nil {
		t.Fatal("delete did not retain disk:", err)
	}
	start := req
	start.OperationID, _ = model.NewID()
	start.Action = "start"
	if err := host.Apply(context.Background(), start); err == nil {
		t.Fatal("retired machine was restarted")
	}
}

func TestInvalidIDsAndUnknownStateFailClosed(t *testing.T) {
	commands := &fakeCommands{queryError: true}
	host := &Host{Root: t.TempDir(), Runner: commands, Catalog: model.Catalog{"dev": {}}}
	req := newRequest(t)
	req.MachineID = "../../etc"
	if err := host.Apply(context.Background(), req); err == nil || len(commands.calls) != 0 {
		t.Fatal("invalid request reached command execution")
	}
	if _, err := host.unitState(context.Background(), "test.service"); err == nil {
		t.Fatal("unavailable system bus was treated as a stopped VM")
	}
}

func TestVMMHasNoNICAndNoCapabilities(t *testing.T) {
	req := newRequest(t)
	args := startArgs(req, model.Template{Firmware: "/nix/store/test/fw"}, "/var/lib/vm-state/instances/"+req.MachineID, "test-user")
	joined := "\n" + strings.Join(args, "\n") + "\n"
	for _, required := range []string{
		"--property=PrivateNetwork=yes",
		"--property=CapabilityBoundingSet=",
		"--property=DevicePolicy=closed",
		"--property=User=test-user",
	} {
		if !strings.Contains(joined, "\n"+required+"\n") {
			t.Errorf("missing %s", required)
		}
	}
	if strings.Contains(joined, "\n--net\n") {
		t.Fatal("bootstrap VMM received a network interface")
	}
}
