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

	// OPDSUser/OPDSPass seed the first dashboard account on an empty database,
	// then are ignored — credentials live in the DB and are managed in the UI.
	OPDSUser string
	OPDSPass string

	// AllowRegistration lets new kosync users self-register via POST /users/create.
	AllowRegistration bool

	// PublicURL is the externally reachable base URL (used in OPDS links).
	PublicURL string

	// StatsExclude holds case-insensitive substrings matched against a book's
	// title and authors at ingest time; matches are excluded from reading stats
	// (e.g. "wallabag" to keep synced articles out). Comma-separated in the env.
	StatsExclude []string
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
		StatsExclude:      envList("BOOKY_STATS_EXCLUDE"),
	}
	return c
}

func envList(key string) []string {
	var out []string
	for _, p := range strings.Split(env(key, ""), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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
