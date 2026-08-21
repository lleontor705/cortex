package projectprotocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

// LimitWriter is the counting writer/hash used by every bounded canonical
// encoding in this package (metadata, revision digests, protocol bundles).
//
// Contract (REQ-DOS-002, REQ-LIMIT-003):
//   - the total accepted byte count never exceeds the configured limit;
//   - a Write whose completion would reach limit+1 is rejected in full: it
//     contributes zero bytes, the internal buffer is discarded (no partial
//     output), and the writer enters a failed state;
//   - once failed, Bytes returns nil and every subsequent Write fails with
//     the same typed limit error;
//   - the running SHA-256 covers exactly the accepted bytes, so a successful
//     writer yields both the bounded canonical output and its digest/ETag.
type LimitWriter struct {
	limit  int64
	count  int64
	buf    bytes.Buffer
	hasher hash.Hash
	failed *Error
}

// NewLimitWriter returns a counting writer that accepts at most limit bytes.
// The limit must be non-negative.
func NewLimitWriter(limit int64) *LimitWriter {
	return &LimitWriter{limit: limit, hasher: sha256.New()}
}

// Write implements io.Writer with the fail-closed limit contract documented
// on LimitWriter.
func (w *LimitWriter) Write(p []byte) (int, error) {
	if w.failed != nil {
		return 0, w.failed
	}
	if w.count+int64(len(p)) > w.limit {
		w.failed = NewLimitError(ErrCodeLimitExceeded, w.limit)
		w.buf.Reset()
		return 0, w.failed
	}
	n, err := w.buf.Write(p)
	if err != nil {
		return n, err
	}
	// hash.Hash.Write never returns an error.
	w.hasher.Write(p[:n])
	w.count += int64(n)
	return n, nil
}

// Bytes returns the accepted bytes, or nil once the writer has aborted. A
// failed encoding therefore never exposes partial output.
func (w *LimitWriter) Bytes() []byte {
	if w.failed != nil {
		return nil
	}
	return w.buf.Bytes()
}

// Count returns the number of accepted bytes; it never exceeds Limit.
func (w *LimitWriter) Count() int64 { return w.count }

// Limit returns the configured bound.
func (w *LimitWriter) Limit() int64 { return w.limit }

// Failed reports whether the writer aborted at the limit.
func (w *LimitWriter) Failed() bool { return w.failed != nil }

// Digest returns the "sha256:<hex>" digest of the accepted bytes.
func (w *LimitWriter) Digest() string {
	return "sha256:" + hex.EncodeToString(w.hasher.Sum(nil))
}

// ETag returns the opaque quoted ETag of the accepted bytes.
func (w *LimitWriter) ETag() string {
	return `"` + hex.EncodeToString(w.hasher.Sum(nil)) + `"`
}
