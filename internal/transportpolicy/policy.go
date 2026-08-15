// Package transportpolicy defines the shared transport security policy for
// every client that sends Bearer credentials to an HTTP(S) destination
// (REM-TRANSPORT-001): HTTPS is required for non-loopback destinations, plain
// HTTP is only allowed on strict loopback, and redirects must never downgrade
// the scheme or change the origin before the Authorization header is
// forwarded.
//
// The policy is deliberately deterministic: hostname resolution (DNS, hosts
// files) is never consulted. "localhost" is accepted as the RFC 6761
// special-use name only in its exact dotless form; every other name must be a
// literal loopback IP.
package transportpolicy

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Stable, machine-readable rejection codes.
const (
	CodeInvalidURL        = "invalid_url"
	CodeInsecureScheme    = "insecure_scheme"
	CodeUnsupportedScheme = "unsupported_scheme"
	CodeSchemeDowngrade   = "scheme_downgrade"
	CodeOriginChange      = "origin_change"
)

// Error is the typed policy rejection. Messages deliberately carry only the
// scheme and host: full URLs may embed userinfo and must never be logged.
type Error struct {
	Code   string
	Reason string
}

func (e *Error) Error() string {
	return "transport policy: " + e.Reason
}

func reject(code, format string, args ...any) *Error {
	return &Error{Code: code, Reason: fmt.Sprintf(format, args...)}
}

// ValidateBearerDestination reports whether rawURL is an acceptable initial
// destination for a request carrying Bearer credentials. It accepts any HTTPS
// destination and plain HTTP only when the host is strict loopback. The check
// must run before the Authorization header is ever attached.
func ValidateBearerDestination(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return reject(CodeInvalidURL, "destination must be an absolute HTTP(S) URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if !IsStrictLoopbackHost(u.Hostname()) {
			return reject(CodeInsecureScheme,
				"plain HTTP to %q is forbidden for Bearer destinations; use HTTPS (only strict loopback may use plain HTTP)", safeHostLabel(u))
		}
		return nil
	default:
		return reject(CodeUnsupportedScheme, "scheme %q is not an HTTP(S) transport", u.Scheme)
	}
}

// CheckBearerRedirect implements http.Client.CheckRedirect semantics for
// Bearer-authenticated requests. It runs before the redirected request (and
// therefore before the Authorization header) is sent, and blocks any redirect
// that downgrades the transport away from HTTPS or changes the origin of the
// original destination.
func CheckBearerRedirect(req *http.Request, via []*http.Request) error {
	if req == nil || req.URL == nil {
		return reject(CodeInvalidURL, "redirect target is not a valid HTTP request")
	}
	targetScheme := strings.ToLower(req.URL.Scheme)
	if targetScheme != "http" && targetScheme != "https" {
		return reject(CodeUnsupportedScheme, "redirect to scheme %q is not an HTTP(S) transport", req.URL.Scheme)
	}
	originalScheme := ""
	if len(via) > 0 && via[0] != nil && via[0].URL != nil {
		originalScheme = strings.ToLower(via[0].URL.Scheme)
	}
	// A redirect away from HTTPS — even towards strict loopback — is a
	// transport downgrade and is rejected before credentials are forwarded.
	if originalScheme == "https" && targetScheme != "https" {
		return reject(CodeSchemeDowngrade,
			"redirect from HTTPS to %q would downgrade the transport before forwarding credentials", targetScheme)
	}
	// The redirect target must itself be a valid Bearer destination (plain
	// HTTP is only allowed on strict loopback).
	if targetScheme == "http" && !IsStrictLoopbackHost(req.URL.Hostname()) {
		return reject(CodeInsecureScheme,
			"redirect to plain HTTP at %q is forbidden for Bearer destinations", safeHostLabel(req.URL))
	}
	if len(via) == 0 || via[0] == nil || via[0].URL == nil {
		return nil
	}
	original, err := originKey(via[0].URL)
	if err != nil {
		return reject(CodeInvalidURL, "original destination is not a valid HTTP(S) URL")
	}
	target, err := originKey(req.URL)
	if err != nil {
		return reject(CodeInvalidURL, "redirect target is not a valid HTTP(S) URL")
	}
	if original != target {
		return reject(CodeOriginChange,
			"redirect from %q to %q changes the origin; Bearer credentials are never forwarded cross-origin", original, target)
	}
	return nil
}

// IsStrictLoopbackHost reports whether host is a strict loopback destination:
// an IPv4 dotted-quad literal inside 127.0.0.0/8, the IPv6 loopback address
// ::1 (any valid IPv6 spelling of that one address), or the exact dotless
// RFC 6761 name "localhost" (case-insensitive). IPv4-mapped IPv6 spellings
// of loopback (::ffff:127.0.0.1, 0:0:0:0:0:ffff:7f00:1, ...) are IPv6
// literals, not the sanctioned native forms, and are rejected. The input is
// a hostname without port (url.URL.Hostname() shape). No name resolution is
// performed: any other name is not loopback.
func IsStrictLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		// Sanction only the native dotted-quad spelling of 127.0.0.0/8.
		// A colon means the literal was written as IPv6 (an IPv4-mapped
		// spelling such as ::ffff:127.0.0.1 or ::ffff:7f00:1); whether that
		// form actually reaches loopback depends on the destination's
		// dual-stack behavior, so it is never treated as strict loopback.
		return !strings.Contains(host, ":") && ip4[0] == 127
	}
	return ip.Equal(net.IPv6loopback)
}

// originKey builds a canonical comparable origin (scheme, canonical host,
// explicit default port) for u.
func originKey(u *url.URL) (string, error) {
	if u == nil || u.Host == "" || u.Scheme == "" {
		return "", fmt.Errorf("not an absolute HTTP(S) URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("scheme %q is not an HTTP(S) transport", u.Scheme)
	}
	host := canonicalHost(u.Hostname())
	port := u.Port()
	if port == "" {
		port = defaultPort(scheme)
	}
	return scheme + "://" + host + ":" + port, nil
}

func canonicalHost(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if ip := net.ParseIP(host); ip != nil {
		return ip.String() // canonical IP text form (e.g. IPv6 compression)
	}
	return host
}

func defaultPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// safeHostLabel renders scheme+host for error messages without ever including
// userinfo or the rest of the URL.
func safeHostLabel(u *url.URL) string {
	return strings.ToLower(u.Scheme) + "://" + canonicalHost(u.Hostname())
}
