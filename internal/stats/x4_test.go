package stats

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/justindickey/booky/internal/store"
)

func pct(v float64) *float64 { return &v }

func bookByMD5(s Summary, md5 string) (BookStat, bool) {
	for _, b := range s.Books {
		if b.MD5 == md5 {
			return b, true
		}
	}
	return BookStat{}, false
}

// The X4's reading must show up even though it has no page_stat rows.
func TestComputeIncludesX4Books(t *testing.T) {
	st := testStore(t)
	// One Kobo book with 10 minutes of dated reading.
	testBook(t, st, "kobo1", "Hyperion", 300)
	now := time.Now().Unix()
	for p := int64(1); p <= 10; p++ {
		testPage(t, st, "kobo1", p, now-3600+p*60, 60)
	}
	if _, err := st.UpsertX4Books([]store.X4Book{
		// Read to the end but never "Mark Finished" on the device.
		{MD5: "x4done", Title: "So Long", Sessions: 16, Seconds: 12960, PagesTurned: 398, Percent: pct(100)},
		// Marked finished on the device.
		{MD5: "x4flag", Title: "Mostly Harmless", Sessions: 19, Seconds: 15943, PagesTurned: 541, Completed: true, Percent: pct(80)},
		// In progress, with a device estimate of time left.
		{MD5: "x4wip", Title: "Between Two Fires", Sessions: 5, Seconds: 3240, PagesTurned: 93, Percent: pct(10.66), EstLeftSecs: 20000},
	}); err != nil {
		t.Fatal(err)
	}

	s, err := Compute(st, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if s.BooksTracked != 4 || s.BooksFinished != 2 {
		t.Errorf("tracked=%d finished=%d, want 4 and 2", s.BooksTracked, s.BooksFinished)
	}
	if want := int64(600 + 12960 + 15943 + 3240); s.TotalSeconds != want {
		t.Errorf("total seconds = %d, want %d", s.TotalSeconds, want)
	}
	if want := int64(10 + 398 + 541 + 93); s.TotalPages != want {
		t.Errorf("total pages = %d, want %d", s.TotalPages, want)
	}
	// Undated X4 time stays out of the dated series.
	if s.DaysRead != 1 {
		t.Errorf("days read = %d, want 1 (Kobo only)", s.DaysRead)
	}
	if s.AvgPagesPerDay != 10 {
		t.Errorf("avg pages/day = %v, want 10 (dated pages only)", s.AvgPagesPerDay)
	}

	wip, ok := bookByMD5(s, "x4wip")
	if !ok || wip.Device != "x4" || wip.Finished || wip.Percent != 10.66 || wip.ForecastSecs != 20000 {
		t.Errorf("wip: %+v", wip)
	}
	if wip.LastOpenSource != "upload" || wip.LastOpen == 0 {
		t.Errorf("wip last open should fall back to the upload time: %+v", wip)
	}
	if b, _ := bookByMD5(s, "x4done"); !b.Finished || b.FinishedAt != 0 {
		t.Errorf("x4done should be finished with no date: %+v", b)
	}
	if b, _ := bookByMD5(s, "kobo1"); b.Device != "kobo" {
		t.Errorf("kobo book device = %q", b.Device)
	}
}

// A kosync push newer than the card upload is the fresher position.
func TestX4SyncUpdatesPosition(t *testing.T) {
	st := testStore(t)
	if _, err := st.UpsertX4Books([]store.X4Book{{MD5: "x4wip", Title: "Dune", Seconds: 600, PagesTurned: 20, Percent: pct(10)}}); err != nil {
		t.Fatal(err)
	}
	// PutProgress stamps "now"; make sure it lands after the upload.
	time.Sleep(1100 * time.Millisecond)
	if _, err := st.PutProgress("justin", "x4wip", store.Progress{Percentage: 0.42, Device: "X4"}, "", "", ""); err != nil {
		t.Fatal(err)
	}
	s, err := Compute(st, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := bookByMD5(s, "x4wip")
	if b.Percent != 42 || b.LastOpenSource != "sync" {
		t.Errorf("got %+v", b)
	}
}

// Same file read on both devices: one row, times added together.
func TestX4MergesWithKoboBook(t *testing.T) {
	st := testStore(t)
	testBook(t, st, "same", "Dune", 400)
	testPage(t, st, "same", 1, time.Now().Unix()-600, 300)
	if _, err := st.UpsertX4Books([]store.X4Book{{MD5: "same", Title: "Dune", Seconds: 900, PagesTurned: 30, Percent: pct(20)}}); err != nil {
		t.Fatal(err)
	}
	s, err := Compute(st, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Books) != 1 || s.BooksTracked != 1 {
		t.Fatalf("want one merged book, got %d books tracked=%d", len(s.Books), s.BooksTracked)
	}
	b := s.Books[0]
	if b.Device != "kobo+x4" || b.Seconds != 1200 || b.PagesRead != 31 {
		t.Errorf("got %+v", b)
	}
}

// The Kobo's stats upload prunes books it doesn't know about. X4 books live
// in their own table and must survive it.
func TestKoboIngestLeavesX4BooksAlone(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "booky.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertX4Books([]store.X4Book{{MD5: "x4only", Title: "Red Rising", Seconds: 60}}); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "statistics.sqlite3")
	makeKOReaderDB(t, src)
	if _, _, err := Ingest(st, src); err != nil {
		t.Fatal(err)
	}
	books, err := st.X4Books()
	if err != nil || len(books) != 1 || books[0].MD5 != "x4only" {
		t.Errorf("x4 books after Kobo ingest: %+v err=%v", books, err)
	}
}
