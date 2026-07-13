// Package store is Booky's own persistence layer: kosync users + progress, and
// the ingested copy of KOReader reading statistics.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
    total_read_pages INTEGER,
    excluded         INTEGER NOT NULL DEFAULT 0  -- 1 = leave out of reading stats
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

-- Dashboard/OPDS accounts (bcrypt hashes), separate from kosync users whose
-- keys are md5 hex fixed by the KOReader sync protocol.
CREATE TABLE IF NOT EXISTS web_user (
    username   TEXT PRIMARY KEY,
    pass_hash  TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

-- Browser sessions for the dashboard. Only a SHA-256 of the token is stored.
CREATE TABLE IF NOT EXISTS web_session (
    token_hash TEXT PRIMARY KEY,
    username   TEXT NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS web_session_exp ON web_session(expires_at);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// Columns added after the initial release; ALTER is a no-op error on DBs
	// that already have them.
	if _, err := s.db.Exec(`ALTER TABLE book ADD COLUMN excluded INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column name") {
		return err
	}
	return nil
}

// SetBookExcluded flips whether a book counts toward reading stats. Reports
// whether a row with that md5 existed.
func (s *Store) SetBookExcluded(md5 string, excluded bool) (bool, error) {
	res, err := s.db.Exec(`UPDATE book SET excluded=? WHERE md5=?`, excluded, md5)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetBooksExcluded bulk-applies the excluded flag to every book whose md5 is
// in the list (unknown md5s are ignored — a file in an ignored folder that was
// never opened has no stats to exclude). Returns how many rows changed.
func (s *Store) SetBooksExcluded(md5s []string, excluded bool) (int64, error) {
	if len(md5s) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	up, err := tx.Prepare(`UPDATE book SET excluded=? WHERE md5=? AND excluded != ?`)
	if err != nil {
		return 0, err
	}
	defer up.Close()
	var changed int64
	for _, md5 := range md5s {
		res, err := up.Exec(excluded, md5, excluded)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		changed += n
	}
	return changed, tx.Commit()
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

// ---- Web users & sessions ----

// CountWebUsers reports how many dashboard accounts exist (0 = first run).
func (s *Store) CountWebUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM web_user`).Scan(&n)
	return n, err
}

func (s *Store) CreateWebUser(username, passHash string) error {
	_, err := s.db.Exec(`INSERT INTO web_user(username,pass_hash,created_at) VALUES(?,?,?)`,
		username, passHash, time.Now().Unix())
	return err
}

// WebUserHash returns the stored bcrypt hash for a username.
func (s *Store) WebUserHash(username string) (string, bool) {
	var h string
	err := s.db.QueryRow(`SELECT pass_hash FROM web_user WHERE username=?`, username).Scan(&h)
	return h, err == nil
}

// UpdateWebUser renames a user and/or replaces their password hash. Sessions
// follow the rename so the caller stays logged in.
func (s *Store) UpdateWebUser(oldName, newName, passHash string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE web_user SET username=?, pass_hash=? WHERE username=?`,
		newName, passHash, oldName); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE web_session SET username=? WHERE username=?`, newName, oldName); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CreateSession(tokenHash, username string, expiresAt int64) error {
	_, err := s.db.Exec(`INSERT INTO web_session(token_hash,username,expires_at) VALUES(?,?,?)`,
		tokenHash, username, expiresAt)
	return err
}

// SessionUser resolves a token hash to a username, expiring stale rows lazily.
func (s *Store) SessionUser(tokenHash string) (string, bool) {
	var user string
	var exp int64
	err := s.db.QueryRow(`SELECT username, expires_at FROM web_session WHERE token_hash=?`, tokenHash).
		Scan(&user, &exp)
	if err != nil {
		return "", false
	}
	if time.Now().Unix() > exp {
		_, _ = s.db.Exec(`DELETE FROM web_session WHERE expires_at < ?`, time.Now().Unix())
		return "", false
	}
	return user, true
}

func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM web_session WHERE token_hash=?`, tokenHash)
	return err
}

// DeleteUserSessions logs a user out everywhere (used after password change).
func (s *Store) DeleteUserSessions(username string) error {
	_, err := s.db.Exec(`DELETE FROM web_session WHERE username=?`, username)
	return err
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
