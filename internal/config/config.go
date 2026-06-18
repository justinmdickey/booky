// Package config loads Booky's runtime configuration from environment variables.
package config

import (
	"os"
	"strconv"
	"strings"
)

// Config holds all tunables. Everything has a sensible default so Booky runs
// with zero configuration for a quick try, and is fully driven by env vars in
// a container.
type Config struct {
	// Addr is the listen address for the HTTP server.
	Addr string
	// DataDir is where Booky keeps its own SQLite database and uploaded files.
	DataDir string

	// CalibreLibrary is the path to the Calibre library root (the directory
	// containing metadata.db). Mounted read-only. Empty disables library
	// integration.
	CalibreLibrary string

	// OPDSUser/OPDSPass gate the OPDS feed and stats-upload endpoints with
	// HTTP Basic auth. If OPDSUser is empty, those endpoints are open.
	OPDSUser string
	OPDSPass string

	// AllowRegistration lets new kosync users self-register via POST /users/create.
	AllowRegistration bool

	// PublicURL is the externally reachable base URL (used in OPDS links).
	PublicURL string
}

func Load() Config {
	c := Config{
		Addr:              env("BOOKY_ADDR", ":8222"),
		DataDir:           env("BOOKY_DATA_DIR", "./data"),
		CalibreLibrary:    env("BOOKY_CALIBRE_LIBRARY", ""),
		OPDSUser:          env("BOOKY_AUTH_USER", ""),
		OPDSPass:          env("BOOKY_AUTH_PASS", ""),
		AllowRegistration: envBool("BOOKY_ALLOW_REGISTRATION", true),
		PublicURL:         strings.TrimRight(env("BOOKY_PUBLIC_URL", ""), "/"),
	}
	return c
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err == nil {
			return b
		}
	}
	return def
}
