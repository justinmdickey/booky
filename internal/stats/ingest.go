// Package stats ingests KOReader's statistics.sqlite3 and computes the
// aggregates that power Booky's reading dashboard.
package stats

import (
	"database/sql"
	"fmt"

	"github.com/justindickey/booky/internal/store"
	_ "modernc.org/sqlite"
)

// Ingest reads an uploaded KOReader statistics.sqlite3 (at srcPath, opened
// read-only and immutable) and merges its book + page-stat rows into Booky's
// store, keyed by the partial-MD5 fingerprint. Returns counts for feedback.
func Ingest(st *store.Store, srcPath string) (books int, pages int, err error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&immutable=1", srcPath)
	src, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, 0, err
	}
	defer src.Close()
	if err := src.Ping(); err != nil {
		return 0, 0, fmt.Errorf("open uploaded statistics db: %w", err)
	}

	usesData, err := detectPageStatData(src)
	if err != nil {
		return 0, 0, err
	}

	// Map of source book id -> md5, built while importing books.
	idToMD5 := map[int64]string{}

	tx, err := st.DB().Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	bookRows, err := src.Query(`
SELECT id, IFNULL(title,''), IFNULL(authors,''), IFNULL(series,''), IFNULL(language,''),
       IFNULL(pages,0), IFNULL(last_open,0), IFNULL(highlights,0), IFNULL(notes,0),
       IFNULL(total_read_time,0), IFNULL(total_read_pages,0), IFNULL(md5,'')
FROM book`)
	if err != nil {
		return 0, 0, err
	}
	defer bookRows.Close()

	upBook, err := tx.Prepare(`
INSERT INTO book(md5,title,authors,series,language,pages,last_open,highlights,notes,total_read_time,total_read_pages)
VALUES(?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(md5) DO UPDATE SET
  title=excluded.title, authors=excluded.authors, series=excluded.series,
  language=excluded.language, pages=MAX(book.pages,excluded.pages),
  last_open=MAX(book.last_open,excluded.last_open),
  highlights=MAX(book.highlights,excluded.highlights),
  notes=MAX(book.notes,excluded.notes),
  total_read_time=MAX(book.total_read_time,excluded.total_read_time),
  total_read_pages=MAX(book.total_read_pages,excluded.total_read_pages)`)
	if err != nil {
		return 0, 0, err
	}
	defer upBook.Close()

	for bookRows.Next() {
		var id, npages, lastOpen, hl, notes, trt, trp int64
		var title, authors, series, lang, md5 string
		if err := bookRows.Scan(&id, &title, &authors, &series, &lang, &npages,
			&lastOpen, &hl, &notes, &trt, &trp, &md5); err != nil {
			return 0, 0, err
		}
		if md5 == "" {
			continue // can't key it; skip
		}
		idToMD5[id] = md5
		if _, err := upBook.Exec(md5, title, authors, series, lang, npages,
			lastOpen, hl, notes, trt, trp); err != nil {
			return 0, 0, err
		}
		books++
	}
	if err := bookRows.Err(); err != nil {
		return 0, 0, err
	}

	// Page stats. Use page_stat_data (raw, session-accurate) when present,
	// otherwise the old page_stat table (period -> duration).
	var pageQuery string
	if usesData {
		pageQuery = `SELECT id_book, page, start_time, duration, IFNULL(total_pages,0) FROM page_stat_data`
	} else {
		pageQuery = `SELECT id_book, page, start_time, period AS duration, 0 FROM page_stat`
	}
	pageRows, err := src.Query(pageQuery)
	if err != nil {
		return 0, 0, err
	}
	defer pageRows.Close()

	upPage, err := tx.Prepare(`
INSERT INTO page_stat(md5,page,start_time,duration,total_pages)
VALUES(?,?,?,?,?)
ON CONFLICT(md5,page,start_time) DO UPDATE SET
  duration=MAX(page_stat.duration,excluded.duration),
  total_pages=MAX(page_stat.total_pages,excluded.total_pages)`)
	if err != nil {
		return 0, 0, err
	}
	defer upPage.Close()

	for pageRows.Next() {
		var idBook, page, startTime, duration, totalPages int64
		if err := pageRows.Scan(&idBook, &page, &startTime, &duration, &totalPages); err != nil {
			return 0, 0, err
		}
		md5, ok := idToMD5[idBook]
		if !ok {
			continue
		}
		if _, err := upPage.Exec(md5, page, startTime, duration, totalPages); err != nil {
			return 0, 0, err
		}
		pages++
	}
	if err := pageRows.Err(); err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return books, pages, nil
}

// detectPageStatData reports whether the source DB uses the modern
// page_stat_data table (vs the legacy page_stat table).
func detectPageStatData(src *sql.DB) (bool, error) {
	var name string
	err := src.QueryRow(`SELECT name FROM sqlite_master WHERE name='page_stat_data'`).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return name == "page_stat_data", nil
}
