// Package httpauth centralizes the bearer credential contract shared by the
// local HTTP plane (internal/http) and the server plane
// (internal/platform/server). It depends only on the standard library so either
// composition can import it without crossing the architecture gate.
package httpauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

const (
	// DigestSize is the fixed SHA-256 width every secret comparison is reduced
	// to before the constant-time compare.
	DigestSize = sha256.Size

	bearerScheme        = "Bearer "
	unauthorizedCode    = "unauthorized"
	unauthorizedMessage = "valid bearer token required"
)

// constantTimeCompare is indirected through a variable so package tests can
// observe the operand widths handed to the constant-time compare and prove the
// raw-length oracle stays closed. Production never reassigns it.
var constantTimeCompare = subtle.ConstantTimeCompare

// Options selects a plane's bearer extraction policy.
type Options struct {
	// AcceptAPIKey enables the X-API-Key header as a bearer-equivalent source.
	AcceptAPIKey bool
	// Trim trims surrounding whitespace from the Authorization header before
	// scheme matching and from the extracted secret.
	Trim bool
}

// LocalOptions mirrors internal/http.withAuth today: whitespace-tolerant and
// accepting both Authorization: Bearer and X-API-Key.
func LocalOptions() Options {
	return Options{AcceptAPIKey: true, Trim: true}
}

// ServerOptions mirrors platform/server requestAuthenticator today: the secret
// is byte-exact and only the Authorization: Bearer scheme is consulted.
func ServerOptions() Options {
	return Options{AcceptAPIKey: false, Trim: false}
}

// ExtractSecret returns the presented secret for the request. It recognizes the
// case-sensitive ASCII "Bearer " Authorization scheme and, when opts.AcceptAPIKey
// is set, the X-API-Key header as a fallback. A blank or absent candidate yields
// ok == false. The returned secret is normalized only as opts.Trim dictates, so
// byte-exact callers observe the header value unchanged.
func ExtractSecret(r *http.Request, opts Options) (string, bool) {
	authorization := r.Header.Get("Authorization")
	if opts.Trim {
		authorization = strings.TrimSpace(authorization)
	}
	if strings.HasPrefix(authorization, bearerScheme) {
		secret := authorization[len(bearerScheme):]
		if opts.Trim {
			secret = strings.TrimSpace(secret)
		}
		if !isBlank(secret) {
			return secret, true
		}
	}
	if !opts.AcceptAPIKey {
		return "", false
	}
	apiKey := r.Header.Get("X-API-Key")
	if opts.Trim {
		apiKey = strings.TrimSpace(apiKey)
	}
	if isBlank(apiKey) {
		return "", false
	}
	return apiKey, true
}

func isBlank(secret string) bool {
	return secret == "" || strings.TrimSpace(secret) == ""
}

// SecretDigest reduces a secret to its fixed-width SHA-256 digest.
func SecretDigest(secret string) [DigestSize]byte {
	return sha256.Sum256([]byte(secret))
}

// CompareDigest compares two fixed-width digests in constant time. The operand
// width is a compile-time constant, so neither the cost nor the result depends
// on the raw lengths that produced the digests.
func CompareDigest(a, b [DigestSize]byte) bool {
	return constantTimeCompare(a[:], b[:]) == 1
}

// EqualSecret reports whether the presented secret matches the configured one.
// Both sides are hashed to a fixed 32-byte digest first, closing the length
// oracle of comparing raw bytes with subtle.ConstantTimeCompare.
func EqualSecret(presented, configured string) bool {
	return CompareDigest(SecretDigest(presented), SecretDigest(configured))
}

// WriteUnauthorized emits the canonical 401 both planes converge on: the
// WWW-Authenticate challenge plus the nested error envelope the embedded web
// client parses.
func WriteUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="cortex"`)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]map[string]string{
		"error": {"code": unauthorizedCode, "message": unauthorizedMessage},
	})
}
