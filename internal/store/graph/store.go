// Package graph implements the SQLite graph store for Cortex.
//
// It provides knowledge graph operations for creating, querying, and deleting
// edges between observations. The store implements the domain.GraphRepository interface.
package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

// sqliteDatetimeFormat is the format used by SQLite's datetime() function.
const sqliteDatetimeFormat = "2006-01-02 15:04:05"

// Store implements the SQLite graph store.
type Store struct {
	db *sql.DB
	v2 bool
}

// v2TemporalSchema detects the clean v2 graph schema without coupling the
// store to a particular backend implementation.
func (s *Store) v2TemporalSchema(ctx context.Context) bool {
	return s.v2
}

type graphTxKey struct{}

// WithinTx enlists the graph store in a shared SQLite UnitOfWork transaction.
func (s *Store) WithinTx(ctx context.Context, handle any, fn func(context.Context) error) error {
	tx, ok := handle.(*sql.Tx)
	if !ok {
		return fmt.Errorf("graph store: WithinTx expected *sql.Tx handle, got %T", handle)
	}
	return fn(context.WithValue(ctx, graphTxKey{}, tx))
}

func graphTx(ctx context.Context) *sql.Tx { tx, _ := ctx.Value(graphTxKey{}).(*sql.Tx); return tx }

// NewStore creates a new graph store with the given database connection.
func NewStore(db *sql.DB) *Store {
	var ddl string
	_ = db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='edges'`).Scan(&ddl)
	return &Store{db: db, v2: strings.Contains(ddl, "tx_from")}
}

// CreateEdge creates a relationship between two observations.
// Returns domain.ErrAlreadyExists if an edge with the same (from, to, relation_type) exists.
func (s *Store) CreateEdge(ctx context.Context, edge *domain.Edge) error {
	if edge == nil {
		return &domain.ValidationError{
			Field:   "edge",
			Message: "edge cannot be nil",
		}
	}

	tx := graphTx(ctx)
	if s.v2TemporalSchema(ctx) && (edge.RelationType == domain.RelationSupersedes || edge.RelationType == domain.RelationContradicts) && tx == nil {
		return fmt.Errorf("graph: %s requires a shared UnitOfWork transaction", edge.RelationType)
	}
	var result sql.Result
	var err error
	if s.v2TemporalSchema(ctx) {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		validFrom := nullableTime(edge.ValidFrom)
		if validFrom == nil {
			validFrom = now
		}
		// Close the exact predecessor before inserting the successor. The shared
		// transaction makes this ordering atomic and lets the current-state unique
		// index reject a conflicting writer without touching unrelated facts.
		if edge.RelationType == domain.RelationSupersedes || edge.RelationType == domain.RelationContradicts {
			state := "deprecated"
			if edge.RelationType == domain.RelationSupersedes {
				state = "superseded"
			}
			update := `UPDATE edges SET valid_until=?, tx_until=?, fact_state=?, evolution_type=?, change_reason=COALESCE(?,change_reason) WHERE from_obs_id=? AND to_obs_id=? AND relation_type=? AND valid_until IS NULL AND fact_state NOT IN ('deprecated','superseded') AND (tenant_id IS ? OR tenant_id=?) AND (workspace_id IS ? OR workspace_id=?)`
			args := []any{now, now, state, edge.RelationType, nullableString(edge.ChangeReason), edge.FromObsID, edge.ToObsID, edge.RelationType, nullableString(edge.TenantID), edge.TenantID, nullableString(edge.WorkspaceID), edge.WorkspaceID}
			if tx != nil {
				_, err = tx.ExecContext(ctx, update, args...)
			}
			if err != nil {
				return fmt.Errorf("graph: close predecessor: %w", err)
			}
		}
		q := `INSERT INTO edges (from_obs_id,to_obs_id,relation_type,weight,confidence,source,reasoning,valid_from,invalid_at,valid_until,tx_from,tenant_id,workspace_id) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`
		if tx != nil {
			result, err = tx.ExecContext(ctx, q, edge.FromObsID, edge.ToObsID, edge.RelationType, edge.Weight, edge.Confidence, nullableString(edge.Source), nullableString(edge.Reasoning), validFrom, nullableTime(edge.InvalidAt), nullableTime(edge.ValidUntil), now, nullableString(edge.TenantID), nullableString(edge.WorkspaceID))
		} else {
			result, err = s.db.ExecContext(ctx, q, edge.FromObsID, edge.ToObsID, edge.RelationType, edge.Weight, edge.Confidence, nullableString(edge.Source), nullableString(edge.Reasoning), validFrom, nullableTime(edge.InvalidAt), nullableTime(edge.ValidUntil), now, nullableString(edge.TenantID), nullableString(edge.WorkspaceID))
		}
	} else {
		result, err = s.db.ExecContext(ctx, `INSERT INTO edges (from_obs_id, to_obs_id, relation_type, weight, confidence, source, reasoning, valid_from, invalid_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, edge.FromObsID, edge.ToObsID, edge.RelationType, edge.Weight, edge.Confidence, nullableString(edge.Source), nullableString(edge.Reasoning), nullableTime(edge.ValidFrom), nullableTime(edge.InvalidAt))
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return domain.ErrAlreadyExists
		}
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			return &domain.NotFoundError{Type: "observation", ID: fmt.Sprintf("%d or %d", edge.FromObsID, edge.ToObsID)}
		}
		return fmt.Errorf("graph: create edge: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("graph: get last insert id: %w", err)
	}
	edge.ID = id

	return nil
}

// CreateEdgeInTx is the explicit atomic-save seam; the transaction is supplied
// by WithinTx and therefore predecessor closure and successor insert cannot
// commit independently.
func (s *Store) CreateEdgeInTx(ctx context.Context, edge *domain.Edge) error {
	return s.CreateEdge(ctx, edge)
}

// CurrentEdges returns current facts only. Historical, deprecated and
// superseded edges remain queryable through GetEdge/EvolutionChain.
func (s *Store) CurrentEdges(ctx context.Context, obsID int64) ([]*domain.Edge, error) {
	if !s.v2TemporalSchema(ctx) {
		return s.GetEdgesForObservation(ctx, obsID)
	}
	return s.queryTemporalEdges(ctx, `(from_obs_id=? OR to_obs_id=?) AND valid_until IS NULL AND invalid_at IS NULL AND fact_state NOT IN ('deprecated','superseded')`, obsID, obsID)
}

// EdgesAsOf reconstructs a graph at a valid-time and system-time point.
func (s *Store) EdgesAsOf(ctx context.Context, obsID int64, validAt, systemAt time.Time) ([]*domain.Edge, error) {
	if !s.v2TemporalSchema(ctx) {
		return s.GetEdgesValidAt(ctx, obsID, validAt)
	}
	v, st := validAt.UTC().Format(time.RFC3339Nano), systemAt.UTC().Format(time.RFC3339Nano)
	return s.queryTemporalEdges(ctx, `(from_obs_id=? OR to_obs_id=?) AND (valid_from IS NULL OR valid_from<=?) AND (valid_until IS NULL OR valid_until>?) AND tx_from<=? AND (tx_until IS NULL OR tx_until>?)`, obsID, obsID, v, v, st, st)
}

// GetRelatedScoped performs bounded bidirectional traversal with cycle safety
// and tenant/workspace/project isolation. The visited set is maintained by an
// ID path predicate, so UNION ALL recursion cannot revisit a node indefinitely.
func (s *Store) GetRelatedScoped(ctx context.Context, obsID int64, opts domain.GraphTraversalOptions) ([]*domain.Observation, error) {
	depth := opts.Depth
	if depth <= 0 {
		depth = 1
	}
	if depth > 10 {
		depth = 10
	}
	budget := opts.MaxVisited
	if budget <= 0 {
		budget = 1000
	}
	if budget > 10000 {
		budget = 10000
	}
	if !s.v2TemporalSchema(ctx) {
		return s.GetRelated(ctx, obsID, depth)
	}
	q := `WITH RECURSIVE walk(id,lvl,path) AS (SELECT ?,0,','||?||',' UNION ALL SELECT CASE WHEN e.from_obs_id=walk.id THEN e.to_obs_id ELSE e.from_obs_id END,walk.lvl+1,walk.path||CASE WHEN e.from_obs_id=walk.id THEN e.to_obs_id ELSE e.from_obs_id END||',' FROM edges e JOIN walk ON (e.from_obs_id=walk.id OR e.to_obs_id=walk.id) JOIN observations so ON so.id=e.from_obs_id JOIN observations tobs ON tobs.id=e.to_obs_id WHERE walk.lvl<? AND instr(walk.path, ','||CASE WHEN e.from_obs_id=walk.id THEN e.to_obs_id ELSE e.from_obs_id END||',')=0 AND (? IS NOT NULL OR (e.valid_until IS NULL AND e.invalid_at IS NULL AND e.fact_state NOT IN ('deprecated','superseded'))) AND (? IS NULL OR ((e.valid_from IS NULL OR e.valid_from<=?) AND (e.invalid_at IS NULL OR e.invalid_at>?) AND (e.tx_from IS NULL OR e.tx_from<=?) AND (e.tx_until IS NULL OR e.tx_until>?))) AND (?='' OR e.tenant_id=?) AND (?='' OR e.workspace_id=?) AND (?='' OR so.project=?) AND (?='' OR tobs.project=?) AND (?='' OR so.tenant_id=?) AND (?='' OR tobs.tenant_id=?) AND (?='' OR so.workspace_id=?) AND (?='' OR tobs.workspace_id=?) LIMIT ?) SELECT DISTINCT o.id,o.title,o.content,o.type,o.project,o.scope,o.session_id,COALESCE(o.topic_key,''),COALESCE(o.confidence,1),COALESCE(o.source,'manual'),COALESCE(o.tags,''),o.created_at,o.updated_at FROM observations o JOIN walk ON walk.id=o.id WHERE o.id<>? AND o.deleted_at IS NULL AND (?='' OR o.project=?) AND (?='' OR o.tenant_id=?) AND (?='' OR o.workspace_id=?) ORDER BY walk.lvl,o.created_at DESC`
	var asOf any
	var asOfText string
	if opts.AsOf != nil {
		asOfText = opts.AsOf.UTC().Format(time.RFC3339Nano)
		asOf = asOfText
	}
	args := []any{obsID, obsID, depth, asOf, asOf, asOfText, asOfText, asOfText, asOfText, opts.TenantID, opts.TenantID, opts.WorkspaceID, opts.WorkspaceID, opts.Project, opts.Project, opts.Project, opts.Project, opts.TenantID, opts.TenantID, opts.TenantID, opts.TenantID, opts.WorkspaceID, opts.WorkspaceID, opts.WorkspaceID, opts.WorkspaceID, budget, obsID, opts.Project, opts.Project, opts.TenantID, opts.TenantID, opts.WorkspaceID, opts.WorkspaceID}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("graph: scoped traversal: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.Observation
	for rows.Next() {
		o := &domain.Observation{}
		var tags, ca, ua string
		if err := rows.Scan(&o.ID, &o.Title, &o.Content, &o.Type, &o.Project, &o.Scope, &o.SessionID, &o.TopicKey, &o.Confidence, &o.Source, &tags, &ca, &ua); err != nil {
			return nil, err
		}
		o.CreatedAt, _ = parseAnyTime(ca)
		o.UpdatedAt, _ = parseAnyTime(ua)
		if tags != "" {
			_ = json.Unmarshal([]byte(tags), &o.Tags)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) queryTemporalEdges(ctx context.Context, where string, args ...any) ([]*domain.Edge, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,from_obs_id,to_obs_id,relation_type,weight,confidence,COALESCE(source,''),COALESCE(reasoning,''),valid_from,invalid_at,valid_until,tx_from,tx_until,created_at,evolution_id,COALESCE(evolution_type,'original'),COALESCE(fact_state,'current'),COALESCE(change_reason,''),COALESCE(tenant_id,''),COALESCE(workspace_id,'') FROM edges WHERE `+where+` ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("graph: temporal query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.Edge
	for rows.Next() {
		e := &domain.Edge{}
		var vf, ia, vu, tf, tu, ca sql.NullString
		var ev sql.NullInt64
		if err := rows.Scan(&e.ID, &e.FromObsID, &e.ToObsID, &e.RelationType, &e.Weight, &e.Confidence, &e.Source, &e.Reasoning, &vf, &ia, &vu, &tf, &tu, &ca, &ev, &e.EvolutionType, &e.FactState, &e.ChangeReason, &e.TenantID, &e.WorkspaceID); err != nil {
			return nil, err
		}
		e.ValidFrom = parseNullableTime(vf)
		e.InvalidAt = parseNullableTime(ia)
		e.ValidUntil = parseNullableTime(vu)
		e.TxFrom = parseNullableTime(tf)
		e.TxUntil = parseNullableTime(tu)
		if ev.Valid {
			e.EvolutionID = &ev.Int64
		}
		e.CreatedAt, _ = parseAnyTime(ca.String)
		out = append(out, e)
	}
	return out, rows.Err()
}

func parseNullableTime(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t, err := parseAnyTime(v.String)
	if err != nil {
		return nil
	}
	return &t
}
func parseAnyTime(v string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, sqliteDatetimeFormat} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time %q", v)
}

// levelNeighborChunk caps the IN-list size per SQL statement for the batch
// adjacency lookup. 900 stays below conservative SQLite variable limits, so
// typical frontiers resolve in a single statement per level (GRAPH-01).
const levelNeighborChunk = 900

// GetLevelNeighborObservations resolves one-hop adjacency for an entire BFS
// frontier in one SQL statement per chunk (domain/graph.LevelNeighborBatcher).
// It returns hydrated neighbor observations for every requested frontier ID,
// deduplicated and ordered by ascending observation ID, following edges in
// both directions. Soft-deleted endpoints are excluded; on the v2 temporal
// schema only current facts (open validity, not deprecated/superseded) are
// followed.
func (s *Store) GetLevelNeighborObservations(ctx context.Context, frontier []int64) (map[int64][]*domain.Observation, error) {
	out := make(map[int64][]*domain.Observation, len(frontier))
	if len(frontier) == 0 {
		return out, nil
	}
	want := make(map[int64]bool, len(frontier))
	for _, id := range frontier {
		want[id] = true
	}

	fCols := "f.id, f.title, f.content, f.type, f.project, f.scope, f.session_id, COALESCE(f.topic_key,''), COALESCE(f.confidence,1), COALESCE(f.source,'manual'), COALESCE(f.tags,''), f.created_at, f.updated_at"
	tCols := "t.id, t.title, t.content, t.type, t.project, t.scope, t.session_id, COALESCE(t.topic_key,''), COALESCE(t.confidence,1), COALESCE(t.source,'manual'), COALESCE(t.tags,''), t.created_at, t.updated_at"
	validity := ""
	if s.v2TemporalSchema(ctx) {
		validity = " AND e.valid_until IS NULL AND e.invalid_at IS NULL AND e.fact_state NOT IN ('deprecated','superseded')"
	}

	for start := 0; start < len(frontier); start += levelNeighborChunk {
		end := start + levelNeighborChunk
		if end > len(frontier) {
			end = len(frontier)
		}
		ids := frontier[start:end]
		placeholders := strings.Repeat("?,", len(ids))
		placeholders = placeholders[:len(placeholders)-1]
		query := `SELECT e.from_obs_id, e.to_obs_id, ` + fCols + `, ` + tCols + `
			FROM edges e
			JOIN observations f ON f.id = e.from_obs_id
			JOIN observations t ON t.id = e.to_obs_id
			WHERE (e.from_obs_id IN (` + placeholders + `) OR e.to_obs_id IN (` + placeholders + `))
			  AND f.deleted_at IS NULL AND t.deleted_at IS NULL` + validity

		args := make([]any, 0, len(ids)*2)
		for _, id := range ids {
			args = append(args, id)
		}
		for _, id := range ids {
			args = append(args, id)
		}

		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("graph: level neighbors: %w", err)
		}
		for rows.Next() {
			var fromID, toID int64
			fobs := &domain.Observation{}
			tobs := &domain.Observation{}
			var ftags, ttags, fca, fua, tca, tua string
			if err := rows.Scan(
				&fromID, &toID,
				&fobs.ID, &fobs.Title, &fobs.Content, &fobs.Type, &fobs.Project, &fobs.Scope, &fobs.SessionID, &fobs.TopicKey, &fobs.Confidence, &fobs.Source, &ftags, &fca, &fua,
				&tobs.ID, &tobs.Title, &tobs.Content, &tobs.Type, &tobs.Project, &tobs.Scope, &tobs.SessionID, &tobs.TopicKey, &tobs.Confidence, &tobs.Source, &ttags, &tca, &tua,
			); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("graph: scan level neighbor: %w", err)
			}
			fobs.CreatedAt, _ = parseAnyTime(fca)
			fobs.UpdatedAt, _ = parseAnyTime(fua)
			tobs.CreatedAt, _ = parseAnyTime(tca)
			tobs.UpdatedAt, _ = parseAnyTime(tua)
			if ftags != "" {
				_ = json.Unmarshal([]byte(ftags), &fobs.Tags)
			}
			if ttags != "" {
				_ = json.Unmarshal([]byte(ttags), &tobs.Tags)
			}
			if want[fromID] {
				out[fromID] = append(out[fromID], tobs)
			}
			if want[toID] && fromID != toID {
				out[toID] = append(out[toID], fobs)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("graph: level neighbors: %w", err)
		}
		_ = rows.Close()
	}

	// Deduplicate by neighbor ID (multiple relation types between the same
	// pair) and order ascending so shuffled rows cannot change traversal.
	for id, list := range out {
		seen := make(map[int64]bool, len(list))
		deduped := list[:0]
		for _, o := range list {
			if seen[o.ID] {
				continue
			}
			seen[o.ID] = true
			deduped = append(deduped, o)
		}
		sort.Slice(deduped, func(i, j int) bool { return deduped[i].ID < deduped[j].ID })
		out[id] = deduped
	}
	return out, nil
}

// GetRelated retrieves observations related to the given observation ID,
// up to the specified depth using a recursive CTE.
func (s *Store) GetRelated(ctx context.Context, obsID int64, depth int) ([]*domain.Observation, error) {
	if s.v2TemporalSchema(ctx) {
		return s.GetRelatedScoped(ctx, obsID, domain.GraphTraversalOptions{Depth: depth})
	}
	if depth <= 0 {
		depth = 1
	}
	if depth > 10 {
		depth = 10
	}

	query := `
		WITH RECURSIVE related(id, lvl) AS (
			SELECT ?, 0
			UNION
			SELECT CASE
				WHEN e.from_obs_id = related.id THEN e.to_obs_id
				ELSE e.from_obs_id
			END, related.lvl + 1
			FROM edges e
			JOIN related ON (e.from_obs_id = related.id OR e.to_obs_id = related.id)
			WHERE related.lvl < ?
		)
		SELECT DISTINCT o.id, o.title, o.content, o.type, o.project, o.scope,
		       o.session_id, COALESCE(o.topic_key, '') AS topic_key,
		       COALESCE(o.confidence, 1.0), COALESCE(o.source, 'manual'),
		       COALESCE(o.tags, ''), o.created_at, o.updated_at
		FROM observations o
		JOIN related r ON r.id = o.id
		WHERE o.deleted_at IS NULL AND o.id != ?
		ORDER BY o.created_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query, obsID, depth, obsID)
	if err != nil {
		return nil, fmt.Errorf("graph: get related: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var observations []*domain.Observation
	for rows.Next() {
		obs := &domain.Observation{}
		var createdAt, updatedAt, tagsJSON string
		if err := rows.Scan(
			&obs.ID, &obs.Title, &obs.Content, &obs.Type, &obs.Project,
			&obs.Scope, &obs.SessionID, &obs.TopicKey,
			&obs.Confidence, &obs.Source, &tagsJSON,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("graph: scan related observation: %w", err)
		}
		obs.CreatedAt, _ = parseTime(createdAt)
		obs.UpdatedAt, _ = parseTime(updatedAt)
		if tagsJSON != "" {
			_ = json.Unmarshal([]byte(tagsJSON), &obs.Tags)
		}
		observations = append(observations, obs)
	}

	if observations == nil {
		return []*domain.Observation{}, rows.Err()
	}
	return observations, rows.Err()
}

// DeleteEdge removes a relationship between observations.
func (s *Store) DeleteEdge(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM edges WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("graph: delete edge: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("graph: rows affected: %w", err)
	}

	if rows == 0 {
		return &domain.NotFoundError{Type: "edge", ID: id}
	}

	return nil
}

// scanEdgeRow scans a single edge row including evolution fields.
func (s *Store) scanEdgeRow(row *sql.Row) (*domain.Edge, error) {
	edge := &domain.Edge{}
	var createdAt string
	var validFrom, invalidAt sql.NullString
	var evolutionID sql.NullInt64
	if err := row.Scan(
		&edge.ID, &edge.FromObsID, &edge.ToObsID, &edge.RelationType, &edge.Weight,
		&edge.Confidence, &edge.Source, &edge.Reasoning,
		&validFrom, &invalidAt, &createdAt,
		&evolutionID, &edge.EvolutionType, &edge.FactState, &edge.ChangeReason,
	); err != nil {
		return nil, err
	}
	edge.CreatedAt, _ = parseTime(createdAt)
	if validFrom.Valid {
		t, _ := parseTime(validFrom.String)
		edge.ValidFrom = &t
	}
	if invalidAt.Valid {
		t, _ := parseTime(invalidAt.String)
		edge.InvalidAt = &t
	}
	if evolutionID.Valid {
		edge.EvolutionID = &evolutionID.Int64
	}
	return edge, nil
}

// scanEdgeRows scans multiple edge rows including evolution fields.
func (s *Store) scanEdgeRows(rows *sql.Rows) ([]*domain.Edge, error) {
	var edges []*domain.Edge
	for rows.Next() {
		edge := &domain.Edge{}
		var createdAt string
		var validFrom, invalidAt sql.NullString
		var evolutionID sql.NullInt64
		if err := rows.Scan(
			&edge.ID, &edge.FromObsID, &edge.ToObsID, &edge.RelationType, &edge.Weight,
			&edge.Confidence, &edge.Source, &edge.Reasoning,
			&validFrom, &invalidAt, &createdAt,
			&evolutionID, &edge.EvolutionType, &edge.FactState, &edge.ChangeReason,
		); err != nil {
			return nil, fmt.Errorf("graph: scan edge: %w", err)
		}
		edge.CreatedAt, _ = parseTime(createdAt)
		if validFrom.Valid {
			t, _ := parseTime(validFrom.String)
			edge.ValidFrom = &t
		}
		if invalidAt.Valid {
			t, _ := parseTime(invalidAt.String)
			edge.InvalidAt = &t
		}
		if evolutionID.Valid {
			edge.EvolutionID = &evolutionID.Int64
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

// parseTime parses a SQLite datetime string, logging a warning if it fails.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(sqliteDatetimeFormat, s)
	if err != nil && s != "" {
		log.Printf("graph: failed to parse time %q", s)
	}
	return t, err
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullableTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Format(sqliteDatetimeFormat)
}

// GetEdgesForObservation retrieves all edges where the observation is either source or target.
func (s *Store) GetEdgesForObservation(ctx context.Context, obsID int64) ([]*domain.Edge, error) {
	if s.v2TemporalSchema(ctx) {
		return s.queryTemporalEdges(ctx, `(from_obs_id=? OR to_obs_id=?)`, obsID, obsID)
	}
	query := `SELECT id, from_obs_id, to_obs_id, relation_type, weight,
	                 COALESCE(confidence, 1.0), COALESCE(source, ''), COALESCE(reasoning, ''),
	                 valid_from, invalid_at, created_at,
	                 evolution_id, COALESCE(evolution_type, 'original'),
	                 COALESCE(fact_state, 'current'), COALESCE(change_reason, '')
	          FROM edges
	          WHERE from_obs_id = ? OR to_obs_id = ?
	          ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, obsID, obsID)
	if err != nil {
		return nil, fmt.Errorf("graph: get edges for observation %d: %w", obsID, err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanEdgeRows(rows)
}

// GetEdgesValidAt retrieves edges for an observation that were valid at the given time.
// An edge is valid at time `at` if: (valid_from IS NULL OR valid_from <= at) AND (invalid_at IS NULL OR invalid_at > at).
func (s *Store) GetEdgesValidAt(ctx context.Context, obsID int64, at time.Time) ([]*domain.Edge, error) {
	if s.v2TemporalSchema(ctx) {
		return s.EdgesAsOf(ctx, obsID, at, time.Now().UTC())
	}
	atStr := at.UTC().Format(time.RFC3339)
	query := `SELECT id, from_obs_id, to_obs_id, relation_type, weight,
	                 COALESCE(confidence, 1.0), COALESCE(source, ''), COALESCE(reasoning, ''),
	                 valid_from, invalid_at, created_at,
	                 evolution_id, COALESCE(evolution_type, 'original'),
	                 COALESCE(fact_state, 'current'), COALESCE(change_reason, '')
	          FROM edges
	          WHERE (from_obs_id = ? OR to_obs_id = ?)
	            AND (valid_from IS NULL OR valid_from <= ?)
	            AND (invalid_at IS NULL OR invalid_at > ?)
	          ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, obsID, obsID, atStr, atStr)
	if err != nil {
		return nil, fmt.Errorf("graph: get edges valid at %s for observation %d: %w", atStr, obsID, err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanEdgeRows(rows)
}

// GetEdge retrieves a specific edge by its ID.
func (s *Store) GetEdge(ctx context.Context, id int64) (*domain.Edge, error) {
	if s.v2TemporalSchema(ctx) {
		edges, err := s.queryTemporalEdges(ctx, `id=?`, id)
		if err != nil {
			return nil, err
		}
		if len(edges) == 0 {
			return nil, &domain.NotFoundError{Type: "edge", ID: id}
		}
		return edges[0], nil
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, from_obs_id, to_obs_id, relation_type, weight,
		       COALESCE(confidence, 1.0), COALESCE(source, ''), COALESCE(reasoning, ''),
		       valid_from, invalid_at, created_at,
		       evolution_id, COALESCE(evolution_type, 'original'),
		       COALESCE(fact_state, 'current'), COALESCE(change_reason, '')
		FROM edges
		WHERE id = ?
	`, id)

	edge, err := s.scanEdgeRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, &domain.NotFoundError{Type: "edge", ID: id}
		}
		return nil, fmt.Errorf("graph: get edge %d: %w", id, err)
	}

	return edge, nil
}

// CurrentEdgeByPairInTx returns the single current durable edge for the exact
// (from, to, relationType) triple. It reads through the shared transaction
// enlisted via WithinTx when one is active in ctx, so the caller observes its
// own uncommitted state rather than a separate connection snapshot. Returns
// domain.NotFoundError when no current fact exists for the triple.
func (s *Store) CurrentEdgeByPairInTx(ctx context.Context, fromObsID, toObsID int64, relationType string) (*domain.Edge, error) {
	query := `
		SELECT id, from_obs_id, to_obs_id, relation_type, weight,
		       COALESCE(confidence, 1.0), COALESCE(source, ''), COALESCE(reasoning, ''),
		       valid_from, invalid_at, created_at,
		       evolution_id, COALESCE(evolution_type, 'original'),
		       COALESCE(fact_state, 'current'), COALESCE(change_reason, '')
		FROM edges
		WHERE from_obs_id = ? AND to_obs_id = ? AND relation_type = ?
		  AND valid_until IS NULL AND invalid_at IS NULL
		  AND fact_state NOT IN ('deprecated', 'superseded')
		ORDER BY created_at DESC
		LIMIT 1
	`
	var row *sql.Row
	if tx := graphTx(ctx); tx != nil {
		row = tx.QueryRowContext(ctx, query, fromObsID, toObsID, relationType)
	} else {
		row = s.db.QueryRowContext(ctx, query, fromObsID, toObsID, relationType)
	}
	edge, err := s.scanEdgeRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, &domain.NotFoundError{Type: "edge", ID: fmt.Sprintf("%d->%d %s", fromObsID, toObsID, relationType)}
		}
		return nil, fmt.Errorf("graph: current edge by pair: %w", err)
	}
	return edge, nil
}

// GetEvolutionChain retrieves all edges that share the same endpoints.
func (s *Store) GetEvolutionChain(ctx context.Context, fromObsID, toObsID int64) ([]*domain.Edge, error) {
	if s.v2TemporalSchema(ctx) {
		return s.queryTemporalEdges(ctx, `from_obs_id=? AND to_obs_id=?`, fromObsID, toObsID)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, from_obs_id, to_obs_id, relation_type, weight,
		       COALESCE(confidence, 1.0), COALESCE(source, ''), COALESCE(reasoning, ''),
		       valid_from, invalid_at, created_at,
		       evolution_id, COALESCE(evolution_type, 'original'),
		       COALESCE(fact_state, 'current'), COALESCE(change_reason, '')
		FROM edges
		WHERE from_obs_id = ? AND to_obs_id = ?
		ORDER BY created_at ASC
	`, fromObsID, toObsID)
	if err != nil {
		return nil, fmt.Errorf("graph: get evolution chain: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanEdgeRows(rows)
}

// CountEdgesByObservation counts edges connected to a specific observation.
func (s *Store) CountEdgesByObservation(ctx context.Context, obsID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM edges
		WHERE from_obs_id = ? OR to_obs_id = ?
	`, obsID, obsID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("graph: count edges by observation %d: %w", obsID, err)
	}
	return count, nil
}

// CountAllEdges counts all edges in the system.
func (s *Store) CountAllEdges(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM edges`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("graph: count all edges: %w", err)
	}
	return count, nil
}

// GetContradictions retrieves contradiction edges created in a time range.
func (s *Store) GetContradictions(ctx context.Context, from, to time.Time) ([]*domain.Edge, error) {
	if s.v2TemporalSchema(ctx) {
		return s.queryTemporalEdges(ctx, `relation_type=? AND created_at>=? AND created_at<=?`, domain.RelationContradicts, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, from_obs_id, to_obs_id, relation_type, weight,
		       COALESCE(confidence, 1.0), COALESCE(source, ''), COALESCE(reasoning, ''),
		       valid_from, invalid_at, created_at,
		       evolution_id, COALESCE(evolution_type, 'original'),
		       COALESCE(fact_state, 'current'), COALESCE(change_reason, '')
		FROM edges
		WHERE relation_type = ? AND created_at >= ? AND created_at <= ?
		ORDER BY created_at DESC
	`, domain.RelationContradicts, from.Format(sqliteDatetimeFormat), to.Format(sqliteDatetimeFormat))
	if err != nil {
		return nil, fmt.Errorf("graph: get contradictions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanEdgeRows(rows)
}

// UpdateEdge updates mutable edge fields by ID.
func (s *Store) UpdateEdge(ctx context.Context, edge *domain.Edge) error {
	if edge == nil {
		return &domain.ValidationError{Field: "edge", Message: "edge cannot be nil"}
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE edges
		SET relation_type = ?, weight = ?, confidence = ?, source = ?, reasoning = ?,
		    valid_from = ?, invalid_at = ?
		WHERE id = ?
	`,
		edge.RelationType, edge.Weight, edge.Confidence, nullableString(edge.Source), nullableString(edge.Reasoning),
		nullableTime(edge.ValidFrom), nullableTime(edge.InvalidAt), edge.ID,
	)
	if err != nil {
		return fmt.Errorf("graph: update edge %d: %w", edge.ID, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("graph: rows affected for edge %d: %w", edge.ID, err)
	}
	if rows == 0 {
		return &domain.NotFoundError{Type: "edge", ID: edge.ID}
	}

	return nil
}

// Ensure Store implements domain.GraphRepository.
var _ domain.GraphRepository = (*Store)(nil)
var _ domain.TxParticipant = (*Store)(nil)
