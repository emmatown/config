package broker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Protocol constants are verified against the Codex source used by this deployment.
const tokenEndpoint = "https://auth.openai.com/oauth/token"
const clientID = "app_EMoamEEZ73f0CkXaXp7hrann"

var errAuth = errors.New("broker login or token refresh required")

type tokens struct {
	ID      string `json:"id_token"`
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Account string `json:"account_id"`
}

type authFile struct {
	Mode        string    `json:"auth_mode"`
	APIKey      *string   `json:"OPENAI_API_KEY"`
	Tokens      tokens    `json:"tokens"`
	LastRefresh time.Time `json:"last_refresh"`
}

type credentials struct {
	path        string
	endpoint    string
	client      *http.Client
	mu          sync.Mutex
	nextRefresh time.Time
}

func atomicJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
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
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func validHeader(value string) bool {
	if value == "" || len(value) > 32768 {
		return false
	}
	for _, c := range value {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func claims(token string) (map[string]json.RawMessage, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errAuth
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errAuth
	}
	var result map[string]json.RawMessage
	err = json.Unmarshal(data, &result)
	return result, err
}

func expiresSoon(token string) bool {
	c, err := claims(token)
	if err != nil {
		return true
	}
	var expires int64
	if json.Unmarshal(c["exp"], &expires) != nil {
		return true
	}
	return time.Unix(expires, 0).Before(time.Now().Add(2 * time.Minute))
}

// The local file is trusted credential material. JWT parsing is only used for
// expiry scheduling; OpenAI validates the token, not this unverified parser.
func (c *credentials) get(ctx context.Context, rejected string) (tokens, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := os.Open(c.path)
	if err != nil {
		return tokens{}, errAuth
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return tokens{}, errAuth
	}
	var auth authFile
	if json.NewDecoder(io.LimitReader(f, 128*1024)).Decode(&auth) != nil {
		return tokens{}, errAuth
	}
	if (auth.Mode != "" && auth.Mode != "chatgpt") || auth.APIKey != nil ||
		!validHeader(auth.Tokens.Access) || !validHeader(auth.Tokens.Refresh) || !validHeader(auth.Tokens.Account) {
		return tokens{}, errAuth
	}
	if !expiresSoon(auth.Tokens.Access) && (rejected == "" || rejected != auth.Tokens.Access) {
		return auth.Tokens, nil
	}
	if time.Now().Before(c.nextRefresh) {
		return tokens{}, errAuth
	}
	c.nextRefresh = time.Now().Add(30 * time.Second)

	body, _ := json.Marshal(map[string]string{
		"client_id": clientID, "grant_type": "refresh_token", "refresh_token": auth.Tokens.Refresh,
	})
	refreshCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(refreshCtx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return tokens{}, errAuth
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "vm-inference-broker/0.1")
	res, err := c.client.Do(req)
	if err != nil {
		return tokens{}, errAuth
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		// Neither OAuth error bodies nor tokens may reach callers or logs.
		return tokens{}, errAuth
	}
	var refreshed tokens
	if json.NewDecoder(io.LimitReader(res.Body, 128*1024)).Decode(&refreshed) != nil || !validHeader(refreshed.Access) {
		return tokens{}, errAuth
	}
	if refreshed.Refresh != "" {
		auth.Tokens.Refresh = refreshed.Refresh
	}
	if refreshed.ID != "" {
		auth.Tokens.ID = refreshed.ID
	}
	auth.Tokens.Access = refreshed.Access
	auth.Mode = "chatgpt"
	auth.LastRefresh = time.Now().UTC()
	if !validHeader(auth.Tokens.Refresh) || expiresSoon(auth.Tokens.Access) {
		return tokens{}, errAuth
	}
	if atomicJSON(c.path, auth) != nil {
		return tokens{}, errAuth
	}
	c.nextRefresh = time.Time{}
	return auth.Tokens, nil
}
