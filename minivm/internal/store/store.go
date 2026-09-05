package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/emmatown/config/minivm/internal/model"
	"github.com/emmatown/config/minivm/internal/sqlite"
)

type Store struct {
	mu     sync.Mutex
	db     *sqlite.DB
	budget uint64
}

func Open(path string, budget uint32) (*Store, error) {
	db, err := sqlite.Open(path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, budget: uint64(budget)}
	for _, q := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL",
		"PRAGMA foreign_keys=ON",
		`CREATE TABLE IF NOT EXISTS machines (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL UNIQUE,
 spec TEXT NOT NULL,
 state TEXT NOT NULL,
 revision INTEGER NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS operations (
 id TEXT PRIMARY KEY,
 machine_id TEXT NOT NULL REFERENCES machines(id),
 action TEXT NOT NULL,
 state TEXT NOT NULL,
 error TEXT
)`,
		`CREATE TABLE IF NOT EXISTS requests (
 key TEXT PRIMARY KEY,
 payload TEXT NOT NULL,
 operation_id TEXT NOT NULL REFERENCES operations(id)
)`,
	} {
		if err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

func (s *Store) transaction(f func() error) error {
	if err := s.db.Exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = s.db.Exec("ROLLBACK")
		}
	}()
	if err := f(); err != nil {
		return err
	}
	if err := s.db.Exec("COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func machine(row []string) (model.Machine, error) {
	m := model.Machine{ID: row[0], State: row[2], Network: "none"}
	if err := json.Unmarshal([]byte(row[1]), &m.CreateMachine); err != nil {
		return m, err
	}
	rev, err := strconv.ParseInt(row[3], 10, 64)
	m.Revision = rev
	return m, err
}

func (s *Store) Machines() ([]model.Machine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.machines()
}

func (s *Store) machines() ([]model.Machine, error) {
	rows, err := s.db.Query("SELECT id,spec,state,revision FROM machines WHERE state!='deleted' ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	out := []model.Machine{}
	for _, row := range rows {
		m, err := machine(row)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Store) Machine(id string) (model.Machine, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.machine(id)
}

func (s *Store) machine(id string) (model.Machine, error) {
	rows, err := s.db.Query("SELECT id,spec,state,revision FROM machines WHERE id=? AND state!='deleted'", id)
	if err != nil {
		return model.Machine{}, err
	}
	if len(rows) == 0 {
		return model.Machine{}, errors.New("not_found")
	}
	return machine(rows[0])
}

func (s *Store) Operation(id string) (model.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.operation(id)
}

func (s *Store) operation(id string) (model.Operation, error) {
	rows, err := s.db.Query("SELECT id,machine_id,action,state,COALESCE(error,'') FROM operations WHERE id=?", id)
	if err != nil {
		return model.Operation{}, err
	}
	if len(rows) == 0 {
		return model.Operation{}, errors.New("not_found")
	}
	r := rows[0]
	op := model.Operation{ID: r[0], MachineID: r[1], Action: r[2], State: r[3]}
	if r[4] != "" {
		op.Error = &r[4]
	}
	return op, nil
}

func (s *Store) replay(key, payload string) (*model.Operation, error) {
	rows, err := s.db.Query("SELECT payload,operation_id FROM requests WHERE key=?", key)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if rows[0][0] != payload {
		return nil, errors.New("idempotency_conflict")
	}
	op, err := s.operation(rows[0][1])
	return &op, err
}

func (s *Store) Create(key string, spec model.CreateMachine) (model.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var op model.Operation
	if err := spec.Validate(); err != nil {
		return op, err
	}
	body, _ := json.Marshal(spec)
	payload := "create:" + string(body)
	err := s.transaction(func() error {
		replay, err := s.replay(key, payload)
		if err != nil {
			return err
		}
		if replay != nil {
			op = *replay
			return nil
		}
		rows, err := s.db.Query("SELECT id FROM machines WHERE name=?", spec.Name)
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			return errors.New("name_conflict")
		}
		machines, err := s.machines()
		if err != nil {
			return err
		}
		used := uint64(spec.MemoryMiB) + 256
		for _, m := range machines {
			used += uint64(m.MemoryMiB) + 256
		}
		if used > s.budget {
			return errors.New("capacity_exceeded")
		}
		id, err := model.NewID()
		if err != nil {
			return err
		}
		opid, err := model.NewID()
		if err != nil {
			return err
		}
		op = model.Operation{ID: opid, MachineID: id, Action: "create", State: "queued"}
		if err := s.db.Exec("INSERT INTO machines VALUES(?,?,?,'provisioning',1)", id, spec.Name, string(body)); err != nil {
			return err
		}
		return s.insert(key, payload, op)
	})
	return op, err
}

func (s *Store) insert(key, payload string, op model.Operation) error {
	if err := s.db.Exec("INSERT INTO operations VALUES(?,?,?,'queued',NULL)", op.ID, op.MachineID, op.Action); err != nil {
		return err
	}
	return s.db.Exec("INSERT INTO requests VALUES(?,?,?)", key, payload, op.ID)
}

func (s *Store) Action(key, id, action string, rev int64) (model.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var op model.Operation
	if action != "start" && action != "stop" && action != "delete" {
		return op, errors.New("unsupported_action")
	}
	err := s.transaction(func() error {
		payload := fmt.Sprintf("%s:%s:%d", action, id, rev)
		replay, err := s.replay(key, payload)
		if err != nil {
			return err
		}
		if replay != nil {
			op = *replay
			return nil
		}
		m, err := s.machine(id)
		if err != nil {
			return err
		}
		if m.Revision != rev {
			return errors.New("revision_conflict")
		}
		busy, err := s.db.Query("SELECT id FROM operations WHERE machine_id=? AND state IN ('queued','running')", id)
		if err != nil {
			return err
		}
		if len(busy) > 0 {
			return errors.New("operation_conflict")
		}
		if action == "start" && m.State != "stopped" || action == "stop" && m.State != "running" {
			return errors.New("state_conflict")
		}
		if action == "delete" && m.State != "stopped" && m.State != "failed" {
			return errors.New("stop_before_delete")
		}
		opid, err := model.NewID()
		if err != nil {
			return err
		}
		op = model.Operation{ID: opid, MachineID: id, Action: action, State: "queued"}
		if err := s.insert(key, payload, op); err != nil {
			return err
		}
		return s.db.Exec("UPDATE machines SET revision=revision+1 WHERE id=?", id)
	})
	return op, err
}

func (s *Store) Next() (*model.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT id FROM operations WHERE state IN ('queued','running') ORDER BY rowid LIMIT 1")
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	op, err := s.operation(rows[0][0])
	return &op, err
}

func (s *Store) Running(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Exec("UPDATE operations SET state='running' WHERE id=? AND state='queued'", id)
}

func (s *Store) Finish(id string, failure *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transaction(func() error {
		op, err := s.operation(id)
		if err != nil {
			return err
		}
		if op.State == "succeeded" || op.State == "failed" {
			return nil
		}
		state := "stopped"
		status := "succeeded"
		message := ""
		if failure != nil {
			state = "failed"
			status = "failed"
			message = *failure
		} else {
			switch op.Action {
			case "start":
				state = "running"
			case "delete":
				state = "deleted"
			}
		}
		if err := s.db.Exec("UPDATE operations SET state=?,error=NULLIF(?,'') WHERE id=?", status, message, id); err != nil {
			return err
		}
		return s.db.Exec("UPDATE machines SET state=?,revision=revision+1 WHERE id=?", state, op.MachineID)
	})
}
