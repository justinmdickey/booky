package koreader

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"testing"
)

// referencePartialMD5 is an independent, straightforward implementation of
// KOReader's algorithm to cross-check the streaming version.
func referencePartialMD5(data []byte) string {
	h := md5.New()
	for i := -1; i <= 10; i++ {
		var off int64
		if s := 2 * i; s >= 0 {
			off = int64(1024) << uint(s)
		} else {
			off = int64(1024) >> uint(-s)
		}
		if off >= int64(len(data)) {
			break
		}
		end := off + 1024
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		h.Write(data[off:end])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestPartialMD5MatchesReference(t *testing.T) {
	for _, size := range []int{300, 2000, 5000, 100000, 500000} {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte((i*31 + 7) % 251)
		}
		got, err := PartialMD5(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		want := referencePartialMD5(data)
		if got != want {
			t.Errorf("size %d: got %s want %s", size, got, want)
		}
	}
}

func TestPartialMD5Deterministic(t *testing.T) {
	data := bytes.Repeat([]byte("the quick brown fox "), 1000)
	a, _ := PartialMD5(bytes.NewReader(data))
	b, _ := PartialMD5(bytes.NewReader(data))
	if a != b {
		t.Fatalf("not deterministic: %s != %s", a, b)
	}
	if len(a) != 32 {
		t.Fatalf("expected 32-char hex, got %q", a)
	}
}
