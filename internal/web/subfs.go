package web

import (
	"io/fs"
	"net/http"
)

// newSubFS returns an http.FileSystem rooted at the embedded static/ dir.
func newSubFS() (http.FileSystem, error) {
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}
