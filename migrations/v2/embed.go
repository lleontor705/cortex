// Package v2 embeds the Cortex v2 baseline SQL migration bundle.
//
// The embedded SQL is the complete forward-only schema baseline for Cortex
// 2.0.0 (REQ-DB-001). It is applied atomically inside a single transaction
// by the migration runner in internal/migration.
package v2

import _ "embed"

// BaselineSQL is the complete v2 baseline schema DDL.
// It consolidates the final-state schema of v1 migrations 001-014 plus the
// v2 corrections (corrected type registry, valid_until, outbox, audit,
// tenant columns, cortex_meta identity).
//
//go:embed 001_init.sql
var BaselineSQL string

// ServerSQL is the PostgreSQL-only server schema migration. It is deliberately
// a separate embed so local SQLite builds never execute or import it.
//
//go:embed 100_server.sql
var ServerSQL string

// ServerIdentityGraphSQL adds request-scoped principals and closes the graph
// metadata gap without changing the immutable version 100 migration.
//
//go:embed 101_identity_graph.sql
var ServerIdentityGraphSQL string

// ServerSyncSQL adds idempotency keys and the incremental replication feed.
//
//go:embed 102_sync.sql
var ServerSyncSQL string

// ServerSyncIdentitySQL canonicalizes server-native sync identities and
// backfills the initial change feed without modifying migration 102.
//
//go:embed 103_sync_identity.sql
var ServerSyncIdentitySQL string

// HandoffReceiptsSQL is the SQLite-only follow-up migration (002) that adds
// the durable handoff receipt ledger on top of the immutable 001 baseline.
// It is applied transactionally with its own SHA-256 checksum recorded in the
// additive cortex_v2_migrations ledger; the baseline identity is unchanged.
//
//go:embed 002_handoff_receipts.sql
var HandoffReceiptsSQL string

// ServerHandoffReceiptsSQL is the PostgreSQL-only follow-up migration (104)
// that adds the tenant-isolated handoff receipt ledger on top of the
// immutable server migrations 100-103. It is ledgered with its SHA-256
// checksum in cortex_server_migrations; the line is forward-only.
//
//go:embed 104_handoff_receipts.sql
var ServerHandoffReceiptsSQL string

// ServerWorkspaceBindingSQL is the PostgreSQL-only follow-up migration
// (105) that binds observations and handoff receipts to workspaces: the
// active topic uniqueness and the receipt idempotency namespace become
// workspace-scoped, and a trigger keeps 104-era observation DML working by
// deriving the workspace from the session. It is ledgered with its SHA-256
// checksum in cortex_server_migrations; the line is forward-only.
//
//go:embed 105_workspace_binding.sql
var ServerWorkspaceBindingSQL string

// ProjectArtifactsSQL is the SQLite-only follow-up migration (003) that adds
// the Project Context artifact ledger on top of the immutable 001/002 line:
// soft-deletable artifacts (workspace defaults carry an absent project with
// per-scope conditional uniqueness), immutable revisions/events, one
// activation pointer per artifact guarded by the monotonic
// activation_revision CAS token, durable idempotency receipts that store the
// exact immutable result revision for exact replay, and transactional
// scope-coordinate storage-usage quota counters whose quota coordinates are
// frozen at insert and whose byte totals never decrease or get removed. It
// is applied transactionally with its own SHA-256 checksum
// recorded in the additive cortex_v2_migrations ledger; the baseline
// identity is unchanged and the line stays forward-only (no purge, no
// expiry, no cascade, no down path).
//
//go:embed 003_project_artifacts.sql
var ProjectArtifactsSQL string

// ServerProjectArtifactsSQL is the PostgreSQL-only follow-up migration
// (106) that adds the tenant/workspace/project-scoped Project Context
// artifact ledger on top of the immutable server migrations 100-105:
// soft-deletable artifacts with immutable revisions/events, workspace
// defaults represented by an absent project with per-scope conditional
// uniqueness, one activation pointer per artifact guarded by the monotonic
// activation_revision CAS token (the pointer is authoritative and the
// artifact mirror cannot drift), project-namespaced durable idempotency
// receipts storing the exact result revision of the same coordinate's
// artifact for exact replay, and transactional storage-usage quota counters
// whose tenant/workspace/project coordinates are frozen at insert and whose
// counters can only grow. Every table enables and forces row level
// security binding the trusted principal-derived tenant/workspace/project
// scope: cortex_bind_principal stays the three-argument entry point and is
// replaced additively to persist the bound actor and clear stale scope on
// every rebind, and cortex_bind_project_scope authorizes the binding
// against the principal's durable grants (exact workspace grants; exact,
// scoped, or wildcard project grants). Identity operations are mediated:
// provisioning, activation changes, token verification, grant reads, and
// the grant-version read-back run only through migration-owned definer
// routines that authorize the bound owner/admin caller and audit non-secret
// metadata atomically; the api_tokens lifecycle (issue, rotate, revoke) is
// mediated the same way with the stored digest derived inside SQL from the
// caller-presented one-time secret and never returned; EVERY principal
// bind — non-empty and legacy empty stored digests alike — requires the
// unforgeable token-bound v1 provenance minted by verification and
// recomputed under the locked live token (the deterministic grant digest
// is integrity metadata only, never an authenticator); and the application role holds no direct actor/grant table
// privileges at all, keeps only non-sensitive column reads (actor labels;
// api_tokens without token_digest and without any direct write), and holds
// no DELETE on any artifact table. All foreign keys RESTRICT hard deletes,
// and the line stays forward-only. The migration-role-only bootstrap
// reconciler cortex_bootstrap_service_principal closes the cold-start
// identity gap: privileged startup reconciles the service account, the
// active actor subject with its exact canonical grants, and the
// reserved-name bootstrap api_token whose stored digest is derived inside
// SQL from the configured bearer, atomically and idempotently, with
// non-secret audit evidence and EXECUTE limited to cortex_migration. It is
// ledgered with its SHA-256 checksum in cortex_server_migrations.
//
//go:embed 106_project_artifacts.sql
var ServerProjectArtifactsSQL string

// ServerWorkspaceSyncSQL is the PostgreSQL-only follow-up migration (107)
// that makes the replication identities workspace-safe after the T01
// sibling-workspace exploit proof (SEC-03): prompts and edges gain a
// workspace bound through their durable chains (prompt -> session,
// edge -> from-observation) with fail-closed backfills, BEFORE binding
// triggers that derive the workspace and reject explicit mismatches, NOT
// NULL composite tenant/workspace foreign keys, and edge endpoints that
// must resolve inside one shared workspace. The tenant-wide observation,
// prompt, and edge client_id unique indexes from 102 are swapped for
// workspace-scoped replacements so identical sibling-workspace client IDs
// coexist while same-workspace duplicates keep failing closed; duplicate,
// orphan, and cross-workspace preflights run before any mutating statement
// and abort without merging or dropping anything. It is applied
// transactionally with its SHA-256 checksum recorded in
// cortex_server_migrations; the line stays forward-only (no down path).
//
//go:embed 107_workspace_sync.sql
var ServerWorkspaceSyncSQL string

// ServerPrincipalRWGatingSQL is the PostgreSQL-only follow-up migration
// (108) that installs the canonical SRW principal read/write gating proven
// by the T01 lock spike: ONE transaction-scoped advisory key namespace
// (cortex_principal_key over 'cortex:principal:' || tenant || ':' || actor)
// shared by verify/bind readers (pg_advisory_xact_lock_shared before their
// FOR SHARE identity re-reads) and every identity invalidator
// (provisioning, activation change, token issue/rotate/revoke, bootstrap
// reconciliation take pg_advisory_xact_lock before any identity row lock,
// after a lock-free lookup used only to derive the key). Token-ID writers
// re-read and revalidate the locked token and subject under the gate and
// reject drift; verification drops the same-token FOR UPDATE serialization
// and the unconditional last_used_at write, replacing them with a
// throttled, monotonic, best-effort telemetry that never waits on a peer
// verifier row lock (dedicated usage advisory + NOWAIT probe) and never
// fails authentication. Only transaction-scoped advisory calls are used, so
// transaction-mode poolers stay safe. The migration is purely additive
// (CREATE OR REPLACE plus owner/ACL reassertion; no destructive operation)
// and is ledgered with its SHA-256 checksum in cortex_server_migrations;
// the line stays forward-only (no down path).
//
//go:embed 108_principal_rw_gating.sql
var ServerPrincipalRWGatingSQL string

// ServerScopedCodeIndexSQL is PostgreSQL migration 109. It introduces new
// tenant/workspace/project-scoped AST tables with forced RLS and leaves any
// legacy project-only index read-denied and unmodified for recovery.
//
//go:embed 109_scoped_code_index.sql
var ServerScopedCodeIndexSQL string

// ServerVerifiedRateLimitTierSQL is PostgreSQL migration 110. It exposes the
// api_tokens rate_limit_tier only as part of the already-verified principal;
// request metadata cannot influence the selected quota tier.
//
//go:embed 110_verified_rate_limit_tier.sql
var ServerVerifiedRateLimitTierSQL string

// ServerMultiTenantVerifierSQL is PostgreSQL migration 111. It adds the
// SaaS data-plane token verifier which derives the tenant from a validated
// bearer instead of accepting a client-selected tenant identifier.
//
//go:embed 111_multi_tenant_verifier.sql
var ServerMultiTenantVerifierSQL string
