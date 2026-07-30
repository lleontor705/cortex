package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ClaimsMapper converts verified issuer claims into an immutable Principal.
// Client/request fields are intentionally not accepted by this port.
type ClaimsMapper struct{}

func (ClaimsMapper) Map(claims map[string]any, authMethod string) (Principal, error) {
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return Principal{}, ErrInvalidToken
	}
	org, _ := claims["org_id"].(string)
	if org == "" {
		org, _ = claims["organization_id"].(string)
	}
	workspaces := stringsClaim(claims["workspace_ids"])
	if len(workspaces) == 0 {
		workspaces = stringsClaim(claims["workspaces"])
	}
	roles := stringsClaim(claims["roles"])
	scopes := stringsClaim(claims["scope"])
	if len(scopes) == 0 {
		scopes = stringsClaim(claims["scp"])
	}
	typ, _ := claims["principal_type"].(string)
	if typ == "" {
		typ = "user"
	}
	grant := sha256.Sum256([]byte(canonicalClaims(claims)))
	return NewPrincipal(sub, typ, org, workspaces, roles, scopes, authMethod, hex.EncodeToString(grant[:])), nil
}

func stringsClaim(v any) []string {
	switch x := v.(type) {
	case string:
		if x == "" {
			return nil
		}
		return splitSpace(x)
	case []string:
		return append([]string(nil), x...)
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
func splitSpace(s string) []string {
	var out []string
	start := 0
	for i, c := range s {
		if c == ' ' || c == '\t' {
			if start < i {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
func canonicalClaims(c map[string]any) string { b, _ := json.Marshal(c); return string(b) }
