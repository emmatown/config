// Package model defines the small shared wire protocol. It has no database or
// controller dependencies, so the privileged supervisor can stay independent.
package model

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

type CreateMachine struct {
	Name             string `json:"name"`
	TemplateRevision string `json:"template_revision"`
	MemoryMiB        uint32 `json:"memory_mib"`
	VCPUs            uint32 `json:"vcpus"`
}

var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (s CreateMachine) Validate() error {
	if !namePattern.MatchString(s.Name) {
		return errors.New("invalid_name")
	}
	switch s.Name {
	case "console", "auth", "api", "www":
		return errors.New("reserved_name")
	}
	if s.MemoryMiB < 512 || s.MemoryMiB > 16384 || s.VCPUs < 1 || s.VCPUs > 8 {
		return errors.New("invalid_resources")
	}
	if s.TemplateRevision == "" || len(s.TemplateRevision) > 256 {
		return errors.New("invalid_template_revision")
	}
	return nil
}

func ValidID(id string) bool { return uuidPattern.MatchString(id) }

func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[:8], h[8:12], h[12:16], h[16:20], h[20:]), nil
}

type Template struct {
	Description string `json:"description"`
	Disk        string `json:"disk"`
	Firmware    string `json:"firmware"`
	Mode        string `json:"mode"`
}

type Catalog map[string]Template

func LoadCatalog(path string) (Catalog, error) {
	var c Catalog
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := Decode(f, &c); err != nil {
		return nil, err
	}
	for id, t := range c {
		if id == "" || len(id) > 256 || t.Mode != "development" {
			return nil, fmt.Errorf("unsupported catalog revision %q", id)
		}
		for _, p := range []string{t.Disk, t.Firmware} {
			if !filepath.IsAbs(p) || filepath.Clean(p) != p {
				return nil, fmt.Errorf("catalog paths must be absolute and clean")
			}
			// These paths enter Cloud Hypervisor's comma-delimited argument syntax.
			for _, r := range p {
				if r == ',' || r == '\n' || r == '\r' || r == 0 {
					return nil, errors.New("invalid catalog path")
				}
			}
		}
	}
	if c == nil {
		c = Catalog{}
	}
	return c, nil
}

type Machine struct {
	ID string `json:"id"`
	CreateMachine
	State    string `json:"state"`
	Revision int64  `json:"revision"`
	Network  string `json:"network"`
}

type Operation struct {
	ID        string  `json:"id"`
	MachineID string  `json:"machine_id"`
	Action    string  `json:"action"`
	State     string  `json:"state"`
	Error     *string `json:"error"`
}

type HostRequest struct {
	OperationID string        `json:"operation_id"`
	Action      string        `json:"action"`
	MachineID   string        `json:"machine_id"`
	Spec        CreateMachine `json:"spec"`
}

func (r HostRequest) Validate() error {
	if !ValidID(r.MachineID) || !ValidID(r.OperationID) {
		return errors.New("invalid_uuid")
	}
	if err := r.Spec.Validate(); err != nil {
		return err
	}
	switch r.Action {
	case "create", "start", "stop", "delete":
		return nil
	}
	return errors.New("unsupported_action")
}

// Decode rejects unknown fields and extra JSON values for both API boundaries.
func Decode(r io.Reader, v any) error {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing_json")
	}
	return nil
}
