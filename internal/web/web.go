// Package web serves Booky's dashboard UI and its JSON/upload API.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/justindickey/booky/internal/auth"
	"github.com/justindickey/booky/internal/library"
	"github.com/justindickey/booky/internal/stats"
	"github.com/justindickey/booky/internal/store"
	"github.com/justindickey/booky/internal/version"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	st      *store.Store
	lib     *library.Library
	auth    *auth.Manager
	dataDir string
	tmpl    *template.Template
	loc     *time.Location
}

func New(st *store.Store, lib *library.Library, am *auth.Manager, dataDir string) (*Server, error) {
	tmpl, err := template.ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		st: st, lib: lib, auth: am, dataDir: dataDir,
		tmpl: tmpl, loc: time.Local,
	}, nil
}

func (s *Server) Register(mux *http.ServeMux) {
	static, _ := newSubFS()
	fileServer := http.FileServer(static)
	// Embedded files carry no modtime, so FileServer emits no validators and
	// browsers cache heuristically — stale JS after deploys. Force revalidation;
	// templates append ?v=<version> so new builds bust caches outright.
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})))

	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /api/login", s.apiLogin)
	mux.HandleFunc("POST /api/setup", s.apiSetup)
	mux.HandleFunc("POST /api/logout", s.apiLogout)
	mux.HandleFunc("POST /api/account", s.auth.RequireJSON(s.apiAccount))
	mux.HandleFunc("GET /api/summary", s.auth.RequireJSON(s.apiSummary))
	mux.HandleFunc("GET /api/collections", s.auth.RequireJSON(s.apiCollections))
	mux.HandleFunc("POST /api/collections", s.auth.RequireJSON(s.apiCreateCollection))
	mux.HandleFunc("DELETE /api/collections/{id}", s.auth.RequireJSON(s.apiDeleteCollection))
	mux.HandleFunc("POST /api/collections/{id}/books/{bid}", s.auth.RequireJSON(s.apiAddToCollection))
	mux.HandleFunc("DELETE /api/collections/{id}/books/{bid}", s.auth.RequireJSON(s.apiRemoveFromCollection))
	mux.HandleFunc("GET /api/library", s.auth.RequireJSON(s.apiLibrary))

	// Sync manifest: the companion plugin pulls this to bulk-download the whole
	// library to the Kobo, skipping what it already has.
	mux.HandleFunc("GET /api/sync/manifest", s.auth.RequireJSON(s.apiSyncManifest))

	// Stats upload: the KOReader companion plugin (or curl/USB script) POSTs
	// the raw statistics.sqlite3 here.
	mux.HandleFunc("POST /api/stats/upload", s.auth.RequireJSON(s.apiUpload))

	// Cover proxy for the dashboard (reuses OPDS cover path under the hood).
	mux.HandleFunc("GET /cover/{id}", s.auth.RequireJSON(s.cover))
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.auth.Authenticate(r); !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	user, _ := s.auth.SessionUser(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := map[string]any{
		"HasLibrary": s.lib != nil, "Version": version.Version,
		"User": user, "AuthEnabled": s.auth.Enabled(),
	}
	if err := s.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	// Already logged in (or auth not set up and no account to create)?
	if _, ok := s.auth.SessionUser(r); ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := map[string]any{
		"Setup":   !s.auth.Enabled(), // first run: create the account instead
		"Version": version.Version,
	}
	if err := s.tmpl.ExecuteTemplate(w, "login.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.auth.CheckPassword(req.Username, req.Password) {
		// Slow down credential guessing a little; bcrypt already costs ~50ms.
		time.Sleep(300 * time.Millisecond)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
		return
	}
	if err := s.auth.StartSession(w, r, req.Username); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiSetup creates the first account. Only valid while no account exists.
func (s *Server) apiSetup(w http.ResponseWriter, r *http.Request) {
	if s.auth.Enabled() {
		http.Error(w, "already configured", http.StatusForbidden)
		return
	}
	var req struct{ Username, Password string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		strings.TrimSpace(req.Username) == "" {
		http.Error(w, "username required", http.StatusBadRequest)
		return
	}
	if len(req.Password) < auth.MinPasswordLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": auth.ErrWeakPassword.Error()})
		return
	}
	if err := s.auth.CreateUser(strings.TrimSpace(req.Username), req.Password); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.auth.StartSession(w, r, strings.TrimSpace(req.Username)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) apiLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.EndSession(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiAccount changes username and/or password for the logged-in user.
func (s *Server) apiAccount(w http.ResponseWriter, r *http.Request) {
	user, ok := s.auth.SessionUser(r)
	if !ok {
		// Basic-auth callers can't manage the account; this is a browser flow.
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login required"})
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewUsername     string `json:"new_username"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	newName := strings.TrimSpace(req.NewUsername)
	if err := s.auth.UpdateUser(user, req.CurrentPassword, newName, req.NewPassword); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if newName == "" {
		newName = user
	}
	// Password change killed all sessions — start a fresh one for this browser.
	if req.NewPassword != "" {
		if err := s.auth.StartSession(w, r, newName); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": newName})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) apiSummary(w http.ResponseWriter, r *http.Request) {
	sum, err := stats.Compute(s.st, s.loc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Enrich with Calibre ids/covers where the library is present.
	if s.lib != nil {
		for i := range sum.Books {
			if id, ok := s.lib.CalibreIDForMD5(sum.Books[i].MD5); ok {
				sum.Books[i].CalibreID = id
			}
		}
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) apiCollections(w http.ResponseWriter, r *http.Request) {
	cols, err := s.st.Collections()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, cols)
}

func (s *Server) apiCreateCollection(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name, Icon string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	id, err := s.st.CreateCollection(req.Name, req.Icon)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (s *Server) apiDeleteCollection(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.st.DeleteCollection(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiAddToCollection(w http.ResponseWriter, r *http.Request) {
	cid, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	bid, _ := strconv.ParseInt(r.PathValue("bid"), 10, 64)
	if err := s.st.AddToCollection(cid, bid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiRemoveFromCollection(w http.ResponseWriter, r *http.Request) {
	cid, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	bid, _ := strconv.ParseInt(r.PathValue("bid"), 10, 64)
	if err := s.st.RemoveFromCollection(cid, bid); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) apiLibrary(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		writeJSON(w, http.StatusOK, []library.Book{})
		return
	}
	books, err := s.lib.All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, books)
}

// manifestEntry is one downloadable book in the bulk-sync manifest.
type manifestEntry struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Authors  string `json:"authors"`
	Format   string `json:"format"`   // lowercase, e.g. "epub"
	Filename string `json:"filename"` // stable name the device saves/matches on
	URL      string `json:"url"`      // download URL (relative to server root)
	Size     int64  `json:"size"`
	MD5      string `json:"md5"` // KOReader partial-MD5 — for content-based dedup
}

func (s *Server) apiSyncManifest(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		writeJSON(w, http.StatusOK, map[string]any{"books": []manifestEntry{}})
		return
	}
	books, err := s.lib.All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]manifestEntry, 0, len(books))
	for _, b := range books {
		f, ok := library.BestFormat(b)
		if !ok {
			continue // no downloadable format
		}
		fmtl := strings.ToLower(f.Format)
		out = append(out, manifestEntry{
			ID:       b.ID,
			Title:    b.Title,
			Authors:  b.Authors,
			Format:   fmtl,
			Filename: syncFilename(b.Title, b.Authors, fmtl),
			URL:      fmt.Sprintf("/opds/download/%d/%s", b.ID, fmtl),
			Size:     f.Size,
			MD5:      s.lib.MD5ForBook(b),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(out), "books": out})
}

// syncFilename builds the stable on-device filename the plugin matches against
// to decide whether a book is already downloaded. Must stay in lockstep with
// the plugin's expectation.
func syncFilename(title, authors, format string) string {
	name := strings.TrimSpace(title)
	if authors != "" {
		name += " - " + authors
	}
	if name == "" {
		name = "book"
	}
	return safeFilename(name) + "." + format
}

// safeFilename strips path-hostile characters so the name is portable to FAT
// (the Kobo's filesystem).
func safeFilename(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 0x20 {
			return '_'
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > 180 {
		s = s[:180]
	}
	return s
}

func (s *Server) apiUpload(w http.ResponseWriter, r *http.Request) {
	// Accept either a multipart "file" field or a raw body.
	var src io.Reader = r.Body
	if r.Header.Get("Content-Type") != "" && hasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing file field", http.StatusBadRequest)
			return
		}
		defer f.Close()
		src = f
	}

	dst := filepath.Join(s.dataDir, "upload-statistics.sqlite3")
	out, err := os.Create(dst)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out.Close()

	books, pages, err := stats.Ingest(s.st, dst)
	if err != nil {
		http.Error(w, "ingest failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "books": books, "page_stats": pages,
		"ingested_at": time.Now().Unix(),
	})
}

func (s *Server) cover(w http.ResponseWriter, r *http.Request) {
	if s.lib == nil {
		http.NotFound(w, r)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	bk, ok := s.lib.One(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	path, ok := s.lib.CoverPath(bk)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, path)
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }
