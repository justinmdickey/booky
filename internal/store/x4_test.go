package store

import (
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "booky.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func pct(v float64) *float64 { return &v }

func TestUpsertX4BooksKeepsTotalsMonotonic(t *testing.T) {
	st := openTest(t)
	first := []X4Book{
		{MD5: "aaa", Title: "Dune", Sessions: 4, Seconds: 3600, PagesTurned: 90, Percent: pct(40)},
		{MD5: "bbb", Title: "Red Rising", Sessions: 1, Seconds: 60, PagesTurned: 2},
	}
	if n, err := st.UpsertX4Books(first); err != nil || n != 2 {
		t.Fatalf("first upload: n=%d err=%v", n, err)
	}
	// Same upload again changes nothing.
	if _, err := st.UpsertX4Books(first); err != nil {
		t.Fatal(err)
	}
	// An older card snapshot (lower totals, one book missing) must not roll
	// totals back or delete the missing book.
	older := []X4Book{{MD5: "aaa", Title: "Dune", Sessions: 2, Seconds: 1000, PagesTurned: 30, Percent: pct(55), StartDate: "2026-09-01"}}
	if _, err := st.UpsertX4Books(older); err != nil {
		t.Fatal(err)
	}

	books, err := st.X4Books()
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 2 {
		t.Fatalf("want 2 books, got %d", len(books))
	}
	got := map[string]X4Book{}
	for _, b := range books {
		got[b.MD5] = b
	}
	d := got["aaa"]
	if d.Sessions != 4 || d.Seconds != 3600 || d.PagesTurned != 90 {
		t.Errorf("totals rolled back: %+v", d)
	}
	if d.Percent == nil || *d.Percent != 55 || d.StartDate != "2026-09-01" {
		t.Errorf("position/date should take the newest upload: %+v", d)
	}
	if got["bbb"].Percent != nil {
		t.Errorf("missing percent should stay NULL: %+v", got["bbb"])
	}
}

func TestSetBookExcludedCoversX4(t *testing.T) {
	st := openTest(t)
	if _, err := st.UpsertX4Books([]X4Book{{MD5: "aaa", Title: "Dune"}}); err != nil {
		t.Fatal(err)
	}
	ok, err := st.SetBookExcluded("aaa", true)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	books, _ := st.X4Books()
	if !books[0].Excluded {
		t.Error("x4 book not excluded")
	}
}

func TestLatestSyncs(t *testing.T) {
	st := openTest(t)
	if _, err := st.PutProgress("justin", "aaa", Progress{Percentage: 0.25, Device: "X4"}, "", "", ""); err != nil {
		t.Fatal(err)
	}
	syncs, err := st.LatestSyncs()
	if err != nil {
		t.Fatal(err)
	}
	s := syncs["aaa"]
	if s.Percent != 25 || s.Timestamp == 0 || s.Device != "X4" {
		t.Errorf("got %+v", s)
	}
}
