package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

// recordingBindTx captures the binder statement a Store emits. Only Exec is
// ever reached by bind; the embedded nil interface satisfies the remaining
// pgx.Tx methods so the assertion stays a pure unit check with no pool.
type recordingBindTx struct {
	pgx.Tx
	sql  string
	args []any
}

func (t *recordingBindTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.sql, t.args = sql, args
	return pgconn.CommandTag{}, nil
}

const (
	bindTenant = "11111111-1111-1111-1111-111111111111"
	bindActor  = "22222222-2222-2222-2222-222222222222"
	// bindGrant mirrors the shape an operator provisions as the actor's grant
	// digest and repeats in server.grant_digest.
	bindGrant = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// staticVector is the pinned static binder argument for bindTenant,
	// bindActor, bindGrant at version 1. It fails closed if the Go-side
	// message/key derivation drifts from the migration-112 recomputation.
	staticVector = "static:c01aab2f8dbf28526ae38f180710205c82f46c07db9ef11912c1a914943b5d03"
)

func authorizedBindStore(grant string, version int64) *Store {
	return &Store{
		authorized:   true,
		tenant:       &domain.TenantContext{TenantID: bindTenant},
		principal:    domain.Principal{Subject: bindActor, OrgID: bindTenant, GrantVersion: version},
		grantDigest:  grant,
		grantVersion: version,
	}
}

// TestBindStaticProvenanceBindsSyntheticPrincipal proves the configuration-
// derived principal reaches cortex_bind_principal with a well-formed static
// argument and the exact tenant/actor/version arguments, so the RLS context
// install is issued instead of failing closed on an unverifiable constant.
func TestBindStaticProvenanceBindsSyntheticPrincipal(t *testing.T) {
	store := authorizedBindStore(bindGrant, 1)
	tx := &recordingBindTx{}
	if err := store.bind(context.Background(), tx); err != nil {
		t.Fatalf("bind() = %v, want nil", err)
	}
	if !strings.Contains(tx.sql, "public.cortex_bind_principal($1::uuid,$2::text,$3::bigint)") {
		t.Fatalf("bind statement = %q, want the three-argument binder", tx.sql)
	}
	if len(tx.args) != 3 {
		t.Fatalf("bind args = %d, want 3", len(tx.args))
	}
	if tx.args[0] != bindActor || tx.args[2] != int64(1) {
		t.Fatalf("bind actor/version args = %v/%v, want %s/1", tx.args[0], tx.args[2], bindActor)
	}
	binderArg, ok := tx.args[1].(string)
	if !ok {
		t.Fatalf("static binder argument type = %T, want string", tx.args[1])
	}
	if binderArg != staticVector {
		t.Fatalf("static binder argument mismatch: %d characters, want the pinned vector of %d", len(binderArg), len(staticVector))
	}
}

// TestBindProvenanceBindsEveryCoordinate is the mutation guard for the static
// contract: perturbing the tenant, actor, grant version, or grant constant
// must change the emitted argument, so a replayed value cannot satisfy the
// database binder for a different coordinate set.
func TestBindProvenanceBindsEveryCoordinate(t *testing.T) {
	baseline, err := authorizedBindStore(bindGrant, 1).bindProvenance()
	if err != nil {
		t.Fatalf("bindProvenance() = %v", err)
	}
	variants := map[string]*Store{
		"tenant":  {tenant: &domain.TenantContext{TenantID: "33333333-3333-3333-3333-333333333333"}, principal: domain.Principal{Subject: bindActor, OrgID: "33333333-3333-3333-3333-333333333333", GrantVersion: 1}, grantDigest: bindGrant, grantVersion: 1},
		"actor":   {tenant: &domain.TenantContext{TenantID: bindTenant}, principal: domain.Principal{Subject: "44444444-4444-4444-4444-444444444444", OrgID: bindTenant, GrantVersion: 1}, grantDigest: bindGrant, grantVersion: 1},
		"version": {tenant: &domain.TenantContext{TenantID: bindTenant}, principal: domain.Principal{Subject: bindActor, OrgID: bindTenant, GrantVersion: 2}, grantDigest: bindGrant, grantVersion: 2},
		"grant":   {tenant: &domain.TenantContext{TenantID: bindTenant}, principal: domain.Principal{Subject: bindActor, OrgID: bindTenant, GrantVersion: 1}, grantDigest: strings.Repeat("b", 64), grantVersion: 1},
	}
	for name, variant := range variants {
		got, err := variant.bindProvenance()
		if err != nil {
			t.Fatalf("%s variant bindProvenance() = %v", name, err)
		}
		if got == baseline {
			t.Errorf("%s variant reused the baseline binder argument; the value must bind every coordinate", name)
		}
	}
}

// TestBindProvenancePassesThroughMintedFormats pins that an already-canonical
// argument is never re-derived: the v1 token-bound proof minted by
// verification and a canonical static value both reach the binder untouched.
func TestBindProvenancePassesThroughMintedFormats(t *testing.T) {
	minted := []string{
		"v1:22222222-2222-2222-2222-222222222222:" + strings.Repeat("c", 64),
		"static:" + strings.Repeat("d", 64),
	}
	for i, grant := range minted {
		got, err := authorizedBindStore(grant, 3).bindProvenance()
		if err != nil {
			t.Fatalf("bindProvenance(minted %d) = %v", i, err)
		}
		if got != grant {
			t.Errorf("minted argument %d was rewritten instead of passed through", i)
		}
	}
}

func TestBindFailsClosedWithoutRequiredAuthority(t *testing.T) {
	ctx := context.Background()
	noGrant := &Store{authorized: true, tenant: &domain.TenantContext{TenantID: bindTenant}, principal: domain.Principal{Subject: bindActor, OrgID: bindTenant}, grantVersion: 1}
	if err := noGrant.bind(ctx, &recordingBindTx{}); !errors.Is(err, ErrGrantDigestRequired) {
		t.Fatalf("empty grant error = %v, want %v", err, ErrGrantDigestRequired)
	}
	opaque := authorizedBindStore(bindGrant, 1)
	opaque.principal.Subject = "opaque-subject"
	if err := opaque.bind(ctx, &recordingBindTx{}); !errors.Is(err, ErrPrincipalRequired) {
		t.Fatalf("opaque subject error = %v, want %v", err, ErrPrincipalRequired)
	}
	if err := (&Store{}).bind(ctx, &recordingBindTx{}); !errors.Is(err, ErrAuthorizedStoreRequired) {
		t.Fatalf("unauthorized store error = %v, want %v", err, ErrAuthorizedStoreRequired)
	}
}
