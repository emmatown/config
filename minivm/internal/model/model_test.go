package model

import (
	"strings"
	"testing"
)

func TestIdentifiersAndRequestFields(t *testing.T) {
	for _, name := range []string{"../disk", "a/b", "--help", "UPPER", "console", "", strings.Repeat("a", 41)} {
		spec := CreateMachine{Name: name, TemplateRevision: "dev", MemoryMiB: 1024, VCPUs: 1}
		if spec.Validate() == nil {
			t.Errorf("accepted %q", name)
		}
	}
	id, err := NewID()
	if err != nil || !ValidID(id) {
		t.Fatalf("new UUID: %q, %v", id, err)
	}
	for _, body := range []string{
		`{"name":"test","host_path":"/etc/shadow"}`,
		`{"name":"test"} {"name":"other"}`,
	} {
		if Decode(strings.NewReader(body), &CreateMachine{}) == nil {
			t.Errorf("accepted unexpected JSON: %s", body)
		}
	}
}
