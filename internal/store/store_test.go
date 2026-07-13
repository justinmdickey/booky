package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrateAddsExcludedColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.sqlite3")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE book (md5 TEXT PRIMARY KEY, title TEXT, authors TEXT, series TEXT,
		language TEXT, pages INTEGER, last_open INTEGER, highlights INTEGER, notes INTEGER,
		total_read_time INTEGER, total_read_pages INTEGER);
		INSERT INTO book(md5,title) VALUES('abc','Old Book');`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open on old schema: %v", err)
	}
	var excl int
	if err := st.DB().QueryRow(`SELECT excluded FROM book WHERE md5='abc'`).Scan(&excl); err != nil {
		t.Fatalf("excluded column missing after migrate: %v", err)
	}
	if excl != 0 {
		t.Errorf("default excluded = %d, want 0", excl)
	}
	if ok, err := st.SetBookExcluded("abc", true); err != nil || !ok {
		t.Fatalf("toggle: ok=%v err=%v", ok, err)
	}
	st.Close()

	// Reopen: duplicate-column ALTER must be tolerated, value preserved.
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	st2.DB().QueryRow(`SELECT excluded FROM book WHERE md5='abc'`).Scan(&excl)
	if excl != 1 {
		t.Errorf("excluded lost on reopen: %d", excl)
	}
}
