package store

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/emmatown/config/minivm/internal/model"
)

func spec(name string) model.CreateMachine {
	return model.CreateMachine{
		Name: name, TemplateRevision: "dev-v1", MemoryMiB: 1024, VCPUs: 1,
	}
}

func openTest(t *testing.T, path string, budget uint32) *Store {
	t.Helper()
	s, err := Open(path, budget)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func requireError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
}

func TestRetrySurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s := openTest(t, path, 4096)
	op, err := s.Create("create-1", spec("one"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Running(op.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = openTest(t, path, 4096)
	retry, err := s.Create("create-1", spec("one"))
	if err != nil || retry.ID != op.ID {
		t.Fatalf("retry: %+v, %v", retry, err)
	}
	pending, err := s.Next()
	if err != nil || pending == nil || pending.ID != op.ID || pending.State != "running" {
		t.Fatalf("pending after reopen: %+v, %v", pending, err)
	}
	_, err = s.Create("create-1", spec("different"))
	requireError(t, err, "idempotency_conflict")
	machines, err := s.Machines()
	if err != nil || len(machines) != 1 {
		t.Fatalf("machines: %+v, %v", machines, err)
	}
}

func TestLifecycleConflictsAndRetainedNames(t *testing.T) {
	s := openTest(t, ":memory:", 1280)
	op, err := s.Create("create", spec("one"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Create("other", spec("two"))
	requireError(t, err, "capacity_exceeded")
	_, err = s.Action("start", op.MachineID, "start", 1)
	requireError(t, err, "operation_conflict")
	if err := s.Finish(op.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err = s.Action("stale", op.MachineID, "start", 1)
	requireError(t, err, "revision_conflict")
	start, err := s.Action("start", op.MachineID, "start", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(start.ID, nil); err != nil {
		t.Fatal(err)
	}
	// Duplicate completion must not increment the revision twice.
	if err := s.Finish(start.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err = s.Action("delete-running", op.MachineID, "delete", 4)
	requireError(t, err, "stop_before_delete")
	stop, err := s.Action("stop", op.MachineID, "stop", 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(stop.ID, nil); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.Action("delete", op.MachineID, "delete", 6)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(deleted.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, err = s.Create("reuse-name", spec("one"))
	requireError(t, err, "name_conflict")
	if _, err := s.Create("new-name", spec("two")); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentConnectionsCannotOvercommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	first := openTest(t, path, 1280)
	second := openTest(t, path, 1280)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, s := range []*Store{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := []string{"one", "two"}[i]
			_, err := s.Create(name, spec(name))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			requireError(t, err, "capacity_exceeded")
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d operations succeeded", succeeded)
	}
}
