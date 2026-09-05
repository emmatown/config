package broker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func jwt(expiry time.Time) string {
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, expiry.Unix()))) + ".access-secret"
}

func fixture(t *testing.T, upstream http.Handler) (*Broker, tokens) {
	t.Helper()
	dir := t.TempDir()
	auth := tokens{Access: jwt(time.Now().Add(time.Hour)), Refresh: "refresh-secret", ID: "id-secret", Account: "account-id"}
	if err := atomicJSON(filepath.Join(dir, "auth.json"), authFile{Mode: "chatgpt", Tokens: auth}); err != nil {
		t.Fatal(err)
	}
	b, err := New(Config{
		AuthFile: filepath.Join(dir, "auth.json"),
		PeerIP:   "192.168.126.2", Concurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	b.upstream = server.URL
	return b, auth
}

func request(b *Broker, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.RemoteAddr = "192.168.126.2:1234"
	r.Header.Set("Authorization", "Bearer attacker")
	r.Header.Set("Cookie", "attacker-cookie")
	r.Header.Set("ChatGPT-Account-ID", "attacker-account")
	r.Header.Set("X-Forwarded-For", "attacker")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	return w
}

const validBody = `{"model":"test-model","input":[],"tools":[{"type":"function","name":"shell"}]}`

func TestStreamingBoundary(t *testing.T) {
	b, auth := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer header.") || r.Header.Get("ChatGPT-Account-ID") != "account-id" {
			t.Error("upstream authentication or path incorrect")
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Forwarded-For") != "" {
			t.Error("untrusted headers forwarded")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["store"] != false || body["stream"] != true {
			t.Error("stateless streaming not enforced")
		}
		w.Header().Set("Set-Cookie", "upstream-secret")
		w.Header().Set("Authorization", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
		// Deliberately echo credentials in split chunks to test the output guard.
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"")
		w.(http.Flusher).Flush()
		fmt.Fprintf(w, "%s refresh-secret id-secret\"}\n\ndata: {\"type\":\"response.completed\"}\n\n", strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}))
	w := request(b, "/v1/responses", validBody)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "response.completed") {
		t.Fatalf("stream failed: %d %s", w.Code, w.Body.String())
	}
	for _, secret := range []string{auth.Access, auth.Refresh, auth.ID} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("credential leaked")
		}
	}
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("Authorization") != "" {
		t.Fatal("upstream credential headers leaked")
	}
}

func TestRejectsAuthorityExpansion(t *testing.T) {
	var calls atomic.Int32
	b, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	for _, path := range []string{"/auth.json", "/oauth/token", "/v1/models", "/v1/responses?url=https://evil.invalid", "/v1/../oauth/token"} {
		if request(b, path, validBody).Code != 404 {
			t.Errorf("allowed path %s", path)
		}
	}
	for _, body := range []string{
		`{"model":""}`,
		`{"model":"test-model","previous_response_id":"another-session"}`,
		`{"model":"test-model","input":[{"type":"item_reference","id":"another-session"}]}`,
		`{"model":"test-model","input":[{"type":"message","content":[{"type":"input_file","file_id":"account-file"}]}]}`,
		`{"model":"test-model","tools":[{"type":"mcp","server_url":"https://evil.invalid"}]}`,
		`{"model":"test-model","tools":[{"type":"namespace","tools":[{"type":"file_search"}]}]}`,
		`{"model":"test-model","input":[{"type":"additional_tools","tools":[{"type":"mcp"}]}]}`,
		`{"model":"test-model","input":[{"type":"tool_search_output","execution":"client","tools":[{"type":"file_search"}]}]}`,
		`{"model":"test-model","input":[{"type":"tool_search_output","execution":"server","tools":[]}]}`,
	} {
		if request(b, "/v1/responses", body).Code < 400 {
			t.Error("allowed authority expansion")
		}
	}
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(validBody))
	r.RemoteAddr = "172.31.255.2:1234"
	r.Header.Set("X-Forwarded-For", b.config.PeerIP)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 403 || calls.Load() != 0 {
		t.Fatal("peer/header spoof reached upstream")
	}
}

func TestResponsesLite(t *testing.T) {
	for _, header := range []string{"true", "false", "unexpected"} {
		t.Run(header, func(t *testing.T) {
			b, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := ""
				if header == "true" {
					want = "true"
				}
				if r.Header.Get(responsesLiteHeader) != want {
					t.Error("incorrect Responses Lite header")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
			}))
			r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"test-model","input":[{"type":"additional_tools","role":"developer","tools":[{"type":"tool_search","execution":"client"}]},{"type":"tool_search_output","execution":"client","tools":[{"type":"namespace","tools":[{"type":"function","name":"shell"}]}]}]}`))
			r.RemoteAddr = "192.168.126.2:1234"
			r.Header.Set(responsesLiteHeader, header)
			w := httptest.NewRecorder()
			b.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("Responses Lite failed: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAcceptsCallerSelectedModel(t *testing.T) {
	b, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "another-model" {
			t.Errorf("unexpected model %v", body["model"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
	}))
	body := strings.Replace(validBody, "test-model", "another-model", 1)
	if w := request(b, "/v1/responses", body); w.Code != 200 {
		t.Fatalf("caller-selected model rejected: %d %s", w.Code, w.Body.String())
	}
}

func TestRefreshSerializedAndPersisted(t *testing.T) {
	b, auth := fixture(t, http.NotFoundHandler())
	auth.Access = jwt(time.Now().Add(-time.Hour))
	_ = atomicJSON(b.config.AuthFile, authFile{Mode: "chatgpt", Tokens: auth})
	var count atomic.Int32
	refresher := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["client_id"] != clientID || payload["refresh_token"] != auth.Refresh || payload["grant_type"] != "refresh_token" {
			t.Error("incorrect refresh protocol")
		}
		_ = json.NewEncoder(w).Encode(tokens{Access: jwt(time.Now().Add(time.Hour)), Refresh: "rotated-refresh", ID: "rotated-id"})
	}))
	defer refresher.Close()
	b.auth.endpoint = refresher.URL
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := b.auth.get(context.Background(), auth.Access)
			if err != nil || got.Refresh != "rotated-refresh" {
				t.Error("refresh failed")
			}
		}()
	}
	wg.Wait()
	info, _ := os.Stat(b.config.AuthFile)
	if count.Load() != 1 || info.Mode().Perm() != 0600 {
		t.Fatal("refresh was not serialized or securely persisted")
	}
	data, _ := os.ReadFile(b.config.AuthFile)
	if !strings.Contains(string(data), "rotated-refresh") {
		t.Fatal("refresh token not persisted")
	}
}

func TestRedirectAndErrorsDoNotLeak(t *testing.T) {
	for _, status := range []int{302, 401, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			b, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://evil.invalid")
				w.WriteHeader(status)
				fmt.Fprint(w, "refresh-secret id-secret upstream error")
			}))
			b.auth.endpoint = b.upstream
			w := request(b, "/v1/responses", validBody)
			if w.Code < 400 || strings.Contains(w.Body.String(), "secret") || w.Header().Get("Location") != "" {
				t.Fatal("unsafe upstream response")
			}
		})
	}
}

func TestCompactWorks(t *testing.T) {
	b, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses/compact" {
			t.Error("wrong compaction path")
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"stream"`) {
			t.Error("stream field added to compaction")
		}
		fmt.Fprint(w, `{"output":[]}`)
	}))
	if request(b, "/v1/responses/compact", validBody).Code != 200 {
		t.Fatal("compaction failed")
	}
}

func TestConcurrencyAndCancellation(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	b, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		close(cancelled)
	}))
	b.slots = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(validBody)).WithContext(ctx)
	r.RemoteAddr = "192.168.126.2:1234"
	done := make(chan struct{})
	go func() {
		b.ServeHTTP(httptest.NewRecorder(), r)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	if request(b, "/v1/responses", validBody).Code != 429 {
		t.Error("concurrency limit not enforced")
	}
	cancel()
	for _, channel := range []chan struct{}{cancelled, done} {
		select {
		case <-channel:
		case <-time.After(5 * time.Second):
			t.Fatal("cancellation did not propagate")
		}
	}
}

func TestUnsafeCredentialFileFailsClosed(t *testing.T) {
	b, _ := fixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("unsafe credentials used")
	}))
	if err := os.Chmod(b.config.AuthFile, 0644); err != nil {
		t.Fatal(err)
	}
	if request(b, "/v1/responses", validBody).Code != 503 {
		t.Fatal("world-readable credential file accepted")
	}
}

func TestStreamFramingRatherThanMediaType(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		status                  int
	}{
		{"plain SSE", "text/plain", "data: {\"type\":\"response.completed\"}\n\n", 200},
		{"binary SSE", "application/octet-stream", "event: response.completed\ndata: {}\n\n", 200},
		{"BOM and heartbeat", "text/plain", "\xef\xbb\xbf\n: keepalive\n\ndata: {}\n\n", 200},
		{"JSON", "application/json", "{\"error\":\"id-secret\"}", 502},
		{"HTML even with SSE header", "text/event-stream", "<html>id-secret</html>", 502},
		{"empty", "text/event-stream", "", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				fmt.Fprint(w, tc.body)
			}))
			w := request(b, "/v1/responses", validBody)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if tc.status == 200 && w.Header().Get("Content-Type") != "text/event-stream" {
				t.Fatal("downstream media type not normalized")
			}
			if strings.Contains(w.Body.String(), "id-secret") {
				t.Fatal("invalid upstream body leaked")
			}
		})
	}
}
