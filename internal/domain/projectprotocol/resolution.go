package projectprotocol

import (
	"sort"
	"time"
)

// Resolution is the deterministic outcome of project-over-workspace
// resolution over active artifact summaries.
type Resolution struct {
	// Effective holds the winning artifact per (kind,key), ordered by kind
	// then key. Its length never exceeds MaxEffectiveArtifacts.
	Effective []ResolvableArtifact `json:"effective"`
	// Shadowed records workspace-default artifacts overridden by a project
	// artifact with the same (kind,key), ordered by kind then key.
	Shadowed []ShadowedArtifact `json:"shadowed"`
	// Conflicts records same-scope key collisions between distinct artifact
	// records (an integrity signal; keys are unique per scope by contract).
	Conflicts []KeyConflict `json:"conflicts"`
}

// ShadowedArtifact is a workspace-default artifact overridden by a project
// artifact for the same (kind,key).
type ShadowedArtifact struct {
	ArtifactID   string `json:"artifact_id"`
	Kind         Kind   `json:"kind"`
	Key          string `json:"key"`
	Revision     int64  `json:"revision"`
	ShadowedByID string `json:"shadowed_by_id"`
}

// Validate checks the shadowed-entry invariants. It does NOT perform the
// bundle-level crosscheck against the effective set; Protocol.Validate does.
func (s ShadowedArtifact) Validate() error {
	if s.ArtifactID == "" || len(s.ArtifactID) > MaxArtifactIDBytes {
		return &Error{Code: ErrCodeValidation, Message: "artifact id must be 1..128 bytes"}
	}
	if _, err := ParseKind(string(s.Kind)); err != nil {
		return err
	}
	if err := ValidateKey(s.Key); err != nil {
		return err
	}
	if s.Revision < 1 {
		return &Error{Code: ErrCodeValidation, Message: "revision must be at least 1"}
	}
	if s.ShadowedByID == "" || len(s.ShadowedByID) > MaxArtifactIDBytes {
		return &Error{Code: ErrCodeValidation, Message: "shadowed_by_id must be 1..128 bytes"}
	}
	if s.ShadowedByID == s.ArtifactID {
		return &Error{Code: ErrCodeValidation, Message: "an artifact cannot shadow itself"}
	}
	return nil
}

// KeyConflict records two or more distinct artifact records sharing one
// (kind,key) within the same scope. The resolver still picks a deterministic
// winner (precedence descending, then artifact id ascending) so resolution
// stays total; consumers surface the conflict for operator review.
type KeyConflict struct {
	Kind         Kind     `json:"kind"`
	Key          string   `json:"key"`
	Scope        Scope    `json:"scope"`
	ArtifactIDs  []string `json:"artifact_ids"`
	ResolvedByID string   `json:"resolved_by_id"`
}

// Validate checks the conflict-entry invariants: at least two distinct
// artifact ids, sorted and unique, with the deterministic winner among them.
// It does NOT perform the bundle-level crosscheck; Protocol.Validate does.
func (c KeyConflict) Validate() error {
	if _, err := ParseKind(string(c.Kind)); err != nil {
		return err
	}
	if err := ValidateKey(c.Key); err != nil {
		return err
	}
	if !c.Scope.Valid() {
		return &Error{Code: ErrCodeValidation, Message: "scope must be workspace_default or project"}
	}
	if len(c.ArtifactIDs) < 2 {
		return &Error{Code: ErrCodeValidation, Message: "conflict requires at least two distinct artifact ids"}
	}
	for i, id := range c.ArtifactIDs {
		if id == "" || len(id) > MaxArtifactIDBytes {
			return &Error{Code: ErrCodeValidation, Message: "artifact id must be 1..128 bytes"}
		}
		if i > 0 && c.ArtifactIDs[i-1] >= id {
			return &Error{Code: ErrCodeValidation, Message: "conflict artifact ids must be sorted and unique"}
		}
	}
	if c.ResolvedByID == "" || len(c.ResolvedByID) > MaxArtifactIDBytes {
		return &Error{Code: ErrCodeValidation, Message: "resolved_by_id must be 1..128 bytes"}
	}
	found := false
	for _, id := range c.ArtifactIDs {
		if id == c.ResolvedByID {
			found = true
			break
		}
	}
	if !found {
		return &Error{Code: ErrCodeValidation, Message: "resolved_by_id must be one of the conflicted artifact ids"}
	}
	return nil
}

type artifactKey struct {
	kind Kind
	key  string
}

// Resolve computes the effective protocol summary set from active artifact
// candidates (REQ-ART-004).
//
// Determinism contract:
//   - for each (kind,key), a project-scope artifact wins over a
//     workspace-default artifact; the loser is recorded in Shadowed;
//   - within one scope, distinct artifact records sharing a (kind,key) are
//     recorded in Conflicts with the deterministic winner chosen by
//     precedence descending then artifact id ascending;
//   - Effective is sorted by kind then key;
//   - the effective count is capped at MaxEffectiveArtifacts: reaching
//     limit+1 aborts with effective_artifact_limit_exceeded BEFORE any
//     content is fetched (candidates carry no content by construction).
//
// Candidate identity integrity: an artifact id appears at most once per
// snapshot. Repeated candidates whose entire summary is identical are exact
// duplicate rows and collapse silently (the outcome is unchanged); a
// repeated id with ANY differing field (key, kind, scope, precedence,
// revision, digest) is inconsistent data and rejected outright. This
// guarantees the returned provenance — conflict id lists and shadowed
// references — is always internally sorted, unique and crosscheck-valid for
// Protocol.Validate.
func Resolve(candidates []ResolvableArtifact) (Resolution, error) {
	seenIDs := make(map[string]ResolvableArtifact, len(candidates))
	groups := make(map[artifactKey]map[Scope][]ResolvableArtifact)
	order := make([]artifactKey, 0, len(candidates))
	for _, c := range candidates {
		if err := c.Validate(); err != nil {
			return Resolution{}, err
		}
		if prev, dup := seenIDs[c.ArtifactID]; dup {
			if prev == c {
				continue // exact duplicate row: collapse
			}
			return Resolution{}, &Error{Code: ErrCodeValidation, Message: "duplicate candidate artifact id with inconsistent data"}
		}
		seenIDs[c.ArtifactID] = c
		ak := artifactKey{kind: c.Kind, key: c.Key}
		if groups[ak] == nil {
			groups[ak] = make(map[Scope][]ResolvableArtifact)
			order = append(order, ak)
		}
		groups[ak][c.Scope] = append(groups[ak][c.Scope], c)
	}

	var res Resolution
	count := 0
	for _, ak := range order {
		scoped := groups[ak]
		var projectWinner, workspaceWinner *ResolvableArtifact
		if list := scoped[ScopeProject]; len(list) > 0 {
			w := deterministicWinner(list)
			projectWinner = &w
			if len(list) > 1 {
				res.Conflicts = append(res.Conflicts, buildConflict(ak, ScopeProject, list, w))
			}
		}
		if list := scoped[ScopeWorkspaceDefault]; len(list) > 0 {
			w := deterministicWinner(list)
			workspaceWinner = &w
			if len(list) > 1 {
				res.Conflicts = append(res.Conflicts, buildConflict(ak, ScopeWorkspaceDefault, list, w))
			}
		}
		winner := projectWinner
		if winner == nil {
			winner = workspaceWinner
		} else if workspaceWinner != nil {
			res.Shadowed = append(res.Shadowed, ShadowedArtifact{
				ArtifactID:   workspaceWinner.ArtifactID,
				Kind:         workspaceWinner.Kind,
				Key:          workspaceWinner.Key,
				Revision:     workspaceWinner.Revision,
				ShadowedByID: projectWinner.ArtifactID,
			})
		}
		if winner == nil {
			continue
		}
		count++
		if count > MaxEffectiveArtifacts {
			// Abort before sorting/materializing further: reject without a
			// subset result (REQ-LIMIT-002).
			return Resolution{}, ErrEffectiveLimit
		}
		res.Effective = append(res.Effective, *winner)
	}

	sort.Slice(res.Effective, func(i, j int) bool {
		if res.Effective[i].Kind != res.Effective[j].Kind {
			return res.Effective[i].Kind < res.Effective[j].Kind
		}
		return res.Effective[i].Key < res.Effective[j].Key
	})
	sort.Slice(res.Shadowed, func(i, j int) bool {
		if res.Shadowed[i].Kind != res.Shadowed[j].Kind {
			return res.Shadowed[i].Kind < res.Shadowed[j].Kind
		}
		return res.Shadowed[i].Key < res.Shadowed[j].Key
	})
	// Conflicts are part of the deterministic resolution output: their order
	// MUST NOT depend on candidate input order.
	sort.Slice(res.Conflicts, func(i, j int) bool {
		if res.Conflicts[i].Kind != res.Conflicts[j].Kind {
			return res.Conflicts[i].Kind < res.Conflicts[j].Kind
		}
		if res.Conflicts[i].Key != res.Conflicts[j].Key {
			return res.Conflicts[i].Key < res.Conflicts[j].Key
		}
		return res.Conflicts[i].Scope < res.Conflicts[j].Scope
	})
	return res, nil
}

func deterministicWinner(list []ResolvableArtifact) ResolvableArtifact {
	winner := list[0]
	for _, c := range list[1:] {
		if c.Precedence > winner.Precedence ||
			(c.Precedence == winner.Precedence && c.ArtifactID < winner.ArtifactID) {
			winner = c
		}
	}
	return winner
}

func buildConflict(ak artifactKey, scope Scope, list []ResolvableArtifact, winner ResolvableArtifact) KeyConflict {
	ids := make([]string, 0, len(list))
	for _, c := range list {
		ids = append(ids, c.ArtifactID)
	}
	sort.Strings(ids)
	return KeyConflict{
		Kind:         ak.kind,
		Key:          ak.key,
		Scope:        scope,
		ArtifactIDs:  ids,
		ResolvedByID: winner.ArtifactID,
	}
}

// ProtocolArtifact is one fully materialized artifact of the effective
// protocol bundle, carrying content and canonical metadata.
type ProtocolArtifact struct {
	ArtifactID  string         `json:"artifact_id"`
	Kind        Kind           `json:"kind"`
	Key         string         `json:"key"`
	Title       string         `json:"title"`
	Revision    int64          `json:"revision"`
	SourceScope Scope          `json:"source_scope"`
	ContentType string         `json:"content_type"`
	Content     string         `json:"content"`
	Metadata    map[string]any `json:"metadata"`
	Digest      string         `json:"digest"`
	Precedence  int32          `json:"precedence"`
}

// Validate checks the bundle artifact invariants.
func (p ProtocolArtifact) Validate() error {
	if p.ArtifactID == "" || len(p.ArtifactID) > MaxArtifactIDBytes {
		return &Error{Code: ErrCodeValidation, Message: "artifact id must be 1..128 bytes"}
	}
	if _, err := ParseKind(string(p.Kind)); err != nil {
		return err
	}
	if err := ValidateKey(p.Key); err != nil {
		return err
	}
	if err := ValidateTitle(p.Title); err != nil {
		return err
	}
	if p.Revision < 1 {
		return &Error{Code: ErrCodeValidation, Message: "revision must be at least 1"}
	}
	if !p.SourceScope.Valid() {
		return &Error{Code: ErrCodeValidation, Message: "scope must be workspace_default or project"}
	}
	if p.ContentType != ContentTypeMarkdown {
		return &Error{Code: ErrCodeValidation, Message: "content_type must be text/markdown"}
	}
	if err := ValidateContent(p.Content); err != nil {
		return err
	}
	if _, err := CanonicalizeMetadataMap(p.Metadata); err != nil {
		return err
	}
	return nil
}

// ProviderBinding is the sanitized, non-secret project->provider summary
// carried by the effective protocol (REQ-ART-004: "provider_binding summary
// no secreto"). It is a versioned reference into the operator provider
// catalog plus its reindex state; it MUST NEVER contain credentials, tokens
// or raw catalog configuration.
type ProviderBinding struct {
	ProviderID      string `json:"provider_id"`
	Model           string `json:"model"`
	Dimension       int    `json:"dimension"`
	BindingRevision int64  `json:"binding_revision"`
	Generation      int64  `json:"generation"`
	ReindexState    string `json:"reindex_state"`
	Health          string `json:"health"`
}

// Health values reuse the port health vocabulary.
const (
	ProviderHealthHealthy   = "healthy"
	ProviderHealthDegraded  = "degraded"
	ProviderHealthUnhealthy = "unhealthy"
)

// validateSanitizedToken enforces the no-secret shape shared by binding
// fields: 1..maxBytes printable ASCII without spaces or control characters,
// so nothing secret-shaped or control-laden can ride along.
func validateSanitizedToken(value string, maxBytes int, field string) error {
	if value == "" {
		return &Error{Code: ErrCodeValidation, Message: field + " must not be empty"}
	}
	if len(value) > maxBytes {
		return &Error{Code: ErrCodeValidation, Message: field + " exceeds maximum length", Limit: int64(maxBytes)}
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return &Error{Code: ErrCodeValidation, Message: field + " must be printable ASCII without spaces"}
		}
	}
	return nil
}

// Validate checks the sanitized provider binding invariants. Every field is
// bounded and printable-ASCII; no secret material can be represented.
func (b ProviderBinding) Validate() error {
	if err := validateSanitizedToken(b.ProviderID, 128, "provider_id"); err != nil {
		return err
	}
	if err := validateSanitizedToken(b.Model, 200, "model"); err != nil {
		return err
	}
	if b.Dimension < 1 {
		return &Error{Code: ErrCodeValidation, Message: "provider binding dimension must be at least 1"}
	}
	if b.BindingRevision < 1 {
		return &Error{Code: ErrCodeValidation, Message: "provider binding revision must be at least 1"}
	}
	if b.Generation < 1 {
		return &Error{Code: ErrCodeValidation, Message: "provider binding generation must be at least 1"}
	}
	if err := validateSanitizedToken(b.ReindexState, 32, "reindex_state"); err != nil {
		return err
	}
	switch b.Health {
	case ProviderHealthHealthy, ProviderHealthDegraded, ProviderHealthUnhealthy:
	default:
		return &Error{Code: ErrCodeValidation, Message: "provider binding health must be healthy, degraded or unhealthy"}
	}
	return nil
}

// Protocol is the deterministic effective protocol snapshot for one project.
// ProtocolRevision is the store-supplied opaque monotonic snapshot
// identifier; GeneratedAt is its creation time.
type Protocol struct {
	Project          string             `json:"project"`
	ProtocolRevision string             `json:"protocol_revision"`
	GeneratedAt      time.Time          `json:"generated_at"`
	Artifacts        []ProtocolArtifact `json:"artifacts"`
	Shadowed         []ShadowedArtifact `json:"shadowed"`
	Conflicts        []KeyConflict      `json:"conflicts"`
	// ProviderBinding is the sanitized project->provider reference summary,
	// or nil when the project has no binding. It participates in bundle
	// validation, hashing and size accounting (REQ-LIMIT-003).
	ProviderBinding *ProviderBinding `json:"provider_binding"`
}

// Bundle is the canonical bounded encoding result of a protocol snapshot.
type Bundle struct {
	// Canonical is the canonical JSON encoding of the protocol (bounded by
	// MaxProtocolBundleBytes) or nil when the bundle exceeded the limit.
	Canonical []byte
	// ETag is the opaque quoted entity tag of the canonical bytes.
	ETag string
	// Digest is the "sha256:<hex>" digest of the canonical bytes.
	Digest string
	// BytesLen is the canonical byte count (0 on abort).
	BytesLen int
}

// EncodeBundle canonicalizes the protocol through the counting writer and
// hashing pipeline (REQ-LIMIT-003): the snapshot is fully validated BEFORE
// any byte is emitted (per-artifact invariants, the effective count cap, the
// sanitized provider binding, and the sorted/unique/crosschecked
// shadowed+conflict provenance), then streamed into the 4 MiB bounded
// buffer. Exactly 4 MiB is accepted; one byte more aborts with
// protocol_too_large and no partial canonical bytes are returned.
//
// The canonical bytes deliberately EXCLUDE generated_at: the ETag/digest are
// content-stable across generation time of the same snapshot
// (protocol_revision identifies the snapshot; conditional requests compare
// content, not wall clocks).
func (p *Protocol) EncodeBundle() (Bundle, error) {
	if err := p.Validate(); err != nil {
		return Bundle{}, err
	}
	root := map[string]any{
		"artifacts":         protocolArtifactsAsAny(p.Artifacts),
		"conflicts":         conflictsAsAny(p.Conflicts),
		"project":           p.Project,
		"provider_binding":  providerBindingAsAny(p.ProviderBinding),
		"protocol_revision": p.ProtocolRevision,
		"shadowed":          shadowedAsAny(p.Shadowed),
	}
	lw := NewLimitWriter(MaxProtocolBundleBytes)
	if err := writeCanonical(lw, root); err != nil {
		if lw.Failed() {
			return Bundle{}, ErrProtocolTooLarge
		}
		return Bundle{}, AsError(err)
	}
	out := lw.Bytes()
	if out == nil {
		return Bundle{}, ErrProtocolTooLarge
	}
	return Bundle{
		Canonical: out,
		ETag:      lw.ETag(),
		Digest:    lw.Digest(),
		BytesLen:  int(lw.Count()),
	}, nil
}

// providerBindingAsAny maps the sanitized binding into its canonical bundle
// form; nil becomes JSON null.
func providerBindingAsAny(b *ProviderBinding) any {
	if b == nil {
		return nil
	}
	return map[string]any{
		"binding_revision": b.BindingRevision,
		"dimension":        b.Dimension,
		"generation":       b.Generation,
		"health":           b.Health,
		"model":            b.Model,
		"provider_id":      b.ProviderID,
		"reindex_state":    b.ReindexState,
	}
}

// Validate checks the snapshot invariants: the effective count cap, the
// sanitized provider binding, per-artifact semantics, strictly sorted and
// unique effective/(kind,key) ordering, and the FULL provenance contract on
// shadowed/conflicts — every entry semantically valid, sorted, unique, and
// crosschecked against the effective set so bundle bytes cannot carry
// provenance the snapshot state does not support (REQ-LIMIT-003).
func (p *Protocol) Validate() error {
	if p.ProtocolRevision == "" {
		return &Error{Code: ErrCodeValidation, Message: "protocol revision must be present"}
	}
	if len(p.Artifacts) > MaxEffectiveArtifacts {
		return ErrEffectiveLimit
	}
	if p.ProviderBinding != nil {
		if err := p.ProviderBinding.Validate(); err != nil {
			return err
		}
	}
	effective := make(map[artifactKey]ProtocolArtifact, len(p.Artifacts))
	for i := range p.Artifacts {
		if err := p.Artifacts[i].Validate(); err != nil {
			return err
		}
		ak := artifactKey{kind: p.Artifacts[i].Kind, key: p.Artifacts[i].Key}
		if _, dup := effective[ak]; dup {
			return &Error{Code: ErrCodeValidation, Message: "duplicate effective artifact key"}
		}
		if i > 0 {
			prev := p.Artifacts[i-1]
			if prev.Kind > p.Artifacts[i].Kind ||
				(prev.Kind == p.Artifacts[i].Kind && prev.Key >= p.Artifacts[i].Key) {
				return &Error{Code: ErrCodeValidation, Message: "effective artifacts must be sorted and unique by (kind,key)"}
			}
		}
		effective[ak] = p.Artifacts[i]
	}
	if err := p.validateShadowedProvenance(effective); err != nil {
		return err
	}
	return p.validateConflictProvenance(effective)
}

// validateShadowedProvenance enforces the shadowed contract: entries are
// semantically valid, sorted and unique by (kind,key), and each is
// crosschecked against the effective set — the shadowing project artifact
// MUST be the effective winner for that (kind,key), and the shadowed
// workspace artifact MUST NOT be it.
func (p *Protocol) validateShadowedProvenance(effective map[artifactKey]ProtocolArtifact) error {
	for i := range p.Shadowed {
		s := p.Shadowed[i]
		if err := s.Validate(); err != nil {
			return err
		}
		if i > 0 {
			prev := p.Shadowed[i-1]
			if prev.Kind > s.Kind || (prev.Kind == s.Kind && prev.Key >= s.Key) {
				return &Error{Code: ErrCodeValidation, Message: "shadowed entries must be sorted and unique by (kind,key)"}
			}
		}
		winner, ok := effective[artifactKey{kind: s.Kind, key: s.Key}]
		if !ok {
			return &Error{Code: ErrCodeValidation, Message: "shadowed entry has no effective artifact for its (kind,key)"}
		}
		if winner.SourceScope != ScopeProject {
			return &Error{Code: ErrCodeValidation, Message: "shadowed entry must be overridden by a project-scope artifact"}
		}
		if winner.ArtifactID != s.ShadowedByID {
			return &Error{Code: ErrCodeValidation, Message: "shadowed_by_id does not match the effective winner for its (kind,key)"}
		}
		if winner.ArtifactID == s.ArtifactID {
			return &Error{Code: ErrCodeValidation, Message: "shadowed artifact cannot be the effective winner"}
		}
	}
	return nil
}

// validateConflictProvenance enforces the conflict contract: entries are
// semantically valid, sorted and unique by (kind,key,scope), and each is
// crosschecked against the effective set. A conflict whose ResolvedByID IS
// the effective artifact is accepted only when the conflict's scope matches
// the effective winner's source scope — a workspace-default conflict can
// only be resolved by a workspace-default winner, a project conflict only by
// the project winner. Opposite-scope displacement (a workspace-default
// conflict whose winner lost the key to a project override) is legal ONLY
// through the validated shadowed branch: the winner must be a project-scope
// artifact and the override must be recorded in the shadowed provenance.
func (p *Protocol) validateConflictProvenance(effective map[artifactKey]ProtocolArtifact) error {
	for i := range p.Conflicts {
		c := p.Conflicts[i]
		if err := c.Validate(); err != nil {
			return err
		}
		if i > 0 {
			prev := p.Conflicts[i-1]
			if prev.Kind > c.Kind ||
				(prev.Kind == c.Kind && prev.Key > c.Key) ||
				(prev.Kind == c.Kind && prev.Key == c.Key && prev.Scope >= c.Scope) {
				return &Error{Code: ErrCodeValidation, Message: "conflicts must be sorted and unique by (kind,key,scope)"}
			}
		}
		winner, ok := effective[artifactKey{kind: c.Kind, key: c.Key}]
		if !ok {
			return &Error{Code: ErrCodeValidation, Message: "conflict has no effective artifact for its (kind,key)"}
		}
		if winner.ArtifactID == c.ResolvedByID {
			// Direct acceptance requires scope agreement with the effective
			// winner: an ID match across mismatched scopes is an
			// inconsistent snapshot, not a resolution.
			if winner.SourceScope != c.Scope {
				return &Error{Code: ErrCodeValidation, Message: "conflict scope does not match the effective winner scope"}
			}
			continue
		}
		if c.Scope == ScopeProject {
			return &Error{Code: ErrCodeValidation, Message: "project-scope conflict winner must be the effective artifact"}
		}
		// Workspace-scope conflict whose winner lost to a project override:
		// the override MUST be recorded in the shadowed provenance.
		if winner.SourceScope != ScopeProject {
			return &Error{Code: ErrCodeValidation, Message: "workspace-scope conflict winner is not the effective artifact"}
		}
		explained := false
		for _, s := range p.Shadowed {
			if s.Kind == c.Kind && s.Key == c.Key &&
				s.ArtifactID == c.ResolvedByID && s.ShadowedByID == winner.ArtifactID {
				explained = true
				break
			}
		}
		if !explained {
			return &Error{Code: ErrCodeValidation, Message: "workspace-scope conflict winner is not explained by shadowed provenance"}
		}
	}
	return nil
}

func protocolArtifactsAsAny(list []ProtocolArtifact) []any {
	out := make([]any, 0, len(list))
	for _, a := range list {
		metadata := a.Metadata
		if metadata == nil {
			metadata = map[string]any{}
		}
		out = append(out, map[string]any{
			"artifact_id":  a.ArtifactID,
			"content":      a.Content,
			"content_type": a.ContentType,
			"digest":       a.Digest,
			"key":          a.Key,
			"kind":         string(a.Kind),
			"metadata":     metadata,
			"precedence":   a.Precedence,
			"revision":     a.Revision,
			"source_scope": string(a.SourceScope),
			"title":        a.Title,
		})
	}
	return out
}

func shadowedAsAny(list []ShadowedArtifact) []any {
	out := make([]any, 0, len(list))
	for _, s := range list {
		out = append(out, map[string]any{
			"artifact_id":    s.ArtifactID,
			"kind":           string(s.Kind),
			"key":            s.Key,
			"revision":       s.Revision,
			"shadowed_by_id": s.ShadowedByID,
		})
	}
	return out
}

func conflictsAsAny(list []KeyConflict) []any {
	out := make([]any, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]any{
			"artifact_ids":   stringSliceAsAny(c.ArtifactIDs),
			"key":            c.Key,
			"kind":           string(c.Kind),
			"resolved_by_id": c.ResolvedByID,
			"scope":          string(c.Scope),
		})
	}
	return out
}

func stringSliceAsAny(list []string) []any {
	out := make([]any, 0, len(list))
	for _, s := range list {
		out = append(out, s)
	}
	return out
}
