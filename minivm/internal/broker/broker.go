// Package broker exposes only Codex inference over a dedicated VM link. It does
// not return OAuth credentials, run tools, or accept caller-selected upstreams.
package broker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const upstreamURL = "https://chatgpt.com/backend-api/codex"
const maxBody = 16 * 1024 * 1024
const responsesLiteHeader = "X-OpenAI-Internal-Codex-Responses-Lite"

type Config struct {
	AuthFile    string
	PeerIP      string
	Concurrency int
}

type Broker struct {
	config   Config
	auth     *credentials
	client   *http.Client
	upstream string
	slots    chan struct{}
}

func New(config Config) (*Broker, error) {
	if net.ParseIP(config.PeerIP) == nil || config.Concurrency < 1 {
		return nil, errors.New("invalid broker configuration")
	}
	if !filepath.IsAbs(config.AuthFile) || filepath.Clean(config.AuthFile) != config.AuthFile {
		return nil, errors.New("broker paths must be absolute")
	}
	client := &http.Client{
		Transport: &http.Transport{
			// Deliberately ignore HTTP(S)_PROXY environment variables.
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 45 * time.Second,
			IdleConnTimeout: 90 * time.Second, ForceAttemptHTTP2: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	b := &Broker{
		config: config, client: client, upstream: upstreamURL,
		slots: make(chan struct{}, config.Concurrency),
		auth:  &credentials{path: config.AuthFile, endpoint: tokenEndpoint, client: client},
	}
	return b, nil
}

func replyError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": code, "code": code}})
}

func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || peer != b.config.PeerIP || r.Header.Get("Origin") != "" {
		replyError(w, 403, "peer_not_allowed")
		return
	}
	// Exact paths only. No arbitrary URL, query parameters, upgrades, or login API.
	if r.URL.RawQuery != "" || r.URL.RawPath != "" || r.Header.Get("Upgrade") != "" ||
		r.Method != http.MethodPost || (r.URL.Path != "/v1/responses" && r.URL.Path != "/v1/responses/compact") {
		replyError(w, 404, "endpoint_not_allowed")
		return
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		replyError(w, 429, "concurrency_limit")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	var body map[string]json.RawMessage
	decoder := json.NewDecoder(r.Body)
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || body == nil {
		replyError(w, 400, "invalid_request")
		return
	}
	var model string
	if json.Unmarshal(body["model"], &model) != nil || !validHeader(model) {
		replyError(w, 400, "invalid_model")
		return
	}
	if !inferenceOnly(body) {
		replyError(w, 400, "unsupported_inference_feature")
		return
	}
	// Request bodies remain Responses wire format. Force stateless inference;
	// compaction has a different schema and does not accept stream/store.
	if r.URL.Path == "/v1/responses" {
		body["store"] = json.RawMessage("false")
		body["stream"] = json.RawMessage("true")
		delete(body, "background")
	}
	payload, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	auth, err := b.auth.get(ctx, "")
	if err != nil {
		replyError(w, 503, "broker_login_required")
		return
	}
	secrets := []string{auth.Access, auth.Refresh, auth.ID}
	var response *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost,
			b.upstream+strings.TrimPrefix(r.URL.Path, "/v1"), bytes.NewReader(payload))
		request.Header.Set("Authorization", "Bearer "+auth.Access)
		request.Header.Set("ChatGPT-Account-ID", auth.Account)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "text/event-stream, application/json")
		request.Header.Set("Originator", "codex_cli_rs")
		request.Header.Set("User-Agent", "codex_cli_rs/0.146.0 (vm-inference-broker)")
		if r.Header.Get(responsesLiteHeader) == "true" {
			request.Header.Set(responsesLiteHeader, "true")
		}
		// Only protocol correlation headers are relayed, never caller auth,
		// cookies, forwarded headers, account selectors, or Host overrides.
		for _, key := range []string{"Session_id", "X-Codex-Turn-State", "X-Codex-Turn-Metadata"} {
			if value := r.Header.Get(key); len(value) <= 4096 {
				request.Header.Set(key, value)
			}
		}
		response, err = b.client.Do(request)
		if err != nil {
			replyError(w, 502, "inference_unavailable")
			return
		}
		if response.StatusCode != 401 || attempt == 1 {
			break
		}
		response.Body.Close()
		auth, err = b.auth.get(ctx, auth.Access)
		if err != nil {
			replyError(w, 503, "broker_login_required")
			return
		}
		secrets = append(secrets, auth.Access, auth.Refresh, auth.ID)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		status := 502
		if response.StatusCode == 429 {
			status = 429
		}
		replyError(w, status, "upstream_rejected_request")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if state := response.Header.Get("X-Codex-Turn-State"); len(state) <= 4096 && state != "" {
		w.Header().Set("X-Codex-Turn-State", string(redact([]byte(state), secrets)))
	}
	if r.URL.Path == "/v1/responses/compact" {
		data, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
		if err != nil || len(data) > maxBody || !json.Valid(data) {
			replyError(w, 502, "invalid_upstream_response")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(redact(data, secrets))
		return
	}
	// The subscription endpoint may label SSE with a different media type.
	// Validate its framing before committing downstream headers instead.
	reader := bufio.NewScanner(response.Body)
	reader.Buffer(make([]byte, 64*1024), maxBody)
	started := false
	firstLine := true
	for reader.Scan() {
		line := reader.Bytes()
		if firstLine {
			line = bytes.TrimPrefix(line, []byte("\xef\xbb\xbf"))
			firstLine = false
		}
		if !started && len(line) == 0 {
			continue
		}
		if !sseLine(line) {
			if !started {
				replyError(w, 502, "invalid_upstream_response")
			}
			return
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			started = true
		}
		if _, err := w.Write(append(redact(line, secrets), '\n')); err != nil {
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	if !started {
		replyError(w, 502, "invalid_upstream_response")
	}
	// Truncated streams are detected by Codex's missing response.completed event.
}

func sseLine(line []byte) bool {
	if len(line) == 0 || line[0] == ':' {
		return true
	}
	field, _, _ := bytes.Cut(line, []byte(":"))
	switch string(field) {
	case "data", "event", "id", "retry":
		return true
	default:
		return false
	}
}

func inferenceOnly(body map[string]json.RawMessage) bool {
	allowed := map[string]bool{}
	for _, key := range strings.Fields("model instructions input tools tool_choice parallel_tool_calls reasoning store stream stream_options include service_tier prompt_cache_key text client_metadata") {
		allowed[key] = true
	}
	for key := range body {
		if !allowed[key] {
			return false
		}
	}
	if raw, ok := body["input"]; ok {
		var input any
		if json.Unmarshal(raw, &input) != nil || hasAccountReference(input, 0) {
			return false
		}
	}
	if raw, ok := body["tools"]; ok {
		return allowedTools(raw, 0)
	}
	return true
}

func hasAccountReference(value any, depth int) bool {
	if depth > 64 {
		return true
	}
	switch value := value.(type) {
	case map[string]any:
		// Responses Lite and client tool search carry tool definitions in input,
		// so the same restrictions must apply there as at the top level.
		if value["type"] == "additional_tools" || value["type"] == "tool_search_output" {
			encoded, err := json.Marshal(value["tools"])
			if err != nil || !allowedTools(encoded, 0) {
				return true
			}
		}
		if value["type"] == "tool_search_output" && value["execution"] != "client" {
			return true
		}
		for key, child := range value {
			switch key {
			case "file_id", "file_ids", "vector_store_ids", "previous_response_id", "conversation":
				return true
			}
			if key == "type" && child == "item_reference" {
				return true
			}
			if hasAccountReference(child, depth+1) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if hasAccountReference(child, depth+1) {
				return true
			}
		}
	}
	return false
}

func allowedTools(raw json.RawMessage, depth int) bool {
	if depth > 4 {
		return false
	}
	var tools []struct {
		Type      string          `json:"type"`
		Tools     json.RawMessage `json:"tools"`
		Execution string          `json:"execution"`
	}
	if json.Unmarshal(raw, &tools) != nil {
		return false
	}
	for _, tool := range tools {
		switch tool.Type {
		case "function", "custom", "web_search", "web_search_preview":
		case "tool_search":
			if tool.Execution != "client" {
				return false
			}
		case "namespace":
			if !allowedTools(tool.Tools, depth+1) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func redact(data []byte, secrets []string) []byte {
	for _, secret := range secrets {
		if secret != "" {
			data = bytes.ReplaceAll(data, []byte(secret), []byte("[redacted]"))
		}
	}
	return data
}
