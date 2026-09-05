package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/emmatown/config/minivm/internal/model"
	"github.com/emmatown/config/minivm/internal/store"
)

type App struct {
	Store   *store.Store
	Catalog model.Catalog
	Token   string
}

func Write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func Error(w http.ResponseWriter, status int, code string) {
	Write(w, status, map[string]any{"error": map[string]string{"code": code}})
}

func failure(w http.ResponseWriter, err error) {
	code := err.Error()
	status := http.StatusInternalServerError
	switch code {
	case "not_found":
		status = 404
	case "revision_conflict":
		status = 412
	case "idempotency_conflict", "name_conflict", "capacity_exceeded", "operation_conflict", "state_conflict", "stop_before_delete":
		status = 409
	case "invalid_name", "reserved_name", "invalid_resources", "invalid_template_revision":
		status = 400
	}
	if status == 500 {
		log.Printf("store: %v", err)
		code = "internal_error"
	}
	Error(w, status, code)
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/templates", a.templates)
	mux.HandleFunc("GET /api/v1/machines", a.machines)
	mux.HandleFunc("POST /api/v1/machines", a.create)
	mux.HandleFunc("GET /api/v1/machines/{id}", a.machine)
	mux.HandleFunc("DELETE /api/v1/machines/{id}", a.delete)
	mux.HandleFunc("POST /api/v1/machines/{id}/actions/{action}", a.action)
	mux.HandleFunc("GET /api/v1/operations/{id}", a.operation)
	expected := sha256.Sum256([]byte("Bearer " + a.Token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			Error(w, 401, "unauthorized")
			return
		}
		if len(r.Header.Values("Origin")) > 0 {
			Error(w, 403, "browser_origin_not_supported")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (a *App) templates(w http.ResponseWriter, r *http.Request) {
	items := []map[string]string{}
	for id, t := range a.Catalog {
		items = append(items, map[string]string{"id": id, "description": t.Description, "mode": t.Mode, "network": "none"})
	}
	Write(w, 200, map[string]any{"items": items})
}

func (a *App) machines(w http.ResponseWriter, r *http.Request) {
	m, err := a.Store.Machines()
	if err != nil {
		failure(w, err)
		return
	}
	Write(w, 200, map[string]any{"items": m})
}

func (a *App) machine(w http.ResponseWriter, r *http.Request) {
	m, err := a.Store.Machine(r.PathValue("id"))
	if err != nil {
		failure(w, err)
		return
	}
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(m.Revision, 10)))
	Write(w, 200, m)
}

func key(w http.ResponseWriter, r *http.Request) (string, bool) {
	k := r.Header.Get("Idempotency-Key")
	if k == "" || len(k) > 128 || len(r.Header.Values("Idempotency-Key")) != 1 {
		Error(w, 400, "idempotency_key_required")
		return "", false
	}
	return k, true
}

func accepted(w http.ResponseWriter, op model.Operation) {
	w.Header().Set("Location", "/api/v1/operations/"+op.ID)
	Write(w, 202, op)
}

func (a *App) create(w http.ResponseWriter, r *http.Request) {
	k, ok := key(w, r)
	if !ok {
		return
	}
	var spec model.CreateMachine
	if err := model.Decode(http.MaxBytesReader(w, r.Body, 16384), &spec); err != nil {
		Error(w, 400, "invalid_request_body")
		return
	}
	if _, ok := a.Catalog[spec.TemplateRevision]; !ok {
		Error(w, 400, "unknown_template_revision")
		return
	}
	op, err := a.Store.Create(k, spec)
	if err != nil {
		failure(w, err)
		return
	}
	accepted(w, op)
}

func (a *App) perform(w http.ResponseWriter, r *http.Request, action string) {
	if action != "start" && action != "stop" && action != "delete" {
		Error(w, 501, "unsupported_action")
		return
	}
	k, ok := key(w, r)
	if !ok {
		return
	}
	match := r.Header.Get("If-Match")
	if len(match) < 3 || !strings.HasPrefix(match, "\"") || !strings.HasSuffix(match, "\"") {
		Error(w, 428, "if_match_required")
		return
	}
	rev, err := strconv.ParseInt(match[1:len(match)-1], 10, 64)
	if err != nil || rev < 1 {
		Error(w, 428, "if_match_required")
		return
	}
	op, err := a.Store.Action(k, r.PathValue("id"), action, rev)
	if err != nil {
		failure(w, err)
		return
	}
	accepted(w, op)
}

func (a *App) action(w http.ResponseWriter, r *http.Request) {
	a.perform(w, r, r.PathValue("action"))
}

func (a *App) delete(w http.ResponseWriter, r *http.Request) {
	a.perform(w, r, "delete")
}

func (a *App) operation(w http.ResponseWriter, r *http.Request) {
	op, err := a.Store.Operation(r.PathValue("id"))
	if err != nil {
		failure(w, err)
		return
	}
	Write(w, 200, op)
}

func UnixClient(socket string) *http.Client {
	return &http.Client{Timeout: 180 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
}

// Step retries the same durable operation after ambiguous host/transport failures.
// Only an explicit client rejection is terminal; a timeout is not a failure receipt.
func (a *App) Step(ctx context.Context, client *http.Client) error {
	op, err := a.Store.Next()
	if err != nil || op == nil {
		return err
	}
	machine, err := a.Store.Machine(op.MachineID)
	if err != nil {
		return err
	}
	if err := a.Store.Running(op.ID); err != nil {
		return err
	}
	body, err := json.Marshal(model.HostRequest{OperationID: op.ID, Action: op.Action, MachineID: op.MachineID, Spec: machine.CreateMachine})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://localhost/execute", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == 200 {
		var receipt struct {
			OperationID string `json:"operation_id"`
			State       string `json:"state"`
		}
		if err := model.Decode(io.LimitReader(response.Body, 16384), &receipt); err != nil {
			return err
		}
		if receipt.OperationID != op.ID || receipt.State != "succeeded" {
			return errors.New("invalid supervisor receipt")
		}
		return a.Store.Finish(op.ID, nil)
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		message := "supervisor_rejected"
		return a.Store.Finish(op.ID, &message)
	}
	return errors.New("supervisor execution remains uncertain")
}

func (a *App) Work(ctx context.Context, socket string) {
	client := UnixClient(socket)
	defer client.CloseIdleConnections()
	for {
		if err := a.Step(ctx, client); err != nil && ctx.Err() == nil {
			log.Printf("worker: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}
