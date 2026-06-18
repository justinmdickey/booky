package kosync

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/justindickey/booky/internal/store"
)

func md5hex(s string) string { h := md5.Sum([]byte(s)); return hex.EncodeToString(h[:]) }

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "b.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(st, true).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestKOSyncFlow(t *testing.T) {
	srv := newTestServer(t)
	c := srv.Client()
	key := md5hex("hunter2")

	// Register
	body, _ := json.Marshal(map[string]string{"username": "reader", "password": key})
	resp, err := c.Post(srv.URL+"/users/create", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: got %d", resp.StatusCode)
	}

	// Duplicate register -> 402
	resp, _ = c.Post(srv.URL+"/users/create", "application/json", strings.NewReader(string(body)))
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("dup create: got %d", resp.StatusCode)
	}

	// Auth check with correct creds
	req, _ := http.NewRequest("GET", srv.URL+"/users/auth", nil)
	req.Header.Set("x-auth-user", "reader")
	req.Header.Set("x-auth-key", key)
	resp, _ = c.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("auth: got %d", resp.StatusCode)
	}

	// Auth with wrong key -> 401
	req, _ = http.NewRequest("GET", srv.URL+"/users/auth", nil)
	req.Header.Set("x-auth-user", "reader")
	req.Header.Set("x-auth-key", "wrong")
	resp, _ = c.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad auth: got %d", resp.StatusCode)
	}

	// Get progress for unknown doc -> 200 {}
	req, _ = http.NewRequest("GET", srv.URL+"/syncs/progress/deadbeef", nil)
	req.Header.Set("x-auth-user", "reader")
	req.Header.Set("x-auth-key", key)
	resp, _ = c.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty progress: got %d", resp.StatusCode)
	}
	var empty map[string]any
	json.NewDecoder(resp.Body).Decode(&empty)
	if len(empty) != 0 {
		t.Fatalf("expected empty object, got %v", empty)
	}

	// Put progress
	prog, _ := json.Marshal(map[string]any{
		"document": "abc123", "progress": "/body/DocFragment[3]/body/p[1]",
		"percentage": 0.42, "device": "Clara", "device_id": "dev1",
		"metadata": map[string]string{"title": "Dune", "authors": "Herbert"},
	})
	req, _ = http.NewRequest("PUT", srv.URL+"/syncs/progress", strings.NewReader(string(prog)))
	req.Header.Set("x-auth-user", "reader")
	req.Header.Set("x-auth-key", key)
	resp, err = c.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("put progress: %v %d", err, resp.StatusCode)
	}
	var putResp struct {
		Document  string `json:"document"`
		Timestamp int64  `json:"timestamp"`
	}
	json.NewDecoder(resp.Body).Decode(&putResp)
	if putResp.Document != "abc123" || putResp.Timestamp == 0 {
		t.Fatalf("put resp = %+v", putResp)
	}

	// Get it back
	req, _ = http.NewRequest("GET", srv.URL+"/syncs/progress/abc123", nil)
	req.Header.Set("x-auth-user", "reader")
	req.Header.Set("x-auth-key", key)
	resp, _ = c.Do(req)
	var got store.Progress
	json.NewDecoder(resp.Body).Decode(&got)
	if got.Percentage != 0.42 || got.Device != "Clara" || got.Document != "abc123" {
		t.Fatalf("get progress = %+v", got)
	}

	// Missing document field -> 403
	req, _ = http.NewRequest("PUT", srv.URL+"/syncs/progress", strings.NewReader(`{"progress":"x"}`))
	req.Header.Set("x-auth-user", "reader")
	req.Header.Set("x-auth-key", key)
	resp, _ = c.Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing doc: got %d", resp.StatusCode)
	}
}
