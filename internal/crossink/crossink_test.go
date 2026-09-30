package crossink

import (
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The hash must match the folder names CrossInk actually created on a real
// card, or uploads silently miss every book.
func TestCacheDirNameMatchesDevice(t *testing.T) {
	cases := map[string]string{
		"/Books/Christopher Buehlman - Between Two Fires.epub":           "epub_17609771156843734274",
		"/Books/J.K. Rowling - Harry Potter and the Goblet of Fire.epub": "epub_5287752927549572509",
	}
	for p, want := range cases {
		if got := CacheDirName(p); got != want {
			t.Errorf("CacheDirName(%q) = %s, want %s", p, got, want)
		}
	}
}

// Fixtures are real stats files pulled off an X4 running CrossInk 1.5.
func TestParseStatsV5(t *testing.T) {
	s, err := ParseStats(readFixture(t, "restaurant_stats_v5.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != 5 || s.Sessions != 19 || s.Seconds != 14155 || s.PagesTurned != 610 || s.Completed {
		t.Errorf("restaurant: got %+v", s)
	}
	if s.StartDate.Valid() || s.FinishedDate.Valid() {
		t.Errorf("clock was never set on this device, dates should be empty: %+v", s)
	}

	s, err = ParseStats(readFixture(t, "mostly_harmless_stats_v5.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Sessions != 19 || s.Seconds != 15943 || s.PagesTurned != 541 || !s.Completed {
		t.Errorf("mostly harmless: got %+v", s)
	}
}

func TestParseStatsDates(t *testing.T) {
	b := make([]byte, 73)
	b[0] = 5
	b[17], b[18], b[19], b[20] = 0xEA, 0x07, 9, 3 // 2026-09-03
	b[21], b[22], b[23], b[24] = 0xEA, 0x07, 9, 28
	s, err := ParseStats(b)
	if err != nil {
		t.Fatal(err)
	}
	if s.StartDate.String() != "2026-09-03" || s.FinishedDate.String() != "2026-09-28" {
		t.Errorf("got start %q finish %q", s.StartDate, s.FinishedDate)
	}
}

func TestParseStatsRejectsBadSize(t *testing.T) {
	for _, b := range [][]byte{nil, {5, 1, 2}, append([]byte{5}, make([]byte, 60)...), append([]byte{9}, make([]byte, 72)...)} {
		if _, err := ParseStats(b); err == nil {
			t.Errorf("expected error for % x", b)
		}
	}
}

func TestParseProgressPercent(t *testing.T) {
	p, err := ParseProgressPercent(readFixture(t, "restaurant_progress_percent.bin"))
	if err != nil || p != 100 {
		t.Errorf("restaurant: %v %v", p, err)
	}
	p, err = ParseProgressPercent(readFixture(t, "between_two_fires_progress_percent.bin"))
	if err != nil || p != 10.66 {
		t.Errorf("between two fires: %v %v", p, err)
	}
	if _, err := ParseProgressPercent([]byte("nope")); err == nil {
		t.Error("expected error for junk")
	}
}

func TestReadBookState(t *testing.T) {
	root := t.TempDir()
	cardPath := "/Books/Douglas Adams/Hitchhiker 02 - The Restaurant at the End of the Universe.epub"
	dir := filepath.Join(root, ".crosspoint", CacheDirName(cardPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "stats_v5.bin"), readFixture(t, "restaurant_stats_v5.bin"), 0o644)
	os.WriteFile(filepath.Join(dir, "progress_percent.bin"), readFixture(t, "restaurant_progress_percent.bin"), 0o644)

	st, found, err := ReadBookState(root, cardPath)
	if err != nil || !found || !st.HasStats || !st.HasPercent || st.Stats.PagesTurned != 610 || st.Percent != 100 {
		t.Errorf("got %+v found=%v err=%v", st, found, err)
	}

	_, found, err = ReadBookState(root, "/Books/Never Opened.epub")
	if err != nil || found {
		t.Errorf("unopened book: found=%v err=%v", found, err)
	}
}
