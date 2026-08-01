package stats

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/justindickey/booky/internal/store"
)

// testBook inserts a book row directly into Booky's own schema (as opposed to
// makeKOReaderDB, which builds a source statistics.sqlite3 for Ingest).
func testBook(t *testing.T, st *store.Store, md5, title string, pages int64) {
	t.Helper()
	_, err := st.DB().Exec(`
INSERT INTO book(md5,title,authors,series,language,pages,last_open,highlights,notes,total_read_time,total_read_pages,excluded)
VALUES(?,?,?,?,?,?,?,?,?,?,?,0)`, md5, title, "", "", "", pages, 0, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
}

func testPage(t *testing.T, st *store.Store, md5 string, page, start, dur int64) {
	t.Helper()
	_, err := st.DB().Exec(`INSERT INTO page_stat(md5,page,start_time,duration,total_pages) VALUES(?,?,?,?,0)`,
		md5, page, start, dur)
	if err != nil {
		t.Fatal(err)
	}
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "booky.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestFinishedAt verifies FinishedAt is the FIRST time the last page was
// reached, not the most recent, and that an unfinished book gets 0.
func TestFinishedAt(t *testing.T) {
	st := testStore(t)

	// Finished book: reaches the last page (99, pages=100) at t1, then
	// revisits that page later at t2. FinishedAt must be t1.
	testBook(t, st, "md5fin", "Finished Book", 100)
	t1 := int64(1_600_000_000)
	t2 := t1 + 86400
	testPage(t, st, "md5fin", 50, t1-100, 30)
	testPage(t, st, "md5fin", 99, t1, 60)
	testPage(t, st, "md5fin", 99, t2, 60)

	// Unfinished book: never reaches its last page.
	testBook(t, st, "md5unfin", "Unfinished Book", 200)
	testPage(t, st, "md5unfin", 10, t1, 60)

	sum, err := Compute(st, time.UTC)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}

	var fin, unfin *BookStat
	for i := range sum.Books {
		switch sum.Books[i].MD5 {
		case "md5fin":
			fin = &sum.Books[i]
		case "md5unfin":
			unfin = &sum.Books[i]
		}
	}
	if fin == nil || unfin == nil {
		t.Fatalf("missing books in summary: fin=%v unfin=%v", fin, unfin)
	}
	if !fin.Finished {
		t.Fatal("expected md5fin to be finished")
	}
	if fin.FinishedAt != t1 {
		t.Errorf("finished_at = %d, want first-reached time %d (not re-read time %d)", fin.FinishedAt, t1, t2)
	}
	if unfin.Finished {
		t.Fatal("expected md5unfin to be unfinished")
	}
	if unfin.FinishedAt != 0 {
		t.Errorf("unfinished book finished_at = %d, want 0", unfin.FinishedAt)
	}
}

// TestForecastSecs verifies a recently-active unfinished book gets a positive
// reading-time forecast at its own pace, and a finished book gets 0.
func TestForecastSecs(t *testing.T) {
	st := testStore(t)
	now := time.Now().UTC()

	// Unfinished, actively read over the last few days: 5 distinct pages
	// spread across 2 distinct days within the last 14 days.
	testBook(t, st, "md5active", "Active Book", 200)
	testPage(t, st, "md5active", 1, now.AddDate(0, 0, -2).Unix(), 60)
	testPage(t, st, "md5active", 2, now.AddDate(0, 0, -2).Unix()+60, 60)
	testPage(t, st, "md5active", 3, now.AddDate(0, 0, -1).Unix(), 60)
	testPage(t, st, "md5active", 4, now.AddDate(0, 0, -1).Unix()+60, 60)
	testPage(t, st, "md5active", 5, now.AddDate(0, 0, -1).Unix()+120, 60)

	// Finished book: no forecast expected regardless of recent activity.
	testBook(t, st, "md5done", "Done Book", 10)
	testPage(t, st, "md5done", 9, now.AddDate(0, 0, -1).Unix(), 60)

	sum, err := Compute(st, time.UTC)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}

	var active, done *BookStat
	for i := range sum.Books {
		switch sum.Books[i].MD5 {
		case "md5active":
			active = &sum.Books[i]
		case "md5done":
			done = &sum.Books[i]
		}
	}
	if active == nil || done == nil {
		t.Fatalf("missing books: active=%v done=%v", active, done)
	}
	if active.ForecastSecs <= 0 {
		t.Errorf("active book forecast_seconds = %v, want > 0", active.ForecastSecs)
	}
	// 5 pages in 300s => 60 pph; 195 pages remain => 195/60 h = 11700s.
	if got, want := active.ForecastSecs, int64(11700); got != want {
		t.Errorf("active book forecast_seconds = %d, want %d", got, want)
	}
	if !done.Finished {
		t.Fatal("expected md5done to be finished")
	}
	if done.ForecastSecs != 0 {
		t.Errorf("finished book forecast_seconds = %v, want 0", done.ForecastSecs)
	}
}

// TestPunchcard verifies a punchcard cell matches a known event's
// weekday/hour, pinned to time.UTC for determinism.
func TestPunchcard(t *testing.T) {
	st := testStore(t)
	testBook(t, st, "md5p", "Punchcard Book", 100)

	// 2024-03-14 is a Thursday; pin an event at 15:00 UTC.
	start := time.Date(2024, 3, 14, 15, 0, 0, 0, time.UTC)
	testPage(t, st, "md5p", 1, start.Unix(), 42)

	sum, err := Compute(st, time.UTC)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	weekday := int(start.Weekday()) // Thursday = 4
	hour := start.Hour()            // 15
	if got := sum.Punchcard[weekday][hour]; got != 42 {
		t.Errorf("punchcard[%d][%d] = %d, want 42", weekday, hour, got)
	}
	// Every other cell should be untouched.
	var total int64
	for w := 0; w < 7; w++ {
		for h := 0; h < 24; h++ {
			total += sum.Punchcard[w][h]
		}
	}
	if total != 42 {
		t.Errorf("punchcard total = %d, want 42 (only one event)", total)
	}
}

// TestMonthlyRollup verifies the monthly series is zero-filled across a gap
// month and correctly attributes a finished book to its finish month.
func TestMonthlyRollup(t *testing.T) {
	st := testStore(t)

	// Anchor on day 15 of the current month (UTC) to dodge month-length
	// edge cases (e.g. subtracting months from the 31st).
	now := time.Now().UTC()
	anchor := time.Date(now.Year(), now.Month(), 15, 12, 0, 0, 0, time.UTC)
	monthMinus3 := anchor.AddDate(0, -3, 0)
	monthMinus2 := anchor.AddDate(0, -2, 0) // left as a gap: no activity here
	monthMinus1 := anchor.AddDate(0, -1, 0)

	testBook(t, st, "md5month", "Monthly Book", 10)
	// Finishes 3 months ago (reaches page 9, the last page for pages=10).
	testPage(t, st, "md5month", 9, monthMinus3.Unix(), 60)
	// More (post-finish) activity 1 month ago.
	testPage(t, st, "md5month", 5, monthMinus1.Unix(), 30)

	sum, err := Compute(st, time.UTC)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}

	byMonth := map[string]MonthPoint{}
	for _, mp := range sum.Monthly {
		byMonth[mp.Month] = mp
	}

	gapKey := monthMinus2.Format("2006-01")
	gap, ok := byMonth[gapKey]
	if !ok {
		t.Fatalf("gap month %s missing from Monthly (not zero-filled): %+v", gapKey, sum.Monthly)
	}
	if gap.Seconds != 0 || gap.Pages != 0 || gap.BooksFinished != 0 {
		t.Errorf("gap month %s = %+v, want all zero", gapKey, gap)
	}

	finKey := monthMinus3.Format("2006-01")
	fin, ok := byMonth[finKey]
	if !ok {
		t.Fatalf("finish month %s missing from Monthly", finKey)
	}
	if fin.BooksFinished != 1 {
		t.Errorf("finish month %s books_finished = %d, want 1", finKey, fin.BooksFinished)
	}
	if fin.Seconds != 60 || fin.Pages != 1 {
		t.Errorf("finish month %s = %+v, want seconds=60 pages=1", finKey, fin)
	}

	lastKey := monthMinus1.Format("2006-01")
	last, ok := byMonth[lastKey]
	if !ok {
		t.Fatalf("month %s missing from Monthly", lastKey)
	}
	if last.BooksFinished != 0 {
		t.Errorf("month %s books_finished = %d, want 0 (already finished earlier)", lastKey, last.BooksFinished)
	}

	curKey := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
	if sum.Monthly[len(sum.Monthly)-1].Month != curKey {
		t.Errorf("last monthly entry = %s, want current month %s", sum.Monthly[len(sum.Monthly)-1].Month, curKey)
	}
	if sum.Monthly[0].Month != finKey {
		t.Errorf("first monthly entry = %s, want first month with data %s", sum.Monthly[0].Month, finKey)
	}
}

// TestPercentIsPosition: a brief peek deep into a book (one event at page
// 3137 of 3695) must not set progress — percent tracks the newest event's
// position. Finishing still keys off the furthest page ever reached, and a
// finished book pins to 100 even after flipping back.
func TestPercentIsPosition(t *testing.T) {
	st := testStore(t)
	now := time.Now().UTC()

	testBook(t, st, "md5omni", "Omnibus", 3695)
	testPage(t, st, "md5omni", 3137, now.AddDate(0, 0, -10).Unix(), 57) // the peek
	testPage(t, st, "md5omni", 285, now.AddDate(0, 0, -1).Unix(), 60)
	testPage(t, st, "md5omni", 286, now.AddDate(0, 0, -1).Unix()+60, 60) // position

	testBook(t, st, "md5rr", "Reread", 100)
	testPage(t, st, "md5rr", 99, now.AddDate(0, 0, -5).Unix(), 60) // finished
	testPage(t, st, "md5rr", 12, now.Unix(), 60)                   // flipped back

	sum, err := Compute(st, time.UTC)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	for i := range sum.Books {
		b := sum.Books[i]
		switch b.MD5 {
		case "md5omni":
			if want := float64(286) / 3695 * 100; b.Percent < want-0.1 || b.Percent > want+0.1 {
				t.Errorf("omnibus percent = %.2f, want ~%.2f (position 286)", b.Percent, want)
			}
			if b.Finished {
				t.Error("omnibus must not be finished from a peek short of the last page")
			}
			// Forecast counts from position, not the peek: ~3409 pages remain.
			if b.ForecastSecs <= 0 {
				t.Errorf("omnibus forecast_seconds = %d, want > 0", b.ForecastSecs)
			}
			wantSecs := int64(float64(3695-286) / b.PagesPerHour * 3600)
			if b.ForecastSecs != wantSecs {
				t.Errorf("omnibus forecast_seconds = %d, want %d", b.ForecastSecs, wantSecs)
			}
		case "md5rr":
			if !b.Finished || b.Percent != 100 {
				t.Errorf("reread: finished=%v percent=%.1f, want finished at 100", b.Finished, b.Percent)
			}
		}
	}
}
