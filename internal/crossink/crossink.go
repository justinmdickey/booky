// Package crossink reads the per-book state that the CrossInk e-reader
// firmware (Xteink X3/X4) keeps on its SD card, so an X4's reading can be
// uploaded to Booky. CrossInk has no statistics.sqlite3: each opened book gets
// a cache folder `.crosspoint/epub_<hash>` holding small binary files, and the
// ones that matter here are the reading stats and the progress percent.
//
// The formats mirror CrossInk's BookReadingStats.cpp and
// RecentBookProgress.cpp (github.com/uxjulia/CrossInk).
package crossink

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CacheDirName returns the cache folder name CrossInk uses for a book at
// cardPath (the absolute path on the card, e.g. "/Books/Fiction/Dune.epub"):
// "epub_" + the decimal FNV-1a 64-bit hash of the path bytes.
func CacheDirName(cardPath string) string {
	h := uint64(14695981039346656037)
	for i := 0; i < len(cardPath); i++ {
		h ^= uint64(cardPath[i])
		h *= 1099511628211
	}
	return "epub_" + strconv.FormatUint(h, 10)
}

// Date is a calendar day. The zero value means "not recorded" — CrossInk only
// records dates once the device clock has been set.
type Date struct {
	Year  int
	Month int
	Day   int
}

func (d Date) Valid() bool {
	return d.Year > 0 && d.Month >= 1 && d.Month <= 12 && d.Day >= 1 && d.Day <= 31
}

// String formats a valid date as YYYY-MM-DD and returns "" otherwise.
func (d Date) String() string {
	if !d.Valid() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

// Unix returns local midnight of the date, or 0 if the date is not valid.
func (d Date) Unix(loc *time.Location) int64 {
	if !d.Valid() {
		return 0
	}
	return time.Date(d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, loc).Unix()
}

// Stats is one book's reading stats as CrossInk records them. Every field is
// a running total; CrossInk keeps no per-session history.
type Stats struct {
	Version          int
	Sessions         int
	Seconds          int64
	PagesTurned      int64
	Completed        bool
	StartDate        Date
	FinishedDate     Date
	EstimatedSecLeft int64 // 0 = unavailable
}

// ErrUnknownFormat is returned for stats files whose version/size pair
// CrossInk itself would reject.
var ErrUnknownFormat = errors.New("crossink: unknown stats file format")

// ParseStats decodes a stats_v*.bin file (versions 1–5).
func ParseStats(b []byte) (Stats, error) {
	var s Stats
	if len(b) < 11 {
		return s, ErrUnknownFormat
	}
	s.Version = int(b[0])
	want := map[int]int{1: 11, 2: 12, 3: 16, 4: 69, 5: 73}[s.Version]
	if want == 0 || len(b) != want {
		return s, ErrUnknownFormat
	}
	le := binary.LittleEndian
	s.Sessions = int(le.Uint16(b[1:]))
	s.Seconds = int64(le.Uint32(b[3:]))
	s.PagesTurned = int64(le.Uint32(b[7:]))
	if s.Version >= 2 {
		s.Completed = b[11] != 0
	}
	if s.Version >= 4 {
		s.StartDate = Date{int(le.Uint16(b[17:])), int(b[19]), int(b[20])}
		s.FinishedDate = Date{int(le.Uint16(b[21:])), int(b[23]), int(b[24])}
	}
	if s.Version >= 5 {
		s.EstimatedSecLeft = int64(le.Uint32(b[69:]))
	}
	return s, nil
}

const percentMagic = 0x45505250 // "EPRP", stored little-endian

// ParseProgressPercent decodes progress_percent.bin and returns the percent
// read (0–100).
func ParseProgressPercent(b []byte) (float64, error) {
	if len(b) != 7 || binary.LittleEndian.Uint32(b) != percentMagic || b[4] != 1 {
		return 0, ErrUnknownFormat
	}
	bp := binary.LittleEndian.Uint16(b[5:])
	if bp > 10000 {
		return 0, ErrUnknownFormat
	}
	return float64(bp) / 100, nil
}

// BookState is everything read from one book's cache folder.
type BookState struct {
	Stats      Stats
	HasStats   bool
	Percent    float64
	HasPercent bool
}

// ReadBookState loads the stats and percent files for the book at cardPath on
// a card mounted at cardRoot. A book that was never opened has no cache folder
// and returns found=false.
func ReadBookState(cardRoot, cardPath string) (st BookState, found bool, err error) {
	dir := filepath.Join(cardRoot, ".crosspoint", CacheDirName(cardPath))
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return st, false, nil
		}
		return st, false, err
	}
	if f := newestStatsFile(dir); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return st, true, err
		}
		if st.Stats, err = ParseStats(b); err != nil {
			return st, true, fmt.Errorf("%s: %w", f, err)
		}
		st.HasStats = true
	}
	if b, err := os.ReadFile(filepath.Join(dir, "progress_percent.bin")); err == nil {
		if p, err := ParseProgressPercent(b); err == nil {
			st.Percent, st.HasPercent = p, true
		}
	}
	return st, true, nil
}

// newestStatsFile picks the highest-versioned stats*.bin in dir, matching how
// CrossInk migrates older files forward.
func newestStatsFile(dir string) string {
	m, _ := filepath.Glob(filepath.Join(dir, "stats*.bin"))
	sort.Slice(m, func(i, j int) bool { return statsVersion(m[i]) > statsVersion(m[j]) })
	if len(m) == 0 {
		return ""
	}
	return m[0]
}

func statsVersion(p string) int {
	base := strings.TrimSuffix(path.Base(filepath.ToSlash(p)), ".bin")
	v, _ := strconv.Atoi(strings.TrimPrefix(base, "stats_v"))
	return v
}
