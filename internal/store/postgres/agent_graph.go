package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

func agentGraphBudgets(maxHops, maxNodes, maxEdges int) (int, int, int) {
	if maxHops < 1 || maxHops > 4 {
		maxHops = 2
	}
	if maxNodes < 1 || maxNodes > 200 {
		maxNodes = 96
	}
	if maxEdges < 1 || maxEdges > 400 {
		maxEdges = 192
	}
	return maxHops, maxNodes, maxEdges
}

func canonicalUUIDSeeds(values []string, limit int) []uuid.UUID {
	unique := map[uuid.UUID]bool{}
	for _, raw := range values {
		if id, err := uuid.Parse(strings.TrimSpace(raw)); err == nil && id != uuid.Nil {
			unique[id] = true
		}
	}
	result := make([]uuid.UUID, 0, len(unique))
	for id := range unique {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

// GetAgentGraphSnapshot applies every budget before acquisition. Each hop is a
// separate canonically ordered bounded query over already-authorized frontier
// nodes, so neither corpus materialization nor unbounded recursive fan-out can
// occur before truncation.
func (s *AuthorizedStore) GetAgentGraphSnapshot(ctx context.Context, projectPublicID, projectLabel string, seedPublicIDs []string, maxHops, maxNodes, maxEdges int) (*domain.GraphSubgraph, error) {
	if s == nil || s.store == nil || strings.TrimSpace(projectLabel) == "" {
		return nil, domain.ErrInvalidInput
	}
	projectID, err := uuid.Parse(strings.TrimSpace(projectPublicID))
	if err != nil || projectID == uuid.Nil {
		return nil, domain.ErrInvalidInput
	}
	projectPublicID = projectID.String()
	maxHops, maxNodes, maxEdges = agentGraphBudgets(maxHops, maxNodes, maxEdges)
	if err := s.authorize(ctx, authz.ResourceSearch, authz.ActionSearch, projectPublicID, "", ""); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionRead, projectPublicID, "", ""); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, authz.ResourceGraph, authz.ActionRead, projectPublicID, "", ""); err != nil {
		return nil, err
	}
	seeds := canonicalUUIDSeeds(seedPublicIDs, maxNodes+1)
	result := &domain.GraphSubgraph{Nodes: []domain.GraphNode{}, Edges: []domain.GraphLink{}}
	err = s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		workspace, scopeErr := requireWorkspaceScope(ctx)
		if scopeErr != nil {
			return scopeErr
		}
		var internalProjectID int64
		var canonicalLabel string
		if queryErr := tx.QueryRow(ctx, `SELECT id,name FROM projects WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=$1 AND public_id=$2::uuid`, workspace, projectPublicID).Scan(&internalProjectID, &canonicalLabel); queryErr != nil {
			if errors.Is(queryErr, pgx.ErrNoRows) {
				return errors.New(authz.DenyProject)
			}
			return queryErr
		}
		if canonicalLabel != projectLabel {
			return errors.New(authz.DenyProject)
		}
		seedSQL := `SELECT o.id,o.public_id::text,o.title,o.type FROM observations o WHERE o.tenant_id=public.cortex_current_tenant() AND o.workspace_id=$1 AND o.project_id=$2 AND o.deleted_at IS NULL`
		args := []any{workspace, internalProjectID}
		seedSQL, args = s.store.appendObservationVisibilityPredicate(seedSQL, args, true)
		seedSQL += fmt.Sprintf(" AND o.public_id=ANY($%d::uuid[]) ORDER BY o.public_id LIMIT $%d", len(args)+1, len(args)+2)
		args = append(args, seeds, maxNodes+1)
		rows, queryErr := tx.Query(ctx, seedSQL, args...)
		if queryErr != nil {
			return queryErr
		}
		selected := map[int64]string{}
		frontier := make([]int64, 0, len(seeds))
		for rows.Next() {
			var internalID int64
			var publicID, title, typ string
			if scanErr := rows.Scan(&internalID, &publicID, &title, &typ); scanErr != nil {
				rows.Close()
				return scanErr
			}
			if len(result.Nodes) >= maxNodes {
				result.Truncated = true
				continue
			}
			nodeID := "observation:" + publicID
			selected[internalID] = nodeID
			frontier = append(frontier, internalID)
			result.Nodes = append(result.Nodes, domain.GraphNode{ID: nodeID, Kind: "observation", Subtype: typ, Label: title, Project: projectPublicID, Metadata: map[string]any{"observation_id": internalID}})
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return rowsErr
		}
		rows.Close()
		edgeSeen := map[string]bool{}
		queryHop := func(frontier []int64, limit int) (pgx.Rows, error) {
			hopSQL := `SELECT e.public_id::text,e.from_observation_id,e.to_observation_id,e.relation_type,e.weight,e.confidence,o.id,o.public_id::text,o.title,o.type
FROM edges e JOIN observations o ON o.tenant_id=e.tenant_id AND o.id=CASE WHEN e.from_observation_id=ANY($1) THEN e.to_observation_id ELSE e.from_observation_id END
WHERE e.tenant_id=public.cortex_current_tenant() AND (e.from_observation_id=ANY($1) OR e.to_observation_id=ANY($1)) AND o.workspace_id=$2 AND o.project_id=$3 AND o.deleted_at IS NULL
AND e.assertion_status='accepted' AND e.fact_state='current' AND e.invalid_at IS NULL AND (e.valid_until IS NULL OR e.valid_until > now())`
			hopArgs := []any{frontier, workspace, internalProjectID}
			hopSQL, hopArgs = s.store.appendObservationVisibilityPredicate(hopSQL, hopArgs, true)
			if len(edgeSeen) > 0 {
				seen := make([]string, 0, len(edgeSeen))
				for edgeID := range edgeSeen {
					seen = append(seen, edgeID)
				}
				sort.Strings(seen)
				hopSQL += fmt.Sprintf(" AND NOT (e.public_id=ANY($%d::uuid[]))", len(hopArgs)+1)
				hopArgs = append(hopArgs, seen)
			}
			hopSQL += fmt.Sprintf(" ORDER BY e.public_id,o.public_id LIMIT $%d", len(hopArgs)+1)
			hopArgs = append(hopArgs, limit)
			return tx.Query(ctx, hopSQL, hopArgs...)
		}
		for hop := 1; hop <= maxHops && len(frontier) > 0; hop++ {
			remainingEdges := maxEdges - len(result.Edges)
			hopRows, hopErr := queryHop(frontier, remainingEdges+1)
			if hopErr != nil {
				return hopErr
			}
			next := []int64{}
			scanned := 0
			for hopRows.Next() {
				scanned++
				var edgeID, relation, publicID, title, typ string
				var from, to, neighborID int64
				var weight, confidence float64
				if scanErr := hopRows.Scan(&edgeID, &from, &to, &relation, &weight, &confidence, &neighborID, &publicID, &title, &typ); scanErr != nil {
					hopRows.Close()
					return scanErr
				}
				if edgeSeen[edgeID] {
					continue
				}
				if scanned > remainingEdges {
					result.Truncated = true
					continue
				}
				if _, exists := selected[neighborID]; !exists {
					if len(result.Nodes) >= maxNodes {
						result.Truncated = true
						continue
					}
					nodeID := "observation:" + publicID
					selected[neighborID] = nodeID
					next = append(next, neighborID)
					result.Nodes = append(result.Nodes, domain.GraphNode{ID: nodeID, Kind: "observation", Subtype: typ, Label: title, Project: projectPublicID, Hop: hop, Metadata: map[string]any{"observation_id": neighborID}})
				}
				source, sourceOK := selected[from]
				target, targetOK := selected[to]
				if sourceOK && targetOK {
					edgeSeen[edgeID] = true
					result.Edges = append(result.Edges, domain.GraphLink{ID: "edge:" + edgeID, Source: source, Target: target, Type: relation, Weight: weight, Confidence: confidence, AssertionStatus: "accepted"})
				}
			}
			if rowsErr := hopRows.Err(); rowsErr != nil {
				hopRows.Close()
				return rowsErr
			}
			hopRows.Close()
			frontier = next
		}
		if !result.Truncated && len(frontier) > 0 {
			probeRows, probeErr := queryHop(frontier, 1)
			if probeErr != nil {
				return probeErr
			}
			for probeRows.Next() {
				var edgeID, relation, publicID, title, typ string
				var from, to, neighborID int64
				var weight, confidence float64
				if scanErr := probeRows.Scan(&edgeID, &from, &to, &relation, &weight, &confidence, &neighborID, &publicID, &title, &typ); scanErr != nil {
					probeRows.Close()
					return scanErr
				}
				if !edgeSeen[edgeID] {
					result.Truncated = true
					break
				}
			}
			if rowsErr := probeRows.Err(); rowsErr != nil {
				probeRows.Close()
				return rowsErr
			}
			probeRows.Close()
		}
		sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
		sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].ID < result.Edges[j].ID })
		return nil
	})
	return result, err
}

// GetAgentCodeGraphSnapshot is the bounded AST acquisition counterpart. It
// reads only seed symbols and a limited relation frontier from the trusted
// project-bound code index; GetGraph's 100k-symbol materialization is avoided.
func (s *AuthorizedStore) GetAgentCodeGraphSnapshot(ctx context.Context, project, query string, maxHops, maxNodes, maxEdges int) (*code.CodeGraph, error) {
	if s == nil || s.store == nil || strings.TrimSpace(query) == "" {
		return nil, domain.ErrInvalidInput
	}
	projectID, err := normalizeCodeProject(project)
	if err != nil {
		return nil, err
	}
	project = projectID.String()
	maxHops, maxNodes, maxEdges = agentGraphBudgets(maxHops, maxNodes, maxEdges)
	patterns := agentCodeQueryPatterns(query)
	if len(patterns) == 0 {
		return nil, domain.ErrInvalidInput
	}
	if err := s.authorize(ctx, authz.ResourceCode, authz.ActionRead, project, "", ""); err != nil {
		return nil, err
	}
	result := &code.CodeGraph{Project: project, Symbols: []code.Symbol{}, Relations: []code.Relation{}}
	err = s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		codeStore, storeErr := newPostgresCodeStore(ctx, tx, s.store.tenant.WorkspaceID, project)
		if storeErr != nil {
			return storeErr
		}
		if storeErr = codeStore.requireReady(ctx); storeErr != nil {
			return storeErr
		}
		selected := map[string]code.Symbol{}
		frontier := []string{}
		seedLimit := min(maxNodes, 16)
		seedRows, queryErr := tx.Query(ctx, `SELECT id,project,file_path,line_number,end_line,kind,name,COALESCE(signature,''),COALESCE(doc_summary,'') FROM scoped_code_symbols WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3 AND (name ILIKE ANY($4) OR file_path ILIKE ANY($4) OR signature ILIKE ANY($4) OR doc_summary ILIKE ANY($4)) ORDER BY id LIMIT $5`, codeStore.scope.tenantID, codeStore.scope.workspaceID, codeStore.scope.projectID, patterns, seedLimit)
		if queryErr != nil {
			return queryErr
		}
		for seedRows.Next() {
			symbol, scanErr := scanAgentCodeSymbol(seedRows)
			if scanErr != nil {
				seedRows.Close()
				return scanErr
			}
			symbol.Metadata = map[string]any{"agent_seed": true}
			selected[symbol.ID] = symbol
		}
		if rowsErr := seedRows.Err(); rowsErr != nil {
			seedRows.Close()
			return rowsErr
		}
		seedRows.Close()
		frontier = frontier[:0]
		for id := range selected {
			frontier = append(frontier, id)
		}
		sort.Strings(frontier)
		relationSeen := map[string]bool{}
		for hop := 1; hop <= maxHops && len(frontier) > 0 && len(result.Relations) < maxEdges; hop++ {
			remainingEdges := maxEdges - len(result.Relations)
			rows, rowsErr := tx.Query(ctx, `SELECT r.id,r.project,r.source_id,r.target_id,r.relation,r.confidence,COALESCE(r.reasoning,''),o.id,o.project,o.file_path,o.line_number,o.end_line,o.kind,o.name,COALESCE(o.signature,''),COALESCE(o.doc_summary,'') FROM scoped_code_relations r JOIN scoped_code_symbols o ON o.tenant_id=r.tenant_id AND o.workspace_id=r.workspace_id AND o.project_id=r.project_id AND o.id=CASE WHEN r.source_id=ANY($4) THEN r.target_id ELSE r.source_id END WHERE r.tenant_id=$1 AND r.workspace_id=$2 AND r.project_id=$3 AND (r.source_id=ANY($4) OR r.target_id=ANY($4)) ORDER BY r.source_id,r.target_id,r.relation LIMIT $5`, codeStore.scope.tenantID, codeStore.scope.workspaceID, codeStore.scope.projectID, frontier, remainingEdges)
			if rowsErr != nil {
				return rowsErr
			}
			next := []string{}
			for rows.Next() {
				var relation code.Relation
				var neighbor code.Symbol
				if scanErr := rows.Scan(&relation.ID, &relation.Project, &relation.SourceID, &relation.TargetID, &relation.Relation, &relation.Confidence, &relation.Reasoning, &neighbor.ID, &neighbor.Project, &neighbor.FilePath, &neighbor.LineNumber, &neighbor.EndLine, &neighbor.Kind, &neighbor.Name, &neighbor.Signature, &neighbor.DocSummary); scanErr != nil {
					rows.Close()
					return scanErr
				}
				if _, exists := selected[neighbor.ID]; !exists {
					if len(selected) >= maxNodes {
						continue
					}
					selected[neighbor.ID] = neighbor
					next = append(next, neighbor.ID)
				}
				key := relation.SourceID + "\x1f" + relation.TargetID + "\x1f" + relation.Relation
				if !relationSeen[key] && len(result.Relations) < maxEdges {
					if _, a := selected[relation.SourceID]; a {
						if _, b := selected[relation.TargetID]; b {
							relationSeen[key] = true
							result.Relations = append(result.Relations, relation)
						}
					}
				}
			}
			if rowsErr := rows.Err(); rowsErr != nil {
				rows.Close()
				return rowsErr
			}
			rows.Close()
			sort.Strings(next)
			frontier = next
		}
		for _, symbol := range selected {
			result.Symbols = append(result.Symbols, symbol)
		}
		sort.Slice(result.Symbols, func(i, j int) bool { return result.Symbols[i].ID < result.Symbols[j].ID })
		sort.Slice(result.Relations, func(i, j int) bool {
			a, b := result.Relations[i], result.Relations[j]
			if a.SourceID != b.SourceID {
				return a.SourceID < b.SourceID
			}
			if a.TargetID != b.TargetID {
				return a.TargetID < b.TargetID
			}
			return a.Relation < b.Relation
		})
		return nil
	})
	return result, err
}

func scanAgentCodeSymbol(rows pgx.Rows) (code.Symbol, error) {
	var symbol code.Symbol
	err := rows.Scan(&symbol.ID, &symbol.Project, &symbol.FilePath, &symbol.LineNumber, &symbol.EndLine, &symbol.Kind, &symbol.Name, &symbol.Signature, &symbol.DocSummary)
	return symbol, err
}

func agentCodeQueryPatterns(query string) []string {
	unique := map[string]bool{}
	for _, term := range strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.' && r != ':'
	}) {
		if len(term) >= 3 {
			unique["%"+term+"%"] = true
		}
	}
	result := make([]string, 0, len(unique))
	for term := range unique {
		result = append(result, term)
	}
	sort.Strings(result)
	if len(result) > 8 {
		result = result[:8]
	}
	return result
}
