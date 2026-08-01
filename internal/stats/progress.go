package stats

import (
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/justindickey/booky/internal/store"
)

// ErrNotFound is returned by Progress when no book row matches the given md5.
var ErrNotFound = errors.New("book not found")

// BookProgress is the per-day reading history for a single book, used by the
// book detail view.
type BookProgress struct {
	MD5    string          `json:"md5"`
	Title  string          `json:"title"`
	Pages  int64           `json:"pages"`
	Points []ProgressPoint `json:"points"`
}

// ProgressPoint is one day of activity on a book. Page is a running
// (monotonic, non-decreasing) furthest-page-reached value, not just that
// day's max — mirrors the "furthest page reached" logic in Compute.
type ProgressPoint struct {
	Day     string `json:"day"`     // YYYY-MM-DD local
	Page    int64  `json:"page"`    // cumulative furthest page up to and including this day
	Seconds int64  `json:"seconds"` // seconds read that day
}

// Progress returns day-by-day reading progress for a single book, ascending,
// with one point per day that has activity. Returns ErrNotFound if md5
// doesn't match a known book.
func Progress(st *store.Store, md5 string, loc *time.Location) (BookProgress, error) {
	var bp BookProgress
	if loc == nil {
		loc = time.Local
	}
	db := st.DB()
	err := db.QueryRow(`SELECT md5, title, pages FROM book WHERE md5=?`, md5).
		Scan(&bp.MD5, &bp.Title, &bp.Pages)
	if err == sql.ErrNoRows {
		return bp, ErrNotFound
	}
	if err != nil {
		return bp, err
	}

	rows, err := db.Query(`SELECT page, start_time, duration FROM page_stat WHERE md5=?`, md5)
	if err != nil {
		return bp, err
	}
	defer rows.Close()

	type agg struct {
		secs    int64
		maxPage int64
	}
	days := map[string]*agg{}
	for rows.Next() {
		var page, start, dur int64
		if err := rows.Scan(&page, &start, &dur); err != nil {
			return bp, err
		}
		key := time.Unix(start, 0).In(loc).Format("2006-01-02")
		a := days[key]
		if a == nil {
			a = &agg{}
			days[key] = a
		}
		a.secs += dur
		if page > a.maxPage {
			a.maxPage = page
		}
	}
	if err := rows.Err(); err != nil {
		return bp, err
	}

	keys := make([]string, 0, len(days))
	for k := range days {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var running int64
	for _, k := range keys {
		a := days[k]
		if a.maxPage > running {
			running = a.maxPage
		}
		bp.Points = append(bp.Points, ProgressPoint{Day: k, Page: running, Seconds: a.secs})
	}
	return bp, nil
}
