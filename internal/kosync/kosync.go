// Package kosync implements a drop-in replacement for KOReader's progress-sync
// server. The protocol matches koreader-sync-server so the stock "Progress
// sync" plugin works against Booky with no client changes.
package kosync

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/justindickey/booky/internal/store"
)

type Server struct {
	st                *store.Store
	allowRegistration bool
}

func New(st *store.Store, allowRegistration bool) *Server {
	return &Server{st: st, allowRegistration: allowRegistration}
}

// Register wires the kosync routes onto a mux. Paths match the reference server.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /users/create", s.handleCreate)
	mux.HandleFunc("GET /users/auth", s.handleAuth)
	mux.HandleFunc("PUT /syncs/progress", s.handlePutProgress)
	mux.HandleFunc("GET /syncs/progress/{document}", s.handleGetProgress)
	mux.HandleFunc("GET /healthcheck", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"state": "OK"})
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, httpCode, code int, msg string) {
	writeJSON(w, httpCode, map[string]any{"code": code, "message": msg})
}

// auth pulls the kosync credentials from the custom headers and verifies them.
func (s *Server) auth(r *http.Request) (string, bool) {
	user := r.Header.Get("x-auth-user")
	key := r.Header.Get("x-auth-key")
	if user == "" || key == "" {
		return "", false
	}
	if !s.st.CheckAuth(user, key) {
		return "", false
	}
	return user, true
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !s.allowRegistration {
		// 403 is more conventional than the reference server's 402 here; the
		// KOReader client only branches on the 201 success code, so the exact
		// error status is safe to make sensible.
		errJSON(w, http.StatusForbidden, 2005, "User registration is disabled.")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"` // already md5(password) hex from the client
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		strings.TrimSpace(req.Username) == "" || req.Password == "" ||
		strings.Contains(req.Username, ":") {
		errJSON(w, http.StatusForbidden, 2003, "Invalid request.")
		return
	}
	if err := s.st.CreateUser(req.Username, req.Password); err != nil {
		if err == store.ErrUserExists {
			errJSON(w, http.StatusPaymentRequired, 2002, "Username is already registered.")
			return
		}
		errJSON(w, http.StatusBadGateway, 2000, "Unknown server error.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"username": req.Username})
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.auth(r); !ok {
		errJSON(w, http.StatusUnauthorized, 2001, "Unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authorized": "OK"})
}

func (s *Server) handlePutProgress(w http.ResponseWriter, r *http.Request) {
	user, ok := s.auth(r)
	if !ok {
		errJSON(w, http.StatusUnauthorized, 2001, "Unauthorized")
		return
	}
	var req struct {
		Document   string  `json:"document"`
		Progress   string  `json:"progress"`
		Percentage float64 `json:"percentage"`
		Device     string  `json:"device"`
		DeviceID   string  `json:"device_id"`
		Metadata   struct {
			Filename string `json:"filename"`
			Title    string `json:"title"`
			Authors  string `json:"authors"`
		} `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errJSON(w, http.StatusForbidden, 2003, "Invalid request.")
		return
	}
	if req.Document == "" {
		errJSON(w, http.StatusForbidden, 2004, "Field 'document' not provided.")
		return
	}
	ts, err := s.st.PutProgress(user, req.Document, store.Progress{
		Percentage: req.Percentage,
		Progress:   req.Progress,
		Device:     req.Device,
		DeviceID:   req.DeviceID,
	}, req.Metadata.Title, req.Metadata.Authors, req.Metadata.Filename)
	if err != nil {
		errJSON(w, http.StatusBadGateway, 2000, "Unknown server error.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"document": req.Document, "timestamp": ts})
}

func (s *Server) handleGetProgress(w http.ResponseWriter, r *http.Request) {
	user, ok := s.auth(r)
	if !ok {
		errJSON(w, http.StatusUnauthorized, 2001, "Unauthorized")
		return
	}
	doc := r.PathValue("document")
	p, found := s.st.GetProgress(user, doc)
	if !found {
		// Reference server returns 200 with an empty object when nothing stored.
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, p)
}
