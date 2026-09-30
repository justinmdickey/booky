package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/justindickey/booky/internal/auth"
	"github.com/justindickey/booky/internal/store"
)

func TestX4Upload(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "booky.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	am := auth.New(st)
	if err := am.CreateUser("reader", "secret"); err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, nil, am, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Register(mux)

	post := func(body string, authed bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/x4/upload", strings.NewReader(body))
		if authed {
			req.SetBasicAuth("reader", "secret")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	good := `{"books":[{"md5":"0123456789abcdef0123456789abcdef","title":"Dune","seconds":600,"pages_turned":20}]}`
	if rec := post(good, false); rec.Code != http.StatusUnauthorized {
		t.Errorf("no auth: got %d", rec.Code)
	}
	if rec := post(`{"books":[{"md5":"../etc","title":"x"}]}`, true); rec.Code != http.StatusBadRequest {
		t.Errorf("bad md5: got %d", rec.Code)
	}
	if rec := post(good, true); rec.Code != http.StatusOK {
		t.Fatalf("good upload: got %d %s", rec.Code, rec.Body)
	}
	books, err := st.X4Books()
	if err != nil || len(books) != 1 || books[0].Title != "Dune" || books[0].Seconds != 600 {
		t.Errorf("stored: %+v err=%v", books, err)
	}
}
