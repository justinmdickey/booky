// Package auth handles Booky's dashboard accounts: bcrypt-hashed credentials
// in the store, cookie sessions for browsers, and an HTTP Basic fallback for
// non-browser clients (the KOReader plugin, OPDS readers, curl).
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/justindickey/booky/internal/store"
)

const (
	cookieName  = "booky_session"
	sessionTTL  = 30 * 24 * time.Hour
	minPassword = 8
)

type Manager struct {
	st *store.Store
}

func New(st *store.Store) *Manager { return &Manager{st: st} }

// Enabled reports whether any account exists. With zero accounts everything
// is open (first run, before setup).
func (m *Manager) Enabled() bool {
	n, err := m.st.CountWebUsers()
	return err == nil && n > 0
}

// CreateUser hashes and stores a new account.
func (m *Manager) CreateUser(username, password string) error {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return m.st.CreateWebUser(username, string(h))
}

// CheckPassword verifies credentials against the stored bcrypt hash.
func (m *Manager) CheckPassword(username, password string) bool {
	h, ok := m.st.WebUserHash(username)
	if !ok {
		// Burn comparable time so missing users aren't distinguishable.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$7EqJtq98hPqEX7fNZaFWoOhi5B1qZP0ZQxV0kJ0aFqUq9nWl0mNSy"), []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(h), []byte(password)) == nil
}

// UpdateUser renames and/or re-passwords an account after verifying the
// current password. Empty newName/newPassword mean "keep".
func (m *Manager) UpdateUser(username, currentPassword, newName, newPassword string) error {
	if !m.CheckPassword(username, currentPassword) {
		return ErrBadCredentials
	}
	if newName == "" {
		newName = username
	}
	hash, _ := m.st.WebUserHash(username)
	if newPassword != "" {
		if len(newPassword) < minPassword {
			return ErrWeakPassword
		}
		h, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		hash = string(h)
	}
	if err := m.st.UpdateWebUser(username, newName, hash); err != nil {
		return err
	}
	if newPassword != "" {
		// Invalidate every session; the caller re-issues one for this browser.
		return m.st.DeleteUserSessions(newName)
	}
	return nil
}

type authError string

func (e authError) Error() string { return string(e) }

const (
	ErrBadCredentials authError = "current password is incorrect"
	ErrWeakPassword   authError = "password must be at least 8 characters"
)

// MinPasswordLen is exposed for handlers validating new passwords.
const MinPasswordLen = minPassword

// ---- Sessions ----

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// StartSession creates a session and sets the cookie on the response.
func (m *Manager) StartSession(w http.ResponseWriter, r *http.Request, username string) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	tok := hex.EncodeToString(raw)
	exp := time.Now().Add(sessionTTL)
	if err := m.st.CreateSession(hashToken(tok), username, exp.Unix()); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    tok,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
	return nil
}

// EndSession deletes the session and clears the cookie.
func (m *Manager) EndSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		_ = m.st.DeleteSession(hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r),
	})
}

// SessionUser returns the logged-in username for the request's cookie.
func (m *Manager) SessionUser(r *http.Request) (string, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return m.st.SessionUser(hashToken(c.Value))
}

// Authenticate accepts either a session cookie (browser) or HTTP Basic
// credentials (plugin, OPDS clients, curl). With no accounts configured it
// always succeeds.
func (m *Manager) Authenticate(r *http.Request) (string, bool) {
	if !m.Enabled() {
		return "", true
	}
	if u, ok := m.SessionUser(r); ok {
		return u, true
	}
	if u, p, ok := r.BasicAuth(); ok && m.CheckPassword(u, p) {
		return u, true
	}
	return "", false
}

// RequireJSON wraps API handlers: 401 JSON on failure. Deliberately no
// WWW-Authenticate header — browsers must never show the Basic popup; the
// frontend redirects to /login on 401 instead. Non-browser clients send
// Basic credentials preemptively and never need the challenge.
func (m *Manager) RequireJSON(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := m.Authenticate(r); !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		h(w, r)
	}
}

// RequireBasic wraps OPDS handlers: e-reader clients speak HTTP Basic and
// need the WWW-Authenticate challenge to prompt for credentials.
func (m *Manager) RequireBasic(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := m.Authenticate(r); !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="Booky"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
