package api

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emmatown/config/minivm/internal/model"
	"github.com/emmatown/config/minivm/internal/store"
)

func testApp(t *testing.T) *App {
	t.Helper()
	db, err := store.Open(":memory:", 4096)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &App{
		Store:   db,
		Token:   strings.Repeat("a", 32),
		Catalog: model.Catalog{"dev-v1": {Mode: "development", Description: "test"}},
	}
}

func TestAPIAuthenticationValidationAndReplay(t *testing.T) {
	app := testApp(t)
	handler := app.Handler()
	body := `{"name":"one","template_revision":"dev-v1","memory_mib":1024,"vcpus":1}`
	request := func(auth, origin, key, payload string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/machines", strings.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		auth, origin, key, body string
		status                  int
	}{
		{"", "", "create", body, 401},
		{"Bearer " + app.Token, "https://guest.example.net", "create", body, 403},
		{"Bearer " + app.Token, "", "", body, 400},
		{"Bearer " + app.Token, "", "create", body + `{}`, 400},
		{"Bearer " + app.Token, "", "create", strings.Replace(body, `"name"`, `"host_path"`, 1), 400},
	} {
		if got := request(tc.auth, tc.origin, tc.key, tc.body); got.Code != tc.status {
			t.Fatalf("got %d (%s), want %d", got.Code, got.Body, tc.status)
		}
	}
	first := request("Bearer "+app.Token, "", "create", body)
	second := request("Bearer "+app.Token, "", "create", body)
	if first.Code != 202 || second.Code != 202 || first.Body.String() != second.Body.String() {
		t.Fatalf("replay differs: %s vs %s", first.Body, second.Body)
	}
	if !strings.HasPrefix(first.Header().Get("Location"), "/api/v1/operations/") {
		t.Fatal("missing operation location")
	}
}

func TestWorkerRetriesAmbiguousResponse(t *testing.T) {
	app := testApp(t)
	spec := model.CreateMachine{Name: "one", TemplateRevision: "dev-v1", MemoryMiB: 1024, VCPUs: 1}
	op, err := app.Store.Create("create", spec)
	if err != nil {
		t.Fatal(err)
	}
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		if len(bodies) == 1 {
			http.Error(w, "uncertain", 503)
			return
		}
		Write(w, 200, map[string]string{"operation_id": op.ID, "state": "succeeded"})
	}))
	defer server.Close()
	// Connect the fixed supervisor URL to this test server without a real host.
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
		},
	}}
	defer client.CloseIdleConnections()
	if err := app.Step(context.Background(), client); err == nil {
		t.Fatal("ambiguous response was treated as success")
	}
	pending, err := app.Store.Operation(op.ID)
	if err != nil || pending.State != "running" {
		t.Fatalf("pending: %+v, %v", pending, err)
	}
	if err := app.Step(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatal("retry changed the supervisor request")
	}
	result, err := app.Store.Operation(op.ID)
	if err != nil || result.State != "succeeded" {
		t.Fatalf("result: %+v, %v", result, err)
	}
}
