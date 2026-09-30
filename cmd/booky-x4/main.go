// Command booky-x4 reads reading stats off an Xteink X4's SD card (CrossInk
// firmware) and uploads them to Booky.
//
//	booky-x4 [flags] /run/media/$USER/XTEINK
//
// It walks every .epub on the card, finds the book's CrossInk cache folder,
// and sends the totals for books that have been opened. Books are keyed by
// KOReader's partial-MD5, the same id the X4 uses for kosync, so uploads line
// up with progress the X4 has already synced to Booky.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/justindickey/booky/internal/crossink"
	"github.com/justindickey/booky/internal/koreader"
	"github.com/justindickey/booky/internal/store"
)

func main() {
	server := flag.String("server", os.Getenv("BOOKY_URL"), "Booky base URL (or $BOOKY_URL)")
	user := flag.String("user", os.Getenv("BOOKY_USER"), "Booky dashboard username (or $BOOKY_USER)")
	pass := flag.String("pass", os.Getenv("BOOKY_PASS"), "Booky dashboard password (or $BOOKY_PASS)")
	dryRun := flag.Bool("dry-run", false, "print what would be uploaded without sending it")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: booky-x4 [flags] CARD_MOUNT\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	card := flag.Arg(0)
	if _, err := os.Stat(filepath.Join(card, ".crosspoint")); err != nil {
		fatalf("%s doesn't look like a CrossInk card (no .crosspoint folder)", card)
	}

	books, skipped, err := scan(card)
	if err != nil {
		fatalf("%v", err)
	}
	printTable(books)
	fmt.Printf("\n%d opened books, %d never opened\n", len(books), skipped)

	if *dryRun {
		return
	}
	if *server == "" || *user == "" || *pass == "" {
		fatalf("need -server, -user and -pass (or BOOKY_URL, BOOKY_USER, BOOKY_PASS)")
	}
	n, err := upload(*server, *user, *pass, books)
	if err != nil {
		fatalf("upload: %v", err)
	}
	fmt.Printf("uploaded %d books to %s\n", n, *server)
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "booky-x4: "+format+"\n", a...)
	os.Exit(1)
}

// scan walks the card and returns one X4Book per opened EPUB.
func scan(card string) ([]store.X4Book, int, error) {
	var books []store.X4Book
	skipped := 0
	err := filepath.WalkDir(card, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != card && (strings.HasPrefix(name, ".") || name == "System Volume Information") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(name), ".epub") || strings.HasPrefix(name, ".") {
			return nil
		}
		rel, err := filepath.Rel(card, p)
		if err != nil {
			return err
		}
		cardPath := "/" + filepath.ToSlash(rel)
		state, found, err := crossink.ReadBookState(card, cardPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", cardPath, err)
		}
		if !found || (!state.HasStats && !state.HasPercent) {
			skipped++
			return nil
		}
		md5, err := koreader.PartialMD5File(p)
		if err != nil {
			return fmt.Errorf("%s: %w", cardPath, err)
		}
		meta := readMeta(p)
		if meta.Title == "" {
			meta.Title = strings.TrimSuffix(name, filepath.Ext(name))
		}
		b := store.X4Book{
			MD5:          md5,
			Title:        meta.Title,
			Authors:      meta.Authors,
			Series:       meta.Series,
			CardPath:     cardPath,
			Sessions:     state.Stats.Sessions,
			Seconds:      state.Stats.Seconds,
			PagesTurned:  state.Stats.PagesTurned,
			Completed:    state.Stats.Completed,
			StartDate:    state.Stats.StartDate.String(),
			FinishedDate: state.Stats.FinishedDate.String(),
			EstLeftSecs:  state.Stats.EstimatedSecLeft,
		}
		if state.HasPercent {
			pct := state.Percent
			b.Percent = &pct
		}
		books = append(books, b)
		return nil
	})
	sort.Slice(books, func(i, j int) bool { return books[i].CardPath < books[j].CardPath })
	return books, skipped, err
}

type bookMeta struct{ Title, Authors, Series string }

// readMeta pulls title, authors and series from the EPUB's OPF. Errors just
// mean an empty result; the filename stands in for a missing title.
func readMeta(p string) bookMeta {
	var m bookMeta
	z, err := zip.OpenReader(p)
	if err != nil {
		return m
	}
	defer z.Close()
	read := func(name string) []byte {
		f, err := z.Open(name)
		if err != nil {
			return nil
		}
		defer f.Close()
		b, _ := io.ReadAll(io.LimitReader(f, 4<<20))
		return b
	}
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if xml.Unmarshal(read("META-INF/container.xml"), &container) != nil || len(container.Rootfiles) == 0 {
		return m
	}
	var opf struct {
		Titles   []string `xml:"metadata>title"`
		Creators []string `xml:"metadata>creator"`
		Metas    []struct {
			Name    string `xml:"name,attr"`
			Content string `xml:"content,attr"`
		} `xml:"metadata>meta"`
	}
	if xml.Unmarshal(read(path.Clean(container.Rootfiles[0].FullPath)), &opf) != nil {
		return m
	}
	if len(opf.Titles) > 0 {
		m.Title = strings.TrimSpace(opf.Titles[0])
	}
	var authors []string
	for _, c := range opf.Creators {
		if c = strings.TrimSpace(c); c != "" {
			authors = append(authors, c)
		}
	}
	m.Authors = strings.Join(authors, ", ")
	for _, mt := range opf.Metas {
		if mt.Name == "calibre:series" {
			m.Series = mt.Content
		}
	}
	return m
}

func upload(server, user, pass string, books []store.X4Book) (int, error) {
	body, err := json.Marshal(map[string]any{"books": books})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest("POST", strings.TrimRight(server, "/")+"/api/x4/upload", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(user, pass)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return 0, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	var out struct {
		Books int `json:"books"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.Books, nil
}

func printTable(books []store.X4Book) {
	for _, b := range books {
		pct := "   –"
		if b.Percent != nil {
			pct = fmt.Sprintf("%3.0f%%", *b.Percent)
		}
		done := ""
		if b.Completed {
			done = " (finished)"
		}
		fmt.Printf("%s  %6s  %4d pages  %3d sessions  %s%s\n", pct,
			(time.Duration(b.Seconds) * time.Second).Round(time.Minute).String(),
			b.PagesTurned, b.Sessions, b.Title, done)
	}
}
