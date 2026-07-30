package identity

import (
	"context"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

type TokenVerifier interface {
	Verify(context.Context, string, string) (Principal, error)
}
type OAuthConfig struct {
	Issuer     string
	JWKSURL    string
	Audience   []string
	ClockSkew  time.Duration
	HTTPClient *http.Client
	MaxKeys    int
	CacheTTL   time.Duration
	Mapper     ClaimsMapper
}
type OAuthVerifier struct {
	cfg     OAuthConfig
	client  *http.Client
	mu      sync.Mutex
	keys    map[string]jwk
	fetched time.Time
}
type jwk struct {
	Kty, Kid, Alg, Use, N, E, X, Y, Crv string
	KeyOps                              []string `json:"key_ops"`
}

func NewOAuthVerifier(cfg OAuthConfig) *OAuthVerifier {
	if cfg.ClockSkew <= 0 {
		cfg.ClockSkew = time.Minute
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 5 * time.Minute
	}
	if cfg.MaxKeys <= 0 || cfg.MaxKeys > 256 {
		cfg.MaxKeys = 64
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &OAuthVerifier{cfg: cfg, client: client, keys: make(map[string]jwk)}
}

func (v *OAuthVerifier) Verify(ctx context.Context, raw, resource string) (Principal, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Principal{}, ErrInvalidToken
	}
	header, claims, err := decodeJWT(parts[0], parts[1])
	if err != nil {
		return Principal{}, ErrInvalidToken
	}
	alg, _ := header["alg"].(string)
	kid, _ := header["kid"].(string)
	if !allowedAlgorithm(alg) || kid == "" {
		return Principal{}, ErrInvalidToken
	}
	if err = v.loadKeys(ctx, false); err != nil {
		return Principal{}, err
	}
	key, ok := v.key(kid)
	if !ok {
		if err = v.loadKeys(ctx, true); err != nil {
			return Principal{}, err
		}
		key, ok = v.key(kid)
	}
	if !jwkCompatible(key, alg) {
		return Principal{}, ErrInvalidToken
	}
	if !ok {
		return Principal{}, errors.New("unknown signing key")
	}
	if err = verifySignature(key, alg, parts[0]+"."+parts[1], parts[2]); err != nil {
		return Principal{}, ErrInvalidToken
	}
	if iss, _ := claims["iss"].(string); iss != v.cfg.Issuer {
		return Principal{}, errors.New("issuer mismatch")
	}
	if !audienceOK(claims["aud"], v.cfg.Audience) || (resource != "" && !audienceOK(claims["aud"], []string{resource})) {
		return Principal{}, errors.New("audience mismatch")
	}
	now := time.Now()
	if n, ok := numericClaim(claims["exp"]); !ok || now.After(time.Unix(n, 0).Add(v.cfg.ClockSkew)) {
		return Principal{}, errors.New("token expired")
	}
	if n, ok := numericClaim(claims["nbf"]); ok && now.Before(time.Unix(n, 0).Add(-v.cfg.ClockSkew)) {
		return Principal{}, errors.New("token not active")
	}
	return v.cfg.Mapper.Map(claims, "oidc")
}

func (v *OAuthVerifier) key(kid string) (jwk, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	k, ok := v.keys[kid]
	return k, ok
}
func (v *OAuthVerifier) loadKeys(ctx context.Context, force bool) error {
	v.mu.Lock()
	if !force && len(v.keys) > 0 && time.Since(v.fetched) < v.cfg.CacheTTL {
		v.mu.Unlock()
		return nil
	}
	v.mu.Unlock()
	url := v.cfg.JWKSURL
	if url == "" {
		urls := []string{strings.TrimRight(v.cfg.Issuer, "/") + "/.well-known/openid-configuration", strings.TrimRight(v.cfg.Issuer, "/") + "/.well-known/oauth-authorization-server"}
		var discoveryErr error
		for _, discoveryURL := range urls {
			b, err := v.get(ctx, discoveryURL)
			if err != nil {
				discoveryErr = err
				continue
			}
			var m struct {
				JWKSURI string `json:"jwks_uri"`
			}
			if err := json.Unmarshal(b, &m); err != nil || !validJWKSURL(m.JWKSURI) {
				discoveryErr = errors.New("jwks discovery failed")
				continue
			}
			url = m.JWKSURI
			break
		}
		if url == "" {
			return discoveryErr
		}
	}
	b, err := v.get(ctx, url)
	if err != nil {
		return err
	}
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err = json.Unmarshal(b, &doc); err != nil {
		return err
	}
	if len(doc.Keys) == 0 || len(doc.Keys) > v.cfg.MaxKeys {
		return errors.New("invalid jwks")
	}
	next := make(map[string]jwk, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kid != "" && allowedAlgorithm(k.Alg) && jwkUsable(k) {
			next[k.Kid] = k
		}
	}
	v.mu.Lock()
	v.keys = next
	v.fetched = time.Now()
	v.mu.Unlock()
	return nil
}
func (v *OAuthVerifier) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func decodeJWT(h, c string) (map[string]any, map[string]any, error) {
	dec := base64.RawURLEncoding
	hb, e := dec.DecodeString(h)
	if e != nil {
		return nil, nil, e
	}
	cb, e := dec.DecodeString(c)
	if e != nil {
		return nil, nil, e
	}
	var hm, cm map[string]any
	if json.Unmarshal(hb, &hm) != nil || json.Unmarshal(cb, &cm) != nil {
		return nil, nil, ErrInvalidToken
	}
	return hm, cm, nil
}
func audienceOK(value any, want []string) bool {
	var got []string
	switch x := value.(type) {
	case string:
		got = []string{x}
	case []any:
		for _, v := range x {
			if s, ok := v.(string); ok {
				got = append(got, s)
			}
		}
	}
	for _, a := range want {
		for _, g := range got {
			if a == g {
				return true
			}
		}
	}
	return false
}
func numericClaim(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case json.Number:
		n, e := x.Int64()
		return n, e == nil
	}
	return 0, false
}
func verifySignature(k jwk, alg, input, sig string) error {
	raw, e := base64.RawURLEncoding.DecodeString(sig)
	if e != nil {
		return e
	}
	if !jwkCompatible(k, alg) {
		return errors.New("incompatible jwk")
	}
	pub, e := publicKey(k)
	if e != nil {
		return e
	}
	var hash crypto.Hash
	switch alg {
	case "RS256":
		hash = crypto.SHA256
	case "RS384":
		hash = crypto.SHA384
	case "RS512":
		hash = crypto.SHA512
	case "PS256":
		hash = crypto.SHA256
	case "ES256":
		hash = crypto.SHA256
	case "ES384":
		hash = crypto.SHA384
	case "ES512":
		hash = crypto.SHA512
	default:
		return errors.New("unsupported algorithm")
	}
	if !hash.Available() {
		return errors.New("hash unavailable")
	}
	if alg == "EdDSA" {
		p, ok := pub.(ed25519.PublicKey)
		if !ok || len(p) != ed25519.PublicKeySize {
			return errors.New("invalid ed25519 key")
		}
		if ed25519.Verify(p, []byte(input), raw) {
			return nil
		}
		return errors.New("signature mismatch")
	}
	h := hash.New()
	h.Write([]byte(input))
	switch p := pub.(type) {
	case *rsa.PublicKey:
		if strings.HasPrefix(alg, "PS") {
			return rsa.VerifyPSS(p, hash, h.Sum(nil), raw, nil)
		}
		return rsa.VerifyPKCS1v15(p, hash, h.Sum(nil), raw)
	case *ecdsa.PublicKey:
		if len(raw)%2 != 0 {
			return errors.New("invalid ecdsa signature")
		}
		if ecdsa.Verify(p, h.Sum(nil), new(big.Int).SetBytes(raw[:len(raw)/2]), new(big.Int).SetBytes(raw[len(raw)/2:])) {
			return nil
		}
		return errors.New("signature mismatch")
	default:
		return errors.New("unsupported key")
	}
}

var _ = sha256.New
var _ = sha512.New

func publicKey(k jwk) (any, error) {
	dec := base64.RawURLEncoding
	switch k.Kty {
	case "RSA":
		n, e := dec.DecodeString(k.N)
		if e != nil {
			return nil, e
		}
		eb, e := dec.DecodeString(k.E)
		if e != nil {
			return nil, e
		}
		ei := 0
		for _, b := range eb {
			ei = ei<<8 | int(b)
		}
		N := new(big.Int).SetBytes(n)
		if N.Sign() <= 0 || ei < 3 || ei%2 == 0 || len(n) < 256 {
			return nil, errors.New("invalid RSA key")
		}
		return &rsa.PublicKey{N: N, E: ei}, nil
	case "OKP":
		if k.Crv != "Ed25519" {
			return nil, errors.New("unsupported OKP curve")
		}
		x, e := dec.DecodeString(k.X)
		if e != nil || len(x) != ed25519.PublicKeySize {
			return nil, errors.New("invalid ed25519 key")
		}
		return ed25519.PublicKey(x), e
	case "EC":
		x, e := dec.DecodeString(k.X)
		if e != nil {
			return nil, e
		}
		y, e := dec.DecodeString(k.Y)
		if e != nil {
			return nil, e
		}
		var crv elliptic.Curve
		switch k.Crv {
		case "P-256":
			crv = elliptic.P256()
		case "P-384":
			crv = elliptic.P384()
		case "P-521":
			crv = elliptic.P521()
		default:
			return nil, errors.New("unsupported EC curve")
		}
		px, py := new(big.Int).SetBytes(x), new(big.Int).SetBytes(y)
		if !validECPoint(k.Crv, x, y) {
			return nil, errors.New("invalid EC point")
		}
		return &ecdsa.PublicKey{Curve: crv, X: px, Y: py}, nil
	}
	return nil, errors.New("unsupported jwk")
}

func validECPoint(crv string, x, y []byte) bool {
	curveBytes := append([]byte{4}, append(x, y...)...)
	var c ecdh.Curve
	switch crv {
	case "P-256":
		c = ecdh.P256()
	case "P-384":
		c = ecdh.P384()
	case "P-521":
		c = ecdh.P521()
	default:
		return false
	}
	_, err := c.NewPublicKey(curveBytes)
	return err == nil
}

func allowedAlgorithm(alg string) bool {
	switch alg {
	case "RS256", "RS384", "RS512", "ES256", "ES384", "ES512", "EdDSA":
		return true
	default:
		return false
	}
}
func validJWKSURL(u string) bool {
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}
func jwkUsable(k jwk) bool {
	if k.Use != "" && k.Use != "sig" {
		return false
	}
	for _, op := range k.KeyOps {
		if op != "verify" {
			return false
		}
	}
	return true
}
func jwkCompatible(k jwk, alg string) bool {
	if !allowedAlgorithm(alg) || !jwkUsable(k) || (k.Alg != "" && k.Alg != alg) {
		return false
	}
	switch alg {
	case "RS256", "RS384", "RS512":
		return k.Kty == "RSA"
	case "ES256":
		return k.Kty == "EC" && k.Crv == "P-256"
	case "ES384":
		return k.Kty == "EC" && k.Crv == "P-384"
	case "ES512":
		return k.Kty == "EC" && k.Crv == "P-521"
	case "EdDSA":
		return k.Kty == "OKP" && k.Crv == "Ed25519"
	}
	return false
}
