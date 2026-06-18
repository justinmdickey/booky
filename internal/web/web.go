// Package web serves Booky's dashboard UI and its JSON/upload API.
package web

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/justindickey/booky/internal/library"
	"github.com/justindickey/booky/internal/stats"
	"github.com/justindickey/booky/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	st       *store.Store
	lib      *library.Library
	dataDir  string
	user     string
	pass     string
	tmpl     *template.Template
	loc      *time.Location
}

func New(st *store.Store, lib *library.Library, dataDir, user, pass string) (*Server, error) {
	tmpl, err := template.ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		st: st, lib: lib, dataDir: dataDir, user: user, pass: pass,
		tmpl: tmpl, loc: time.Local,
	}, nil
}

func (s *Server) Register(mux *http.ServeMux) {
	static, _ := newSubFS()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(static)))

	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /api/summary", s.basicAuth(s.apiSummary))
	mux.HandleFunc("GET /api/collections", s.basicAuth(s.apiCollections))
	mux.HandleFunc("POST /api/collections", s.basicAuth(s.apiCreateCollection))
	mux.HandleFunc("DELETE /api/collections/{id}", s.basicAuth(s.apiDeleteCollection))
	mux.HandleFunc("POST /api/collections/{id}/books/{bid}", s.basicAuth(s.apiAddToCollection))
	mux.HandleFunc("DELETE /api/collections/{id}/books/{bid}", s.basicAuth(s.apiRemoveFromCollection))
	mux.HandleFunc("GET /api/library", s.basicAuth(s.apiLibrary))

	// Stats upload: the KOReader companion plugin (or curl/USB script) POSTs
	// the raw statistics.sqlite3 here.
	mux.HandleFunc("POST /api/stats/upload", s.basicAuth(s.apiUpload))

	// Cover proxy for the dashboard (reuses OPDS cover path under the hood).
	mux.HandleFunc("GET /cover/{id}", s.cover)
}

// basicAuth wraps a handler with HTTP Basic auth when credentials are configured.
func (s *Server) basicAuth(h http.HandlerFunc) http.HandlerFunc {
	if s.user == "" {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(s.user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(s.pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Booky"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := map[string]any{"HasLibrary": s.lib != nil}
	if err := s.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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
