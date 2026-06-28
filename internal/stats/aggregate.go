package stats

import (
	"database/sql"
	"sort"
	"time"

	"github.com/justindickey/booky/internal/store"
)

// Summary is the top-level dashboard payload.
type Summary struct {
	TotalSeconds   int64       `json:"total_seconds"`
	TotalPages     int64       `json:"total_pages"`
	BooksTracked   int         `json:"books_tracked"`
	BooksFinished  int         `json:"books_finished"`
	CurrentStreak  int         `json:"current_streak"`
	LongestStreak  int         `json:"longest_streak"`
	DaysRead       int         `json:"days_read"`
	AvgPagesPerDay float64     `json:"avg_pages_per_day"`
	PagesPerHour   float64     `json:"pages_per_hour"`
	ThisWeekSecs   int64       `json:"this_week_seconds"`
	ThisYearSecs   int64       `json:"this_year_seconds"`
	Daily          []DayPoint  `json:"daily"`
	Heatmap        []DayPoint  `json:"heatmap"` // last 365 days
	Books          []BookStat  `json:"books"`
	RecentSessions []Session   `json:"recent_sessions"`
	Hourly         [24]int64   `json:"hourly"` // seconds read by hour-of-day
	Weekday        [7]int64    `json:"weekday"`
}

type DayPoint struct {
	Day     string `json:"day"` // YYYY-MM-DD
	Seconds int64  `json:"seconds"`
	Pages   int64  `json:"pages"`
}

type BookStat struct {
	MD5          string  `json:"md5"`
	Title        string  `json:"title"`
	Authors      string  `json:"authors"`
	Series       string  `json:"series"`
	Pages        int64   `json:"pages"`
	Seconds      int64   `json:"seconds"`
	PagesRead    int64   `json:"pages_read"`
	Percent      float64 `json:"percent"`
	PagesPerHour float64 `json:"pages_per_hour"`
	LastOpen     int64   `json:"last_open"`
	FirstRead    int64   `json:"first_read"`
	Highlights   int64   `json:"highlights"`
	CalibreID    int64   `json:"calibre_id"` // filled in by web layer if library present
	Finished     bool    `json:"finished"`
}

type Session struct {
	MD5     string `json:"md5"`
	Title   string `json:"title"`
	Started int64  `json:"started"`
	Seconds int64  `json:"seconds"`
	Pages   int64  `json:"pages"`
}

// Compute builds the full dashboard summary from Booky's ingested stats.
func Compute(st *store.Store, loc *time.Location) (Summary, error) {
	db := st.DB()
	var s Summary
	if loc == nil {
		loc = time.Local
	}

	// Per-book aggregates.
	rows, err := db.Query(`
SELECT b.md5, b.title, b.authors, b.series, b.pages, b.highlights, b.last_open,
       IFNULL(SUM(p.duration),0)        AS secs,
       COUNT(DISTINCT p.page)            AS pages_read,
       IFNULL(MAX(p.page),0)             AS max_page,
       IFNULL(MIN(p.start_time),0)       AS first_read
FROM book b LEFT JOIN page_stat p ON p.md5 = b.md5
GROUP BY b.md5
ORDER BY secs DESC`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var b BookStat
		var maxPage int64
		if err := rows.Scan(&b.MD5, &b.Title, &b.Authors, &b.Series, &b.Pages,
			&b.Highlights, &b.LastOpen, &b.Seconds, &b.PagesRead, &maxPage, &b.FirstRead); err != nil {
			return s, err
		}
		// "Read this far" is the furthest page reached, not the count of distinct
		// pages logged. When a book is re-paginated across file versions (a
		// metadata rewrite, a font/reflow change), the same content gets logged
		// under different page numbers, so DISTINCT undercounts. Reaching the
		// last page is the honest signal that the book was read through.
		reached := maxPage
		if b.PagesRead > reached {
			reached = b.PagesRead
		}
		if b.Pages > 0 {
			b.Percent = float64(reached) / float64(b.Pages) * 100
			if b.Percent > 100 {
				b.Percent = 100
			}
		}
		if b.Seconds > 0 {
			b.PagesPerHour = float64(b.PagesRead) * 3600.0 / float64(b.Seconds)
		}
		b.Finished = b.Pages > 0 && reached >= b.Pages-1 // allow off-by-one
		s.TotalSeconds += b.Seconds
		s.TotalPages += b.PagesRead
		s.BooksTracked++
		if b.Finished {
			s.BooksFinished++
		}
		s.Books = append(s.Books, b)
	}
	if err := rows.Err(); err != nil {
		return s, err
	}

	if s.TotalSeconds > 0 {
		s.PagesPerHour = float64(s.TotalPages) * 3600.0 / float64(s.TotalSeconds)
	}

	// Daily buckets (local time), hourly + weekday distributions.
	if err := s.computeDaily(db, loc); err != nil {
		return s, err
	}

	// Streaks from the set of active days.
	s.computeStreaks(loc)

	// Recent sessions (gap-split, raw page stats).
	if err := s.computeSessions(db); err != nil {
		return s, err
	}

	if s.DaysRead > 0 {
		s.AvgPagesPerDay = float64(s.TotalPages) / float64(s.DaysRead)
	}
	return s, nil
}

func (s *Summary) computeDaily(db *sql.DB, loc *time.Location) error {
	rows, err := db.Query(`SELECT page, start_time, duration FROM page_stat`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type agg struct {
		secs  int64
		pages map[int64]struct{}
	}
	days := map[string]*agg{}
	now := time.Now().In(loc)
	weekStart := now.AddDate(0, 0, -int(now.Weekday()))
	weekStart = time.Date(weekStart.Year(), weekStart.Month(), weekStart.Day(), 0, 0, 0, 0, loc)
	yearStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, loc)

	for rows.Next() {
		var page, start, dur int64
		if err := rows.Scan(&page, &start, &dur); err != nil {
			return err
		}
		t := time.Unix(start, 0).In(loc)
		key := t.Format("2006-01-02")
		a := days[key]
		if a == nil {
			a = &agg{pages: map[int64]struct{}{}}
			days[key] = a
		}
		a.secs += dur
		a.pages[page] = struct{}{}
		s.Hourly[t.Hour()] += dur
		s.Weekday[int(t.Weekday())] += dur
		if !t.Before(weekStart) {
			s.ThisWeekSecs += dur
		}
		if !t.Before(yearStart) {
			s.ThisYearSecs += dur
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	keys := make([]string, 0, len(days))
	for k := range days {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s.Daily = append(s.Daily, DayPoint{Day: k, Seconds: days[k].secs, Pages: int64(len(days[k].pages))})
	}
	s.DaysRead = len(days)

	// Heatmap: dense last-365-days series (zero-filled).
	cutoff := now.AddDate(0, 0, -364)
	for d := time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, loc); !d.After(now); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		var pt DayPoint
		pt.Day = k
		if a := days[k]; a != nil {
			pt.Seconds = a.secs
			pt.Pages = int64(len(a.pages))
		}
		s.Heatmap = append(s.Heatmap, pt)
	}
	return nil
}

func (s *Summary) computeStreaks(loc *time.Location) {
	if len(s.Daily) == 0 {
		return
	}
	dayset := map[string]bool{}
	var dates []time.Time
	for _, d := range s.Daily {
		dayset[d.Day] = true
		t, _ := time.ParseInLocation("2006-01-02", d.Day, loc)
		dates = append(dates, t)
	}
	sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })

	// Longest run of consecutive days.
	longest, run := 1, 1
	for i := 1; i < len(dates); i++ {
		if dates[i].Sub(dates[i-1]) == 24*time.Hour {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
	}
	s.LongestStreak = longest

	// Current streak: count back from today (or yesterday).
	today := time.Now().In(loc)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	cur := 0
	d := today
	if !dayset[d.Format("2006-01-02")] {
		d = d.AddDate(0, 0, -1) // grace: streak alive if read yesterday
	}
	for dayset[d.Format("2006-01-02")] {
		cur++
		d = d.AddDate(0, 0, -1)
	}
	s.CurrentStreak = cur
}

func (s *Summary) computeSessions(db *sql.DB) error {
	// Gap-split sessions per book: a >1h gap starts a new session.
	rows, err := db.Query(`
SELECT p.md5, IFNULL(b.title,''), p.start_time, p.duration, p.page
FROM page_stat p LEFT JOIN book b ON b.md5=p.md5
ORDER BY p.md5, p.start_time`)
	if err != nil {
		return err
	}
	defer rows.Close()

	const gap = int64(3600)
	var sessions []Session
	var cur *Session
	var lastMD5 string
	var lastStart int64
	seenPages := map[int64]struct{}{}

	flush := func() {
		if cur != nil {
			cur.Pages = int64(len(seenPages))
			sessions = append(sessions, *cur)
		}
	}
	for rows.Next() {
		var md5, title string
		var start, dur, page int64
		if err := rows.Scan(&md5, &title, &start, &dur, &page); err != nil {
			return err
		}
		newSession := cur == nil || md5 != lastMD5 || start-lastStart > gap
		if newSession {
			flush()
			cur = &Session{MD5: md5, Title: title, Started: start}
			seenPages = map[int64]struct{}{}
		}
		cur.Seconds += dur
		seenPages[page] = struct{}{}
		lastMD5 = md5
		lastStart = start
	}
	if err := rows.Err(); err != nil {
		return err
	}
	flush()

	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Started > sessions[j].Started })
	if len(sessions) > 30 {
		sessions = sessions[:30]
	}
	s.RecentSessions = sessions
	return nil
}
