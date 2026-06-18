// Package opds serves a curated OPDS 1.2 catalog that KOReader (and any OPDS
// client) can browse to pull books from the Calibre library. Curation lives in
// Booky's collections; the actual files and covers are read from the library.
package opds

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/justindickey/booky/internal/library"
	"github.com/justindickey/booky/internal/store"
)

const (
	navType  = "application/atom+xml;profile=opds-catalog;kind=navigation"
	acqType  = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	relAcq   = "http://opds-spec.org/acquisition"
	relImage = "http://opds-spec.org/image"
	relThumb = "http://opds-spec.org/image/thumbnail"
)

type Server struct {
	lib   *library.Library
	st    *store.Store
	base  string // public base URL, may be empty (then derived from request)
}

func New(lib *library.Library, st *store.Store, base string) *Server {
	return &Server{lib: lib, st: st, base: strings.TrimRight(base, "/")}
}

func (s *Server) Register(mux *http.ServeMux) {
	if s.lib == nil {
		return
	}
	mux.HandleFunc("GET /opds", s.root)
	mux.HandleFunc("GET /opds/", s.root)
	mux.HandleFunc("GET /opds/all", s.all)
	mux.HandleFunc("GET /opds/recent", s.recent)
	mux.HandleFunc("GET /opds/collections", s.collections)
	mux.HandleFunc("GET /opds/collection/{id}", s.collection)
	mux.HandleFunc("GET /opds/download/{id}/{format}", s.download)
	mux.HandleFunc("GET /opds/cover/{id}", s.cover)
}

// ---- Atom/OPDS XML model ----

type feed struct {
	XMLName xml.Name `xml:"feed"`
	XMLNS   string   `xml:"xmlns,attr"`
	XMLNSOp string   `xml:"xmlns:opds,attr"`
	ID      string   `xml:"id"`
	Title   string   `xml:"title"`
	Updated string   `xml:"updated"`
	Links   []link   `xml:"link"`
	Entries []entry  `xml:"entry"`
}

type link struct {
	Rel   string `xml:"rel,attr"`
	Href  string `xml:"href,attr"`
	Type  string `xml:"type,attr"`
	Title string `xml:"title,attr,omitempty"`
}

type entry struct {
	Title   string  `xml:"title"`
	ID      string  `xml:"id"`
	Updated string  `xml:"updated"`
	Author  *author `xml:"author,omitempty"`
	Content *content `xml:"content,omitempty"`
	Links   []link  `xml:"link"`
}

type author struct {
	Name string `xml:"name"`
}

type content struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

func (s *Server) baseURL(r *http.Request) string {
	if s.base != "" {
		return s.base
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func writeFeed(w http.ResponseWriter, kind string, f feed) {
	f.XMLNS = "http://www.w3.org/2005/Atom"
	f.XMLNSOp = "http://opds-spec.org/2010/catalog"
	if f.Updated == "" {
		f.Updated = time.Now().UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", kind)
	w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(f)
}

func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	b := s.baseURL(r)
	f := feed{
		ID:      b + "/opds",
		Title:   "📚 Booky",
		Updated: time.Now().UTC().Format(time.RFC3339),
		Links: []link{
			{Rel: "self", Href: b + "/opds", Type: navType},
			{Rel: "start", Href: b + "/opds", Type: navType},
		},
		Entries: []entry{
			navEntry("On Deck — curated collections", b+"/opds/collections", "Books you've lined up to read", b),
			navEntry("Recently Added", b+"/opds/recent", "The latest additions to the library", b),
			navEntry("All Books", b+"/opds/all", "Browse the entire library", b),
		},
	}
	writeFeed(w, navType, f)
}

func navEntry(title, href, desc, base string) entry {
	return entry{
		Title:   title,
		ID:      href,
		Updated: time.Now().UTC().Format(time.RFC3339),
		Content: &content{Type: "text", Body: desc},
		Links:   []link{{Rel: "subsection", Href: href, Type: navType}},
	}
}

func (s *Server) collections(w http.ResponseWriter, r *http.Request) {
	b := s.baseURL(r)
	cols, _ := s.st.Collections()
	f := feed{
		ID:    b + "/opds/collections",
		Title: "On Deck",
		Links: []link{
			{Rel: "self", Href: b + "/opds/collections", Type: navType},
			{Rel: "start", Href: b + "/opds", Type: navType},
			{Rel: "up", Href: b + "/opds", Type: navType},
		},
	}
	for _, c := range cols {
		icon := c.Icon
		if icon == "" {
			icon = "📖"
		}
		href := fmt.Sprintf("%s/opds/collection/%d", b, c.ID)
		f.Entries = append(f.Entries, entry{
			Title:   fmt.Sprintf("%s %s (%d)", icon, c.Name, c.Count),
			ID:      href,
			Updated: time.Now().UTC().Format(time.RFC3339),
			Links:   []link{{Rel: "subsection", Href: href, Type: acqType}},
		})
	}
	if len(cols) == 0 {
		f.Entries = append(f.Entries, entry{
			Title:   "No collections yet — add some in the Booky web UI",
			ID:      b + "/opds/collections#empty",
			Updated: time.Now().UTC().Format(time.RFC3339),
			Content: &content{Type: "text", Body: "Open Booky and curate your On Deck list."},
		})
	}
	writeFeed(w, navType, f)
}

func (s *Server) collection(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ids, name, err := s.st.CollectionBooks(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	books, _ := s.lib.ByIDs(ids)
	s.writeAcquisition(w, r, name, fmt.Sprintf("/opds/collection/%d", id), books)
}

func (s *Server) all(w http.ResponseWriter, r *http.Request) {
	books, _ := s.lib.All()
	s.writeAcquisition(w, r, "All Books", "/opds/all", books)
}

func (s *Server) recent(w http.ResponseWriter, r *http.Request) {
	books, _ := s.lib.All() // already sorted by added desc
	if len(books) > 50 {
		books = books[:50]
	}
	s.writeAcquisition(w, r, "Recently Added", "/opds/recent", books)
}

func (s *Server) writeAcquisition(w http.ResponseWriter, r *http.Request, title, self string, books []library.Book) {
	b := s.baseURL(r)
	f := feed{
		ID:    b + self,
		Title: title,
		Links: []link{
			{Rel: "self", Href: b + self, Type: acqType},
			{Rel: "start", Href: b + "/opds", Type: navType},
			{Rel: "up", Href: b + "/opds", Type: navType},
		},
	}
	for _, bk := range books {
		f.Entries = append(f.Entries, s.bookEntry(b, bk))
	}
	writeFeed(w, acqType, f)
}

func (s *Server) bookEntry(base string, bk library.Book) entry {
	e := entry{
		Title:   bk.Title,
		ID:      fmt.Sprintf("urn:booky:book:%d", bk.ID),
		Updated: bk.Added.UTC().Format(time.RFC3339),
	}
	if e.Updated == "0001-01-01T00:00:00Z" {
		e.Updated = time.Now().UTC().Format(time.RFC3339)
	}
	if bk.Authors != "" {
		e.Author = &author{Name: bk.Authors}
	}
	desc := bk.Comment
	if bk.Series != "" {
		desc = fmt.Sprintf("Series: %s #%.0f\n%s", bk.Series, bk.SeriesIndex, desc)
	}
	if desc != "" {
		e.Content = &content{Type: "text", Body: stripHTML(desc)}
	}
	if bk.HasCover {
		cov := fmt.Sprintf("%s/opds/cover/%d", base, bk.ID)
		e.Links = append(e.Links,
			link{Rel: relImage, Href: cov, Type: "image/jpeg"},
			link{Rel: relThumb, Href: cov, Type: "image/jpeg"},
		)
	}
	for _, fm := range bk.Formats {
		mime := formatMIME(fm.Format)
		e.Links = append(e.Links, link{
			Rel:   relAcq,
			Href:  fmt.Sprintf("%s/opds/download/%d/%s", base, bk.ID, strings.ToLower(fm.Format)),
			Type:  mime,
			Title: strings.ToUpper(fm.Format),
		})
	}
	return e
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	format := r.PathValue("format")
	bk, ok := s.lib.One(id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	path, ok := s.lib.FilePath(bk, format)
	if !ok {
		http.Error(w, "format not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", formatMIME(format))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`,
		sanitize(bk.Title), strings.ToLower(format)))
	http.ServeFile(w, r, path)
}

func (s *Server) cover(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	bk, ok := s.lib.One(id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	path, ok := s.lib.CoverPath(bk)
	if !ok {
		http.Error(w, "no cover", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, path)
}

func formatMIME(format string) string {
	switch strings.ToUpper(format) {
	case "EPUB", "KEPUB":
		return "application/epub+zip"
	case "PDF":
		return "application/pdf"
	case "MOBI":
		return "application/x-mobipocket-ebook"
	case "AZW3", "AZW":
		return "application/vnd.amazon.ebook"
	case "CBZ":
		return "application/x-cbz"
	case "FB2":
		return "application/x-fictionbook+xml"
	default:
		return "application/octet-stream"
	}
}

func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 1000 {
		out = out[:1000] + "…"
	}
	return out
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, s)
}
