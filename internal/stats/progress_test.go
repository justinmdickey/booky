package stats

import (
	"errors"
	"testing"
	"time"
)

// TestProgress verifies Progress returns each day's closing position (the
// page of the day's LAST event, not its max) and correct per-day seconds —
// so a peek at the back of the book doesn't pin the curve to the top, and an
// honest flip-back shows as a dip.
func TestProgress(t *testing.T) {
	st := testStore(t)
	testBook(t, st, "md5prog", "Progress Book", 100)

	day1 := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2024, 1, 2, 10, 0, 0, 0, time.UTC)
	day3 := time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC)

	// Day 1: reads up to page 10, 120s total.
	testPage(t, st, "md5prog", 5, day1.Unix(), 60)
	testPage(t, st, "md5prog", 10, day1.Unix()+60, 60)
	// Day 2: flips back to page 3 — the day closes there, an honest dip.
	testPage(t, st, "md5prog", 3, day2.Unix(), 30)
	// Day 3: peeks at page 90 (the back of the book), then settles at 15 —
	// the closing position must be 15, not the peek.
	testPage(t, st, "md5prog", 90, day3.Unix(), 5)
	testPage(t, st, "md5prog", 15, day3.Unix()+60, 45)

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
		{Day: "2024-01-02", Page: 3, Seconds: 30}, // honest dip: day closed on page 3
		{Day: "2024-01-03", Page: 15, Seconds: 50}, // closing position, not the page-90 peek
	}
	for i, w := range want {
		got := bp.Points[i]
		if got != w {
			t.Errorf("point %d = %+v, want %+v", i, got, w)
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
