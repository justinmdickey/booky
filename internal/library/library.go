// Package library reads a Calibre metadata.db read-only to enrich Booky's
// dashboard and power the OPDS feed: covers, authors, series, formats, and the
// on-disk file path for each book (joined to KOReader stats via partial-MD5).
package library

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/justindickey/booky/internal/koreader"
	_ "modernc.org/sqlite"
)

type Library struct {
	root string
	db   *sql.DB

	mu       sync.RWMutex
	md5Cache map[string]int64 // partial-md5 -> calibre book id
	cachedAt time.Time
}

type Book struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Authors     string    `json:"authors"`
	Series      string    `json:"series"`
	SeriesIndex float64   `json:"series_index"`
	Tags        string    `json:"tags"`
	Rating      int       `json:"rating"` // 0..5 stars
	Language    string    `json:"language"`
	Comment     string    `json:"comment"`
	HasCover    bool      `json:"has_cover"`
	Path        string    `json:"-"` // relative folder under library root
	Added       time.Time `json:"added"`
	Formats     []Format  `json:"formats"`
}

type Format struct {
	Format string `json:"format"` // EPUB, PDF, ...
	Name   string `json:"name"`   // base filename, no extension
	Size   int64  `json:"size"`
}

// Open opens the Calibre metadata.db read-only. root is the library directory.
func Open(root string) (*Library, error) {
	if root == "" {
		return nil, nil
	}
	dbPath := filepath.Join(root, "metadata.db")
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("open calibre metadata.db at %s: %w", dbPath, err)
	}
	return &Library{root: root, db: db, md5Cache: map[string]int64{}}, nil
}

func (l *Library) Root() string { return l.root }

// scanBooks loads the full library. Used both for listing and for building the
// md5 index. We keep the SQL in one place.
func (l *Library) listBooks(where string, args ...any) ([]Book, error) {
	q := `
SELECT b.id, b.title,
       IFNULL((SELECT GROUP_CONCAT(a.name, ' & ') FROM books_authors_link bal
               JOIN authors a ON a.id = bal.author WHERE bal.book = b.id), '') AS authors,
       IFNULL((SELECT s.name FROM books_series_link bsl JOIN series s ON s.id=bsl.series
               WHERE bsl.book=b.id LIMIT 1), '') AS series,
       IFNULL(b.series_index, 0),
       IFNULL((SELECT GROUP_CONCAT(t.name, ', ') FROM books_tags_link btl
               JOIN tags t ON t.id=btl.tag WHERE btl.book=b.id), '') AS tags,
       IFNULL((SELECT r.rating FROM books_ratings_link brl JOIN ratings r ON r.id=brl.rating
               WHERE brl.book=b.id LIMIT 1), 0) AS rating,
       IFNULL((SELECT l2.lang_code FROM books_languages_link bll JOIN languages l2 ON l2.id=bll.lang_code
               WHERE bll.book=b.id LIMIT 1), '') AS language,
       IFNULL((SELECT c.text FROM comments c WHERE c.book=b.id LIMIT 1), '') AS comment,
       b.has_cover, b.path, b.timestamp
FROM books b `
	if where != "" {
		q += "WHERE " + where + " "
	}
	q += "ORDER BY b.timestamp DESC"

	rows, err := l.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Book
	for rows.Next() {
		var b Book
		var hasCover int
		var ts string
		if err := rows.Scan(&b.ID, &b.Title, &b.Authors, &b.Series, &b.SeriesIndex,
			&b.Tags, &b.Rating, &b.Language, &b.Comment, &hasCover, &b.Path, &ts); err != nil {
			return nil, err
		}
		b.HasCover = hasCover != 0
		b.Rating = b.Rating / 2 // Calibre stores 0..10
		b.Added = parseCalibreTime(ts)
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Attach formats.
	for i := range out {
		out[i].Formats = l.formats(out[i].ID)
	}
	return out, nil
}

func (l *Library) formats(bookID int64) []Format {
	rows, err := l.db.Query(`SELECT format, name, IFNULL(uncompressed_size,0) FROM data WHERE book=?`, bookID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var fs []Format
	for rows.Next() {
		var f Format
		if err := rows.Scan(&f.Format, &f.Name, &f.Size); err == nil {
			fs = append(fs, f)
		}
	}
	return fs
}

// All returns every book in the library.
func (l *Library) All() ([]Book, error) { return l.listBooks("") }

// ByIDs returns books for the given Calibre ids, preserving no particular order.
func (l *Library) ByIDs(ids []int64) ([]Book, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return l.listBooks("b.id IN ("+ph+")", args...)
}

// One returns a single book by Calibre id.
func (l *Library) One(id int64) (Book, bool) {
	bs, err := l.listBooks("b.id=?", id)
	if err != nil || len(bs) == 0 {
		return Book{}, false
	}
	return bs[0], true
}

// CoverPath returns the absolute path to a book's cover.jpg, if present.
func (l *Library) CoverPath(b Book) (string, bool) {
	if !b.HasCover {
		return "", false
	}
	return filepath.Join(l.root, b.Path, "cover.jpg"), true
}

// FilePath returns the absolute path to a book's file in the given format.
func (l *Library) FilePath(b Book, format string) (string, bool) {
	for _, f := range b.Formats {
		if strings.EqualFold(f.Format, format) {
			return filepath.Join(l.root, b.Path, f.Name+"."+strings.ToLower(f.Format)), true
		}
	}
	return "", false
}

// BestFormat picks the most reader-friendly format available (EPUB > KEPUB >
// AZW3 > MOBI > PDF > anything).
func BestFormat(b Book) (Format, bool) {
	pref := []string{"EPUB", "KEPUB", "AZW3", "MOBI", "PDF", "CBZ", "FB2"}
	for _, p := range pref {
		for _, f := range b.Formats {
			if strings.EqualFold(f.Format, p) {
				return f, true
			}
		}
	}
	if len(b.Formats) > 0 {
		return b.Formats[0], true
	}
	return Format{}, false
}

// CalibreIDForMD5 resolves a KOReader partial-MD5 to a Calibre book id by
// computing the partial-MD5 of each book's primary file. Results are cached;
// the index is rebuilt lazily at most every few minutes.
func (l *Library) CalibreIDForMD5(md5hex string) (int64, bool) {
	l.mu.RLock()
	if id, ok := l.md5Cache[md5hex]; ok {
		l.mu.RUnlock()
		return id, true
	}
	fresh := time.Since(l.cachedAt) < 5*time.Minute && !l.cachedAt.IsZero()
	l.mu.RUnlock()
	if fresh {
		return 0, false
	}
	l.buildMD5Index()
	l.mu.RLock()
	defer l.mu.RUnlock()
	id, ok := l.md5Cache[md5hex]
	return id, ok
}

// MD5ForBook computes the KOReader partial-MD5 of a book's best-format file —
// the same content fingerprint used everywhere else, so clients can dedupe by
// content regardless of filename. Returns "" if no file is available.
func (l *Library) MD5ForBook(b Book) string {
	f, ok := BestFormat(b)
	if !ok {
		return ""
	}
	path := filepath.Join(l.root, b.Path, f.Name+"."+strings.ToLower(f.Format))
	if h, err := koreader.PartialMD5File(path); err == nil {
		return h
	}
	return ""
}

func (l *Library) buildMD5Index() {
	books, err := l.All()
	if err != nil {
		return
	}
	idx := make(map[string]int64, len(books))
	for _, b := range books {
		f, ok := BestFormat(b)
		if !ok {
			continue
		}
		path := filepath.Join(l.root, b.Path, f.Name+"."+strings.ToLower(f.Format))
		if h, err := koreader.PartialMD5File(path); err == nil {
			idx[h] = b.ID
		}
	}
	l.mu.Lock()
	l.md5Cache = idx
	l.cachedAt = time.Now()
	l.mu.Unlock()
}

// parseCalibreTime parses Calibre's ISO-ish timestamp strings.
func parseCalibreTime(s string) time.Time {
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999-07:00",
		"2006-01-02 15:04:05.999999+00:00",
		"2006-01-02 15:04:05",
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
