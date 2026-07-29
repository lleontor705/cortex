package authz

import (
	"context"
	"errors"

	"github.com/lleontor705/cortex/internal/domain"
)

var ErrForbidden = errors.New("forbidden")

// Enforce is the mandatory use-case seam. Repositories must not be called
// until this check succeeds. The stable reason is safe for audit/metrics and
// never includes a resource's existence or contents.
func Enforce(ctx context.Context, a Authorizer, req Request) error {
	if a == nil {
		return errors.New(DenyRole)
	}
	if audited, ok := a.(AuditedAuthorizer); ok {
		d, err := audited.AuthorizeWithAudit(ctx, req)
		if err != nil {
			return err
		}
		if d.Allowed {
			return nil
		}
		return errors.New(d.Reason)
	}
	d := a.Authorize(ctx, req)
	if d.Allowed {
		return nil
	}
	return errors.New(d.Reason)
}

type authorizedContextKey struct{}
type AuthorizedContext struct {
	Principal   domain.Principal
	Tenant      domain.TenantContext
	GrantDigest string
}

// NewAuthorizedContext binds only verified principal data to a request. A
// caller cannot construct a server context by supplying an arbitrary tenant.
func NewAuthorizedContext(ctx context.Context, a Authorizer, req Request) (AuthorizedContext, error) {
	if err := Enforce(ctx, a, req); err != nil {
		return AuthorizedContext{}, err
	}
	t, err := DeriveTenantContext(req.Principal, req.Tenant)
	if err != nil {
		return AuthorizedContext{}, err
	}
	return AuthorizedContext{Principal: req.Principal, Tenant: t, GrantDigest: req.Principal.GrantDigest}, nil
}
func WithAuthorizedContext(ctx context.Context, a AuthorizedContext) context.Context {
	return context.WithValue(ctx, authorizedContextKey{}, a)
}
func AuthorizedFromContext(ctx context.Context) (AuthorizedContext, bool) {
	v, ok := ctx.Value(authorizedContextKey{}).(AuthorizedContext)
	return v, ok
}
