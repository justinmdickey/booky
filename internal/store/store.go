// Package store is Booky's own persistence layer: kosync users + progress, and
// the ingested copy of KOReader reading statistics.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	// WAL + busy timeout: Booky reads (dashboard) and writes (sync/ingest)
	// concurrently.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS users (
    username   TEXT PRIMARY KEY,
    key        TEXT NOT NULL,          -- md5(password) hex, as KOReader sends it
    created_at INTEGER NOT NULL
);

-- One row per (user, document) — document is the kosync hash (partial-MD5 by default).
CREATE TABLE IF NOT EXISTS progress (
    username   TEXT NOT NULL,
    document   TEXT NOT NULL,
    percentage REAL,
    progress   TEXT,
    device     TEXT,
    device_id  TEXT,
    title      TEXT,                   -- from optional metadata
    authors    TEXT,
    filename   TEXT,
    timestamp  INTEGER NOT NULL,
    PRIMARY KEY (username, document)
);
CREATE INDEX IF NOT EXISTS progress_ts ON progress(timestamp);

-- Ingested KOReader book rows. Keyed by md5 (the partial-MD5 fingerprint) which
-- is stable across re-ingests of the same book. We merge by md5 on upload.
CREATE TABLE IF NOT EXISTS book (
    md5              TEXT PRIMARY KEY,
    title            TEXT,
    authors          TEXT,
    series           TEXT,
    language         TEXT,
    pages            INTEGER,
    last_open        INTEGER,
    highlights       INTEGER,
    notes            INTEGER,
    total_read_time  INTEGER,
    total_read_pages INTEGER
);

-- Per-page reading sessions, mirrored from KOReader's page_stat_data, keyed by
-- book md5 so they survive the device's autoincrement ids. UNIQUE dedupes
-- re-uploads of overlapping windows.
CREATE TABLE IF NOT EXISTS page_stat (
    md5         TEXT NOT NULL,
    page        INTEGER NOT NULL,
    start_time  INTEGER NOT NULL,
    duration    INTEGER NOT NULL,
    total_pages INTEGER NOT NULL,
    PRIMARY KEY (md5, page, start_time)
);
CREATE INDEX IF NOT EXISTS page_stat_start ON page_stat(start_time);

-- User-curated collections for the OPDS feed ("Want to read", "On deck", ...).
CREATE TABLE IF NOT EXISTS collection (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    name  TEXT NOT NULL UNIQUE,
    icon  TEXT,
    sort  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS collection_book (
    collection_id INTEGER NOT NULL REFERENCES collection(id) ON DELETE CASCADE,
    calibre_id    INTEGER NOT NULL,    -- books.id in Calibre's metadata.db
    added_at      INTEGER NOT NULL,
    PRIMARY KEY (collection_id, calibre_id)
);

CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT);
`
	_, err := s.db.Exec(schema)
	return err
}

// ---- Users ----

var ErrUserExists = errors.New("username already registered")

func (s *Store) CreateUser(username, key string) error {
	_, err := s.db.Exec(`INSERT INTO users(username,key,created_at) VALUES(?,?,?)`,
		username, key, time.Now().Unix())
	if err != nil {
		// modernc returns a generic error; detect uniqueness by re-query.
		if s.UserExists(username) {
			return ErrUserExists
		}
		return err
	}
	return nil
}

func (s *Store) UserExists(username string) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM users WHERE username=?`, username).Scan(&n)
	return n > 0
}

// CheckAuth returns true if the username exists and the supplied key (md5 hex)
// matches.
func (s *Store) CheckAuth(username, key string) bool {
	var stored string
	err := s.db.QueryRow(`SELECT key FROM users WHERE username=?`, username).Scan(&stored)
	if err != nil {
		return false
	}
	return stored == key
}

// ---- Progress ----

type Progress struct {
	Document   string  `json:"document,omitempty"`
	Percentage float64 `json:"percentage,omitempty"`
	Progress   string  `json:"progress,omitempty"`
	Device     string  `json:"device,omitempty"`
	DeviceID   string  `json:"device_id,omitempty"`
	Timestamp  int64   `json:"timestamp,omitempty"`
}

func (s *Store) PutProgress(username, document string, p Progress, title, authors, filename string) (int64, error) {
	ts := time.Now().Unix()
	_, err := s.db.Exec(`
INSERT INTO progress(username,document,percentage,progress,device,device_id,title,authors,filename,timestamp)
VALUES(?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(username,document) DO UPDATE SET
  percentage=excluded.percentage, progress=excluded.progress,
  device=excluded.device, device_id=excluded.device_id,
  title=COALESCE(NULLIF(excluded.title,''), progress.title),
  authors=COALESCE(NULLIF(excluded.authors,''), progress.authors),
  filename=COALESCE(NULLIF(excluded.filename,''), progress.filename),
  timestamp=excluded.timestamp`,
		username, document, p.Percentage, p.Progress, p.Device, p.DeviceID,
		title, authors, filename, ts)
	return ts, err
}

// GetProgress returns the stored progress for a document, and whether a row
// exists.
func (s *Store) GetProgress(username, document string) (Progress, bool) {
	var p Progress
	err := s.db.QueryRow(`
SELECT document,percentage,progress,device,device_id,timestamp
FROM progress WHERE username=? AND document=?`, username, document).
		Scan(&p.Document, &p.Percentage, &p.Progress, &p.Device, &p.DeviceID, &p.Timestamp)
	if err != nil {
		return Progress{}, false
	}
	return p, true
}
