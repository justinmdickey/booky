package store

import (
	"database/sql"
	"time"
)

// X4Book is one book's totals as uploaded from an Xteink X4 (CrossInk).
type X4Book struct {
	MD5          string   `json:"md5"`
	Title        string   `json:"title"`
	Authors      string   `json:"authors"`
	Series       string   `json:"series"`
	CardPath     string   `json:"card_path"`
	Sessions     int      `json:"sessions"`
	Seconds      int64    `json:"seconds"`
	PagesTurned  int64    `json:"pages_turned"`
	Completed    bool     `json:"completed"`
	Percent      *float64 `json:"percent,omitempty"`
	StartDate    string   `json:"start_date,omitempty"`
	FinishedDate string   `json:"finished_date,omitempty"`
	EstLeftSecs  int64    `json:"est_left_secs"`
	UploadedAt   int64    `json:"uploaded_at"`
	Excluded     bool     `json:"excluded"`
}

// UpsertX4Books stores an X4 upload. Books missing from the upload are left
// alone: an X4 upload is a partial view (only books with a cache folder on
// the card), unlike a KOReader statistics DB, so absence means nothing.
// Totals only move forward, so re-uploading an older card can't erase reading.
func (s *Store) UpsertX4Books(books []X4Book) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	up, err := tx.Prepare(`
INSERT INTO x4_book(md5,title,authors,series,card_path,sessions,seconds,pages_turned,completed,
                    percent,start_date,finished_date,est_left_secs,uploaded_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(md5) DO UPDATE SET
  title=excluded.title, authors=excluded.authors, series=excluded.series,
  card_path=excluded.card_path,
  sessions=MAX(x4_book.sessions,excluded.sessions),
  seconds=MAX(x4_book.seconds,excluded.seconds),
  pages_turned=MAX(x4_book.pages_turned,excluded.pages_turned),
  completed=excluded.completed,
  percent=COALESCE(excluded.percent,x4_book.percent),
  start_date=COALESCE(excluded.start_date,x4_book.start_date),
  finished_date=COALESCE(excluded.finished_date,x4_book.finished_date),
  est_left_secs=excluded.est_left_secs,
  uploaded_at=excluded.uploaded_at`)
	if err != nil {
		return 0, err
	}
	defer up.Close()
	now := time.Now().Unix()
	n := 0
	for _, b := range books {
		if b.MD5 == "" {
			continue
		}
		if _, err := up.Exec(b.MD5, b.Title, b.Authors, b.Series, b.CardPath, b.Sessions, b.Seconds,
			b.PagesTurned, b.Completed, b.Percent, nullIfEmpty(b.StartDate), nullIfEmpty(b.FinishedDate),
			b.EstLeftSecs, now); err != nil {
			return 0, err
		}
		n++
	}
	return n, tx.Commit()
}

// X4Books returns every stored X4 book.
func (s *Store) X4Books() ([]X4Book, error) {
	rows, err := s.db.Query(`
SELECT md5, IFNULL(title,''), IFNULL(authors,''), IFNULL(series,''), IFNULL(card_path,''),
       sessions, seconds, pages_turned, completed, percent,
       IFNULL(start_date,''), IFNULL(finished_date,''), est_left_secs, uploaded_at, excluded
FROM x4_book`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []X4Book
	for rows.Next() {
		var b X4Book
		var pct sql.NullFloat64
		if err := rows.Scan(&b.MD5, &b.Title, &b.Authors, &b.Series, &b.CardPath, &b.Sessions,
			&b.Seconds, &b.PagesTurned, &b.Completed, &pct, &b.StartDate, &b.FinishedDate,
			&b.EstLeftSecs, &b.UploadedAt, &b.Excluded); err != nil {
			return nil, err
		}
		if pct.Valid {
			p := pct.Float64
			b.Percent = &p
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LatestSyncs returns, per kosync document, the newest sync time and the
// percent (0-100) it reported, across all kosync users.
func (s *Store) LatestSyncs() (map[string]Sync, error) {
	rows, err := s.db.Query(`SELECT document, IFNULL(percentage,0), timestamp, IFNULL(device,'') FROM progress`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Sync{}
	for rows.Next() {
		var doc, device string
		var pct float64
		var ts int64
		if err := rows.Scan(&doc, &pct, &ts, &device); err != nil {
			return nil, err
		}
		// KOReader-protocol clients send a 0-1 fraction.
		if pct <= 1 {
			pct *= 100
		}
		if cur, ok := out[doc]; !ok || ts > cur.Timestamp {
			out[doc] = Sync{Percent: pct, Timestamp: ts, Device: device}
		}
	}
	return out, rows.Err()
}

// Sync is the most recent kosync position for one document.
type Sync struct {
	Percent   float64
	Timestamp int64
	Device    string
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
