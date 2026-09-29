package domain

// Canonical fields of the synthetic single-tenant principal (REQ-SQ-SEC-008).
// They are constants rather than caller inputs so the server request plane and
// the local serve plane cannot drift field by field as the principal shape
// grows: adding a grant field here updates every site at once.
const (
	SyntheticPrincipalType          = "service_account"
	SyntheticPrincipalRole          = "owner"
	SyntheticPrincipalGrantWildcard = "*"
	SyntheticPrincipalRateLimitTier = "standard"
)

// SyntheticPrincipalParams carries the per-plane identity and provenance inputs
// for NewSyntheticPrincipal. The authorization grants themselves are NOT
// parameters: the owner role and the wildcard project/classification grants are
// canonical to a single-tenant synthetic identity.
type SyntheticPrincipalParams struct {
	Subject      string
	OrgID        string
	WorkspaceID  string
	AuthMethod   string
	GrantDigest  string
	GrantVersion int64
}

// NewSyntheticPrincipal assembles the canonical synthetic service-account
// principal shared by every plane that authenticates a single configured
// identity (REQ-SQ-SEC-008). Callers vary only identity, provenance, and the
// authentication method; the grant shape is fixed here.
func NewSyntheticPrincipal(p SyntheticPrincipalParams) Principal {
	return Principal{
		Subject:                 p.Subject,
		Type:                    SyntheticPrincipalType,
		OrgID:                   p.OrgID,
		WorkspaceIDs:            []string{p.WorkspaceID},
		Roles:                   []string{SyntheticPrincipalRole},
		AuthMethod:              p.AuthMethod,
		GrantDigest:             p.GrantDigest,
		GrantVersion:            p.GrantVersion,
		RateLimitTier:           SyntheticPrincipalRateLimitTier,
		ProjectIDs:              []string{SyntheticPrincipalGrantWildcard},
		ClassificationClearance: []string{SyntheticPrincipalGrantWildcard},
	}
}
