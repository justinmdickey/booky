// Package koreader contains helpers that mirror KOReader internals — most
// importantly the partial-MD5 document fingerprint that ties together a book's
// sync progress (kosync "binary" mode), its KOReader statistics row, and its
// Calibre file on disk.
package koreader

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
)

// PartialMD5 reproduces KOReader's util.partialMD5 exactly: it MD5s up to
// twelve 1 KiB chunks read at byte offsets 1024 * 4^i for i = -1..10
// (256, 1024, 4096, 16384, ... up to ~1 GiB), stopping at the first read past
// EOF. This is the same algorithm KOReader uses for the kosync "binary"
// document hash and for the `md5` column in statistics.sqlite3, so the three
// data sources join on it.
func PartialMD5(r io.ReadSeeker) (string, error) {
	const step = 1024
	const size = 1024
	h := md5.New()
	for i := -1; i <= 10; i++ {
		// offset = 1024 << (2*i); for i=-1 this is a right shift => 256.
		var offset int64
		if shift := 2 * i; shift >= 0 {
			offset = int64(step) << uint(shift)
		} else {
			offset = int64(step) >> uint(-shift)
		}
		if _, err := r.Seek(offset, io.SeekStart); err != nil {
			break
		}
		buf := make([]byte, size)
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			if n == 0 {
				break
			}
			// Partial read at tail: counted above, then stop.
			break
		}
		if err != nil {
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// PartialMD5File opens a file and computes its partial MD5.
func PartialMD5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return PartialMD5(f)
}
