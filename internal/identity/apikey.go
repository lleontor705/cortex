package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

var (
	ErrInvalidToken      = errors.New("invalid token")
	ErrTokenRevoked      = errors.New("token revoked")
	ErrTokenExpired      = errors.New("token expired")
	ErrInsufficientScope = errors.New("insufficient scope")
)

type TokenRecord struct {
	ID            string
	Prefix        string
	Digest        string
	Subject       string
	PrincipalType string
	OrgID         string
	Workspaces    []string
	Scopes        []string
	ExpiresAt     time.Time
	RevokedAt     time.Time
	LastUsedAt    time.Time
}

type TokenIssue struct {
	Subject, PrincipalType, OrgID string
	Workspaces, Scopes            []string
	ExpiresAt                     time.Time
}

type IssuedToken struct {
	Secret string
	Record TokenRecord
}

type TokenStore interface {
	Issue(context.Context, TokenIssue) (IssuedToken, error)
	Verify(context.Context, string, string) (Principal, error)
	Revoke(context.Context, string) error
	Rotate(context.Context, string) (IssuedToken, error)
}

type MemoryTokenStore struct {
	mu       sync.RWMutex
	key      []byte
	tokens   map[string]TokenRecord
	byPrefix map[string]string
}

func NewMemoryTokenStore(key []byte) *MemoryTokenStore {
	if len(key) < 32 {
		sum := sha256.Sum256(key)
		key = sum[:]
	}
	return &MemoryTokenStore{key: append([]byte(nil), key...), tokens: make(map[string]TokenRecord), byPrefix: make(map[string]string)}
}

func (s *MemoryTokenStore) Issue(_ context.Context, in TokenIssue) (IssuedToken, error) {
	if s == nil || len(s.key) == 0 || in.Subject == "" {
		return IssuedToken{}, ErrInvalidToken
	}
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return IssuedToken{}, err
	}
	secret := "ctx_" + base64.RawURLEncoding.EncodeToString(secretBytes)
	r := TokenRecord{ID: randomID(), Prefix: secret[:12], Digest: s.digest(secret), Subject: in.Subject, PrincipalType: in.PrincipalType, OrgID: in.OrgID, Workspaces: cloneStrings(in.Workspaces), Scopes: cloneStrings(in.Scopes), ExpiresAt: in.ExpiresAt}
	s.mu.Lock()
	s.tokens[r.ID] = r
	s.byPrefix[r.Prefix] = r.ID
	s.mu.Unlock()
	return IssuedToken{Secret: secret, Record: r}, nil
}

func (s *MemoryTokenStore) Verify(_ context.Context, secret, requiredScope string) (Principal, error) {
	if s == nil || secret == "" {
		return Principal{}, ErrInvalidToken
	}
	d := s.digest(secret)
	s.mu.RLock()
	r, found := TokenRecord{}, false
	if len(secret) >= 12 {
		if id, ok := s.byPrefix[secret[:12]]; ok {
			r, found = s.tokens[id]
		}
	}
	if found {
		found = hmac.Equal([]byte(r.Digest), []byte(d))
	}
	s.mu.RUnlock()
	if !found {
		return Principal{}, ErrInvalidToken
	}
	now := time.Now()
	if !r.RevokedAt.IsZero() {
		return Principal{}, ErrTokenRevoked
	}
	if !r.ExpiresAt.IsZero() && !now.Before(r.ExpiresAt) {
		return Principal{}, ErrTokenExpired
	}
	if requiredScope != "" && !contains(r.Scopes, requiredScope) {
		return Principal{}, ErrInsufficientScope
	}
	s.mu.Lock()
	r.LastUsedAt = now
	s.tokens[r.ID] = r
	s.mu.Unlock()
	return NewPrincipal(r.Subject, r.PrincipalType, r.OrgID, r.Workspaces, nil, r.Scopes, "api_key", r.ID), nil
}

func (s *MemoryTokenStore) Revoke(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.tokens[id]
	if !ok {
		return ErrInvalidToken
	}
	r.RevokedAt = time.Now()
	s.tokens[id] = r
	return nil
}

func (s *MemoryTokenStore) Rotate(ctx context.Context, id string) (IssuedToken, error) {
	s.mu.RLock()
	r, ok := s.tokens[id]
	s.mu.RUnlock()
	if !ok {
		return IssuedToken{}, ErrInvalidToken
	}
	return s.Issue(ctx, TokenIssue{Subject: r.Subject, PrincipalType: r.PrincipalType, OrgID: r.OrgID, Workspaces: r.Workspaces, Scopes: r.Scopes, ExpiresAt: r.ExpiresAt})
}

func (s *MemoryTokenStore) digest(secret string) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(secret))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func cloneStrings(v []string) []string { return append([]string(nil), v...) }
func contains(v []string, want string) bool {
	for _, x := range v {
		if x == want {
			return true
		}
	}
	return false
}
