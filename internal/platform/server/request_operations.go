package server

import (
	"context"
	"errors"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
	"github.com/lleontor705/cortex/v2/internal/identity"
)

type operationsContextKey struct{}

func withOperations(ctx context.Context, ops Operations) context.Context {
	return context.WithValue(ctx, operationsContextKey{}, ops)
}

func operationsFromContext(ctx context.Context) (Operations, error) {
	ops, ok := ctx.Value(operationsContextKey{}).(Operations)
	if !ok || ops == nil {
		return nil, errors.New("server: authenticated operations are unavailable")
	}
	return ops, nil
}

// requestOperations keeps transports capability-only while selecting the
// principal-scoped AuthorizedStore installed by authentication middleware.
type requestOperations struct{}

func (requestOperations) SaveObservation(ctx context.Context, value *domain.Observation) error {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return err
	}
	return ops.SaveObservation(ctx, value)
}

// SaveObservationWithEffect delegates the durable, status-reporting save to
// the principal-scoped AuthorizedStore (REM-SAVE-001 server surface).
func (requestOperations) SaveObservationWithEffect(ctx context.Context, value *domain.Observation) (domain.SaveEffect, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return domain.SaveEffect{}, err
	}
	return ops.SaveObservationWithEffect(ctx, value)
}

// ExecuteHandoff delegates the compound, preauthorized handoff to the
// principal-scoped AuthorizedStore (REM-AUTH-001).
func (requestOperations) ExecuteHandoff(ctx context.Context, request domain.HandoffRequest) (domain.ObservationWriteResult, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return domain.ObservationWriteResult{}, err
	}
	return ops.ExecuteHandoff(ctx, request)
}
func (requestOperations) GetObservationByID(ctx context.Context, id int64) (*domain.Observation, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.GetObservationByID(ctx, id)
}
func (requestOperations) GetObservationByPublicID(ctx context.Context, id string) (*domain.Observation, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.GetObservationByPublicID(ctx, id)
}
func (requestOperations) UpdateObservation(ctx context.Context, value *domain.Observation) error {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return err
	}
	return ops.UpdateObservation(ctx, value)
}
func (requestOperations) DeleteObservation(ctx context.Context, id int64) error {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return err
	}
	return ops.DeleteObservation(ctx, id)
}
func (requestOperations) CreateSession(ctx context.Context, value *domain.Session) error {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return err
	}
	return ops.CreateSession(ctx, value)
}
func (requestOperations) ListSessions(ctx context.Context, project string) ([]*domain.Session, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.ListSessions(ctx, project)
}
func (requestOperations) PushSync(ctx context.Context, batch *domain.SyncBatch) (*domain.SyncResult, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.PushSync(ctx, batch)
}
func (requestOperations) PullSync(ctx context.Context, cursor int64, limit int) (*domain.SyncPage, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.PullSync(ctx, cursor, limit)
}
func (requestOperations) GetServerStats(ctx context.Context) (*domain.ServerStats, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.GetServerStats(ctx)
}
func (requestOperations) ListAuditEvents(ctx context.Context, limit int) ([]*domain.AuditEntry, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.ListAuditEvents(ctx, limit)
}
func (requestOperations) ListProjects(ctx context.Context) ([]string, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.ListProjects(ctx)
}
func (requestOperations) ListObservations(ctx context.Context, filter domain.ObservationFilter) ([]*domain.Observation, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.ListObservations(ctx, filter)
}
func (requestOperations) SearchObservations(ctx context.Context, query string, options domain.SearchOptions) ([]*domain.SearchResult, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.SearchObservations(ctx, query, options)
}
func (requestOperations) CreateGraphEdge(ctx context.Context, value *domain.Edge) error {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return err
	}
	return ops.CreateGraphEdge(ctx, value)
}
func (requestOperations) GetGraphEdgeByPublicID(ctx context.Context, id string) (*domain.Edge, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.GetGraphEdgeByPublicID(ctx, id)
}
func (requestOperations) GetRelatedObservations(ctx context.Context, id int64, depth int) ([]*domain.Observation, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.GetRelatedObservations(ctx, id, depth)
}
func (requestOperations) DeleteGraphEdge(ctx context.Context, id int64) error {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return err
	}
	return ops.DeleteGraphEdge(ctx, id)
}
func (requestOperations) GetImportanceScore(ctx context.Context, id int64) (*domain.ImportanceScore, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.GetImportanceScore(ctx, id)
}
func (requestOperations) GetGraphSubgraph(ctx context.Context, id string, depth, maxNodes int) (*domain.GraphSubgraph, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.GetGraphSubgraph(ctx, id, depth, maxNodes)
}
func (requestOperations) CreateUser(ctx context.Context, input identity.UserCreate) (identity.UserRecord, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return identity.UserRecord{}, err
	}
	return ops.CreateUser(ctx, input)
}
func (requestOperations) ListUsers(ctx context.Context) ([]identity.UserRecord, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.ListUsers(ctx)
}
func (requestOperations) GetUserProfile(ctx context.Context, id string) (*identity.UserRecord, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.GetUserProfile(ctx, id)
}
func (requestOperations) SetUserActive(ctx context.Context, id string, active bool) error {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return err
	}
	return ops.SetUserActive(ctx, id, active)
}
func (requestOperations) IssueToken(ctx context.Context, input identity.TokenIssue) (identity.IssuedToken, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return identity.IssuedToken{}, err
	}
	return ops.IssueToken(ctx, input)
}
func (requestOperations) ListTokens(ctx context.Context) ([]identity.TokenRecord, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.ListTokens(ctx)
}
func (requestOperations) RevokeToken(ctx context.Context, id string) error {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return err
	}
	return ops.RevokeToken(ctx, id)
}
func (requestOperations) RotateToken(ctx context.Context, id string) (identity.IssuedToken, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return identity.IssuedToken{}, err
	}
	return ops.RotateToken(ctx, id)
}
func (requestOperations) GetProjectContext(ctx context.Context, project string) (*domain.ProjectContext, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.GetProjectContext(ctx, project)
}
func (requestOperations) ListProjectSkills(ctx context.Context, project string) ([]*domain.ProjectSkill, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.ListProjectSkills(ctx, project)
}
func (requestOperations) GetProjectSkill(ctx context.Context, project, key string) (*domain.ProjectSkill, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.GetProjectSkill(ctx, project, key)
}
func (requestOperations) SaveProjectArtifact(ctx context.Context, in domain.SaveProjectArtifactInput) (*domain.ProjectArtifactItem, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.SaveProjectArtifact(ctx, in)
}
func (requestOperations) ListProjectArtifacts(ctx context.Context, project string, kind string) ([]*domain.ProjectArtifactItem, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.ListProjectArtifacts(ctx, project, kind)
}
func (requestOperations) DeleteProjectArtifact(ctx context.Context, id string, reason string) error {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return err
	}
	return ops.DeleteProjectArtifact(ctx, id, reason)
}
func (requestOperations) GetProjectDuplicates(ctx context.Context) ([]domain.ProjectDuplicateGroup, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.GetProjectDuplicates(ctx)
}
func (requestOperations) MergeProject(ctx context.Context, source, target string) (*domain.ProjectMergeResult, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.MergeProject(ctx, source, target)
}
func (requestOperations) ListCodeSymbols(ctx context.Context, filter code.SymbolFilter) ([]code.Symbol, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.ListCodeSymbols(ctx, filter)
}
func (requestOperations) GetCodeGraph(ctx context.Context, project string) (*code.CodeGraph, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.GetCodeGraph(ctx, project)
}
func (requestOperations) SaveCodeGraph(ctx context.Context, graph *code.CodeGraph) error {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return err
	}
	return ops.SaveCodeGraph(ctx, graph)
}
func (requestOperations) GetRAGStats(ctx context.Context, project string) (*domain.RAGStats, error) {
	ops, err := operationsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return ops.GetRAGStats(ctx, project)
}

