package projectprotocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestLimitWriterExactLimitAccepted(t *testing.T) {
	w := NewLimitWriter(16)
	n, err := w.Write([]byte("1234567890123456"))
	if err != nil || n != 16 {
		t.Fatalf("exact write: n=%d err=%v", n, err)
	}
	if w.Count() != 16 {
		t.Fatalf("count=%d want 16", w.Count())
	}
	if !bytes.Equal(w.Bytes(), []byte("1234567890123456")) {
		t.Fatalf("bytes mismatch: %q", w.Bytes())
	}
}

func TestLimitWriterAbortsAtLimitPlusOne(t *testing.T) {
	w := NewLimitWriter(16)
	if _, err := w.Write([]byte("12345678")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// This write would reach 17 bytes: rejected in full.
	n, err := w.Write([]byte("901234567"))
	if err == nil {
		t.Fatal("crossing write accepted")
	}
	if n != 0 {
		t.Fatalf("crossing write contributed %d bytes", n)
	}
	if !errorHasCode(err, ErrCodeLimitExceeded) {
		t.Fatalf("wrong error code: %v", err)
	}
	if got := err.(*Error).Limit; got != 16 {
		t.Fatalf("error limit=%d want 16", got)
	}
	// No partial output.
	if w.Bytes() != nil {
		t.Fatalf("partial output exposed after abort: %q", w.Bytes())
	}
	if w.Count() > w.Limit() {
		t.Fatalf("count %d exceeds limit %d", w.Count(), w.Limit())
	}
	// Sticky failure.
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("write after abort accepted")
	}
	if w.Bytes() != nil {
		t.Fatal("bytes exposed after sticky failure")
	}
}

func TestLimitWriterBoundarySplitAcrossWrites(t *testing.T) {
	// The crossing write is rejected even when the buffer was exactly at
	// the limit before it.
	w := NewLimitWriter(4)
	if _, err := w.Write([]byte("abcd")); err != nil {
		t.Fatalf("fill to limit: %v", err)
	}
	if n, err := w.Write([]byte("e")); err == nil || n != 0 {
		t.Fatalf("limit+1 single byte accepted: n=%d err=%v", n, err)
	}
	// A zero-length write at the limit is still fine.
	w2 := NewLimitWriter(4)
	if n, err := w2.Write(nil); err != nil || n != 0 {
		t.Fatalf("zero write: n=%d err=%v", n, err)
	}
}

func TestLimitWriterHashMatchesAcceptedBytes(t *testing.T) {
	payload := []byte(strings.Repeat("x", 1000))
	w := NewLimitWriter(2048)
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(append(append([]byte{}, payload...), payload...))
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])
	if got := w.Digest(); got != wantDigest {
		t.Fatalf("digest mismatch: %s want %s", got, wantDigest)
	}
	wantETag := `"` + hex.EncodeToString(sum[:]) + `"`
	if got := w.ETag(); got != wantETag {
		t.Fatalf("etag mismatch: %s want %s", got, wantETag)
	}
}

func TestLimitWriterZeroLimit(t *testing.T) {
	w := NewLimitWriter(0)
	if _, err := w.Write([]byte("a")); !errorHasCode(err, ErrCodeLimitExceeded) {
		t.Fatalf("zero limit accepted a byte: %v", err)
	}
	// A zero-length write on a fresh zero-limit writer is fine.
	fresh := NewLimitWriter(0)
	if n, err := fresh.Write(nil); err != nil || n != 0 {
		t.Fatalf("zero-length write on zero limit: n=%d err=%v", n, err)
	}
}
