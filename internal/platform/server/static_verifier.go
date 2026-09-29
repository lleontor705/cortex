package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

// errInvalidBearer is deliberately opaque: every rejection path — mismatched,
// empty, or whitespace-padded secret — returns this same sentinel, so the
// request plane maps it to the standard 401 envelope without echoing any
// credential material.
var errInvalidBearer = errors.New("server: invalid bearer")

// staticGrantDigest is the synthetic principal's provenance constant. It is
// non-empty and paired with the historical grant version so the store and
// audit composition contracts (postgres.validateAuthorizedContext,
// postgres.NewAuditSink) are satisfied even when the deprecated configured
// grant_digest input is absent.
const staticGrantDigest = "static-single-tenant"

// staticGrantVersion is the deprecated configured grant_version default: a
// zero value means the operator never set the compatibility input, so the
// historical version-1 principal shape is preserved. The composition replaces
// it with the bootstrapped actor row's real version before request traffic.
const staticGrantVersion int64 = 1

// staticBearerVerifier is the single-tenant request identity source: it
// compares the presented secret to the configured bearer in constant time and
// returns one synthetic principal assembled once from configuration. It holds
// no database capability, so bearer verification has no configured-token
// bypass and no per-request tenant or workspace derivation (REQ-SH-001).
type staticBearerVerifier struct {
	token     string
	principal domain.Principal
}

var _ principalVerifier = staticBearerVerifier{}

// newStaticBearerVerifier assembles the verifier and its synthetic principal
// from trusted administrator configuration. It fails closed, naming the
// missing key, when principal_subject, tenant_id, or workspace_id is empty, and
// on a negative grant_version, so the composition aborts before any pool or
// handler exists. The assembled grant version is the configured (or composed)
// value: hardcoding it would strand every request behind a stale grant after a
// canonical-grant reconcile bumps the persisted version.
func newStaticBearerVerifier(cfg config.Config) (staticBearerVerifier, error) {
	for _, required := range []struct {
		key   string
		value string
	}{
		{"tenant_id", cfg.Server.TenantID},
		{"workspace_id", cfg.Server.WorkspaceID},
		{"principal_subject", cfg.Server.PrincipalSubject},
	} {
		if required.value == "" {
			return staticBearerVerifier{}, fmt.Errorf("server: %s is required", required.key)
		}
	}
	grantDigest := cfg.Server.GrantDigest
	if grantDigest == "" {
		grantDigest = staticGrantDigest
	}
	// The composition overwrites this with the bootstrapped actor row's real
	// grant_version before assembling request traffic. A negative value is
	// never a valid principal grant version and fails closed; a zero value is
	// the deprecated configured default and keeps the historical shape.
	grantVersion := cfg.Server.GrantVersion
	if grantVersion < 0 {
		return staticBearerVerifier{}, fmt.Errorf("server: grant_version %d is not a valid principal grant version", grantVersion)
	}
	if grantVersion == 0 {
		grantVersion = staticGrantVersion
	}
	return staticBearerVerifier{
		token: cfg.HTTP.Token,
		principal: domain.Principal{
			Subject:                 cfg.Server.PrincipalSubject,
			Type:                    "service_account",
			OrgID:                   cfg.Server.TenantID,
			WorkspaceIDs:            []string{cfg.Server.WorkspaceID},
			Roles:                   []string{string(authz.RoleOwner)},
			AuthMethod:              "static",
			GrantDigest:             grantDigest,
			GrantVersion:            grantVersion,
			RateLimitTier:           "standard",
			ProjectIDs:              []string{"*"},
			ClassificationClearance: []string{"*"},
		},
	}, nil
}

// VerifyToken compares the presented secret to the configured bearer with
// subtle.ConstantTimeCompare. The secret is never trimmed: the configured
// bearer is canonical at startup (validateBearerToken), so whitespace-padded
// presentations fail closed exactly like any other byte difference.
func (v staticBearerVerifier) VerifyToken(_ context.Context, secret, _ string) (domain.Principal, error) {
	if subtle.ConstantTimeCompare([]byte(secret), []byte(v.token)) != 1 {
		return domain.Principal{}, errInvalidBearer
	}
	return v.principal, nil
}
