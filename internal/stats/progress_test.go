package stats

import (
	"errors"
	"testing"
	"time"
)

// TestProgress verifies Progress returns ascending, monotonic cumulative
// pages and correct per-day seconds, including a day where the max page read
// dips below a previous day's (the running total must not drop).
func TestProgress(t *testing.T) {
	st := testStore(t)
	testBook(t, st, "md5prog", "Progress Book", 100)

	day1 := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)
	day3 := time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC)

	// Day 1: reads up to page 10, 120s total.
	testPage(t, st, "md5prog", 5, day1.Unix(), 60)
	testPage(t, st, "md5prog", 10, day1.Unix()+60, 60)
	// Day 2: re-reads page 3 (e.g. flipped back) — max page for the day is
	// lower than day 1's cumulative high, so the running total must hold.
	testPage(t, st, "md5prog", 3, day2.Unix(), 30)
	// Day 3: advances past day 1's high point.
	testPage(t, st, "md5prog", 15, day3.Unix(), 45)

	bp, err := Progress(st, "md5prog", time.UTC)
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if bp.MD5 != "md5prog" || bp.Title != "Progress Book" || bp.Pages != 100 {
		t.Errorf("unexpected book header: %+v", bp)
	}
	if len(bp.Points) != 3 {
		t.Fatalf("expected 3 points, got %d: %+v", len(bp.Points), bp.Points)
	}

	want := []ProgressPoint{
		{Day: "2024-01-01", Page: 10, Seconds: 120},
		{Day: "2024-01-02", Page: 10, Seconds: 30}, // running max holds, doesn't drop to 3
		{Day: "2024-01-03", Page: 15, Seconds: 45},
	}
	for i, w := range want {
		got := bp.Points[i]
		if got != w {
			t.Errorf("point %d = %+v, want %+v", i, got, w)
		}
	}
	// Monotonic non-decreasing across the whole series.
	for i := 1; i < len(bp.Points); i++ {
		if bp.Points[i].Page < bp.Points[i-1].Page {
			t.Errorf("page dropped at index %d: %d -> %d", i, bp.Points[i-1].Page, bp.Points[i].Page)
		}
	}
}

// TestProgressUnknownBook verifies an unknown md5 returns ErrNotFound so the
// web layer can translate it to a 404.
func TestProgressUnknownBook(t *testing.T) {
	st := testStore(t)
	_, err := Progress(st, "nosuchmd5", time.UTC)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
