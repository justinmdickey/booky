// Command booky is a self-hosted companion for KOReader + Calibre-Web-Automated:
// a private KOReader progress-sync server, a reading-stats dashboard, and a
// curated OPDS feed — all in one static binary.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/justindickey/booky/internal/config"
	"github.com/justindickey/booky/internal/kosync"
	"github.com/justindickey/booky/internal/library"
	"github.com/justindickey/booky/internal/opds"
	"github.com/justindickey/booky/internal/store"
	"github.com/justindickey/booky/internal/web"
)

func main() {
	cfg := config.Load()
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("booky: ")

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	st, err := store.Open(filepath.Join(cfg.DataDir, "booky.sqlite3"))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	var lib *library.Library
	if cfg.CalibreLibrary != "" {
		lib, err = library.Open(cfg.CalibreLibrary)
		if err != nil {
			log.Printf("WARNING: calibre library disabled: %v", err)
			lib = nil
		} else {
			log.Printf("calibre library: %s", lib.Root())
		}
	} else {
		log.Printf("calibre library: not configured (set BOOKY_CALIBRE_LIBRARY)")
	}

	mux := http.NewServeMux()

	// kosync — always on; this is the headline feature.
	kosync.New(st, cfg.AllowRegistration).Register(mux)

	// OPDS — only when a library is mounted.
	if lib != nil {
		opds.New(lib, st, cfg.PublicURL).Register(mux)
	}

	// Web dashboard + API.
	websrv, err := web.New(st, lib, cfg.DataDir, cfg.OPDSUser, cfg.OPDSPass)
	if err != nil {
		log.Fatalf("init web: %v", err)
	}
	websrv.Register(mux)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", cfg.Addr)
		log.Printf("  dashboard:   http://localhost%s/", normAddr(cfg.Addr))
		log.Printf("  kosync URL:  http://<host>%s/  (set this in KOReader Progress sync)", normAddr(cfg.Addr))
		if lib != nil {
			log.Printf("  OPDS feed:   http://<host>%s/opds", normAddr(cfg.Addr))
		}
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Printf("shutting down…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func normAddr(a string) string {
	if len(a) > 0 && a[0] == ':' {
		return a
	}
	return a
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
