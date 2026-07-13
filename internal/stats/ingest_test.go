package stats

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/justindickey/booky/internal/store"
	_ "modernc.org/sqlite"
)

// makeKOReaderDB builds a synthetic statistics.sqlite3 in the modern schema.
func makeKOReaderDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE book (id INTEGER PRIMARY KEY, title TEXT, authors TEXT, notes INTEGER,
  last_open INTEGER, highlights INTEGER, pages INTEGER, series TEXT, language TEXT,
  md5 TEXT, total_read_time INTEGER, total_read_pages INTEGER);
CREATE TABLE page_stat_data (id_book INTEGER, page INTEGER, start_time INTEGER,
  duration INTEGER, total_pages INTEGER, UNIQUE(id_book,page,start_time));`)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO book VALUES (1,'Dune','Frank Herbert',2,?,5,400,'','eng','md5dune',0,0)`, time.Now().Unix())
	db.Exec(`INSERT INTO book VALUES (2,'Hyperion','Dan Simmons',0,?,0,300,'','eng','md5hyp',0,0)`, time.Now().Unix())

	// Three days of reading on Dune, one big session today.
	base := time.Now().Add(-72 * time.Hour).Unix()
	for d := 0; d < 3; d++ {
		dayStart := base + int64(d)*86400
		for p := 1; p <= 20; p++ {
			db.Exec(`INSERT INTO page_stat_data VALUES (1,?,?,?,400)`,
				d*20+p, dayStart+int64(p)*60, 55)
		}
	}
	// A separate session on Hyperion today.
	now := time.Now().Unix()
	for p := 1; p <= 10; p++ {
		db.Exec(`INSERT INTO page_stat_data VALUES (2,?,?,?,300)`, p, now-int64(600-p*60), 50)
	}
}

// TestIngestPrunesStaleBooks verifies that a book present in an earlier upload
// but absent from a later one is removed — the re-pagination/metadata-rewrite
// case where a book's md5 changes and the old row would otherwise linger.
func TestIngestPrunesStaleBooks(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "booky.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// First upload: two books.
	ko1 := filepath.Join(dir, "stats1.sqlite3")
	makeKOReaderDB(t, ko1)
	if _, _, err := Ingest(st, ko1); err != nil {
		t.Fatalf("ingest1: %v", err)
	}

	// Second upload: only one book (md5dune), simulating md5hyp going stale.
	ko2 := filepath.Join(dir, "stats2.sqlite3")
	db, err := sql.Open("sqlite", "file:"+ko2)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE book (id INTEGER PRIMARY KEY, title TEXT, authors TEXT, notes INTEGER,
	  last_open INTEGER, highlights INTEGER, pages INTEGER, series TEXT, language TEXT,
	  md5 TEXT, total_read_time INTEGER, total_read_pages INTEGER);
	CREATE TABLE page_stat_data (id_book INTEGER, page INTEGER, start_time INTEGER,
	  duration INTEGER, total_pages INTEGER, UNIQUE(id_book,page,start_time));`)
	db.Exec(`INSERT INTO book VALUES (1,'Dune','Frank Herbert',0,?,0,400,'','eng','md5dune',0,0)`, time.Now().Unix())
	db.Exec(`INSERT INTO page_stat_data VALUES (1,1,?,55,400)`, time.Now().Unix())
	db.Close()

	if _, _, err := Ingest(st, ko2); err != nil {
		t.Fatalf("ingest2: %v", err)
	}

	var nBooks, nHypPages int
	st.DB().QueryRow(`SELECT COUNT(*) FROM book`).Scan(&nBooks)
	if nBooks != 1 {
		t.Errorf("expected 1 book after prune, got %d", nBooks)
	}
	st.DB().QueryRow(`SELECT COUNT(*) FROM page_stat WHERE md5='md5hyp'`).Scan(&nHypPages)
	if nHypPages != 0 {
		t.Errorf("stale page_stat rows not pruned: %d", nHypPages)
	}
}

// TestExcludedBooks verifies both exclusion paths: an ingest-time pattern
// match and a manual toggle. Excluded books must vanish from every aggregate
// but stay listed (flagged) for the management UI, and a manual exclusion must
// survive a re-ingest.
func TestExcludedBooks(t *testing.T) {
	dir := t.TempDir()
	koPath := filepath.Join(dir, "statistics.sqlite3")
	makeKOReaderDB(t, koPath)

	st, err := store.Open(filepath.Join(dir, "booky.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Pattern exclusion at ingest: Hyperion matches, Dune doesn't.
	if _, _, err := Ingest(st, koPath, "hyperion"); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	sum, err := Compute(st, time.Local)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if sum.BooksTracked != 1 {
		t.Errorf("books tracked = %d, want 1 (Hyperion pattern-excluded)", sum.BooksTracked)
	}
	if want := int64(60 * 55); sum.TotalSeconds != want { // Dune only
		t.Errorf("total seconds = %d want %d", sum.TotalSeconds, want)
	}
	if len(sum.Books) != 2 {
		t.Fatalf("excluded book missing from Books list: %d", len(sum.Books))
	}
	for _, b := range sum.Books {
		if want := b.MD5 == "md5hyp"; b.Excluded != want {
			t.Errorf("book %s excluded=%v want %v", b.MD5, b.Excluded, want)
		}
	}
	for _, s := range sum.RecentSessions {
		if s.MD5 == "md5hyp" {
			t.Error("excluded book leaked into recent sessions")
		}
	}

	// Manual exclusion sticks across a re-ingest without patterns.
	if ok, err := st.SetBookExcluded("md5dune", true); err != nil || !ok {
		t.Fatalf("SetBookExcluded: ok=%v err=%v", ok, err)
	}
	if _, _, err := Ingest(st, koPath); err != nil {
		t.Fatalf("re-ingest: %v", err)
	}
	sum, err = Compute(st, time.Local)
	if err != nil {
		t.Fatalf("compute2: %v", err)
	}
	if sum.TotalSeconds != 0 || sum.BooksTracked != 0 {
		t.Errorf("manual exclusion lost on re-ingest: secs=%d tracked=%d", sum.TotalSeconds, sum.BooksTracked)
	}
	if sum.DaysRead != 0 || len(sum.RecentSessions) != 0 {
		t.Errorf("excluded pages still in daily/sessions: days=%d sessions=%d", sum.DaysRead, len(sum.RecentSessions))
	}

	// Re-include restores counting.
	if ok, err := st.SetBookExcluded("md5dune", false); err != nil || !ok {
		t.Fatalf("re-include: ok=%v err=%v", ok, err)
	}
	sum, err = Compute(st, time.Local)
	if err != nil {
		t.Fatalf("compute3: %v", err)
	}
	if want := int64(60 * 55); sum.TotalSeconds != want {
		t.Errorf("after re-include total seconds = %d want %d", sum.TotalSeconds, want)
	}
}

func TestIngestAndCompute(t *testing.T) {
	dir := t.TempDir()
	koPath := filepath.Join(dir, "statistics.sqlite3")
	makeKOReaderDB(t, koPath)

	st, err := store.Open(filepath.Join(dir, "booky.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	books, pages, err := Ingest(st, koPath)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if books != 2 {
		t.Errorf("expected 2 books, got %d", books)
	}
	if pages != 70 { // 60 dune + 10 hyperion
		t.Errorf("expected 70 page stats, got %d", pages)
	}

	// Idempotency: re-ingest should not duplicate rows.
	if _, _, err := Ingest(st, koPath); err != nil {
		t.Fatalf("re-ingest: %v", err)
	}
	var n int
	st.DB().QueryRow(`SELECT COUNT(*) FROM page_stat`).Scan(&n)
	if n != 70 {
		t.Errorf("re-ingest duplicated rows: %d", n)
	}

	sum, err := Compute(st, time.Local)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if sum.BooksTracked != 2 {
		t.Errorf("books tracked = %d", sum.BooksTracked)
	}
	wantSecs := int64(60*55 + 10*50) // 60 Dune @55s + 10 Hyperion @50s
	if sum.TotalSeconds != wantSecs {
		t.Errorf("total seconds = %d want %d", sum.TotalSeconds, wantSecs)
	}
	if sum.DaysRead < 1 {
		t.Errorf("days read = %d", sum.DaysRead)
	}
	if len(sum.RecentSessions) == 0 {
		t.Error("expected recent sessions")
	}
	if sum.CurrentStreak < 1 {
		t.Errorf("expected a current streak, got %d", sum.CurrentStreak)
	}
	if len(sum.Heatmap) != 365 {
		t.Errorf("heatmap should be 365 days, got %d", len(sum.Heatmap))
	}
}
