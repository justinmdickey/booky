package stats

import (
	"time"

	"github.com/justindickey/booky/internal/store"
)

// x4FinishedPercent is where an X4 book counts as finished without the
// device's own "Mark Finished" flag. CrossInk reports 100% on the last page;
// the slack covers back matter the reader skips.
const x4FinishedPercent = 99.5

// mergeX4 folds uploaded X4 (CrossInk) books into the summary. X4 data is
// per-book running totals with no timestamps, so it feeds the book list and
// all-time totals but not the dated series (daily, heatmap, streaks, hourly).
// A book read on both devices (same file, same partial-MD5) becomes one row.
func (s *Summary) mergeX4(st *store.Store, loc *time.Location) error {
	books, err := st.X4Books()
	if err != nil {
		return err
	}
	if len(books) == 0 {
		return nil
	}
	syncs, err := st.LatestSyncs()
	if err != nil {
		return err
	}
	byMD5 := map[string]int{}
	for i, b := range s.Books {
		byMD5[b.MD5] = i
	}

	for _, x := range books {
		percent := 0.0
		if x.Percent != nil {
			percent = *x.Percent
		}
		lastOpen, source := x.UploadedAt, "upload"
		if sy, ok := syncs[x.MD5]; ok && sy.Timestamp > 0 {
			lastOpen, source = sy.Timestamp, "sync"
			// A sync after the last card upload is the fresher position.
			if sy.Timestamp > x.UploadedAt {
				percent = sy.Percent
			}
		}
		if percent > 100 {
			percent = 100
		}
		finished := x.Completed || percent >= x4FinishedPercent
		if finished {
			percent = 100
		}

		if i, ok := byMD5[x.MD5]; ok {
			b := &s.Books[i]
			b.Device = "kobo+x4"
			b.Seconds += x.Seconds
			b.PagesRead += x.PagesTurned
			if lastOpen > b.LastOpen {
				b.LastOpen, b.LastOpenSource = lastOpen, source
				b.Percent = percent
			}
			if !b.Excluded {
				s.TotalSeconds += x.Seconds
				s.TotalPages += x.PagesTurned
				if finished && !b.Finished {
					s.BooksFinished++
				}
			}
			if finished && !b.Finished {
				b.Finished = true
				b.FinishedAt = dateUnix(x.FinishedDate, loc)
				b.ForecastSecs = 0
			}
			continue
		}

		b := BookStat{
			MD5:            x.MD5,
			Title:          x.Title,
			Authors:        x.Authors,
			Series:         x.Series,
			Seconds:        x.Seconds,
			PagesRead:      x.PagesTurned,
			Percent:        percent,
			LastOpen:       lastOpen,
			LastOpenSource: source,
			FirstRead:      dateUnix(x.StartDate, loc),
			Finished:       finished,
			Excluded:       x.Excluded,
			Device:         "x4",
		}
		if finished {
			b.FinishedAt = dateUnix(x.FinishedDate, loc)
		} else {
			b.ForecastSecs = x.EstLeftSecs
		}
		if b.Seconds > 0 {
			b.PagesPerHour = float64(b.PagesRead) * 3600.0 / float64(b.Seconds)
		}
		if !b.Excluded {
			s.TotalSeconds += b.Seconds
			s.TotalPages += b.PagesRead
			s.BooksTracked++
			if b.Finished {
				s.BooksFinished++
			}
		}
		s.Books = append(s.Books, b)
	}
	return nil
}

// dateUnix turns a stored YYYY-MM-DD into local midnight, or 0 when empty.
func dateUnix(day string, loc *time.Location) int64 {
	if day == "" {
		return 0
	}
	t, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		return 0
	}
	return t.Unix()
}
