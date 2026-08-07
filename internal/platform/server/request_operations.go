package server

import (
	"context"
	"errors"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/identity"
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
