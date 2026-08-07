package postgres

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/internal/authz"
	"github.com/lleontor705/cortex/internal/domain"
)

// GetGraphSubgraph projects authorized aggregates into a bounded heterogeneous
// graph without duplicating users, sessions, projects, or entities into a
// generic source-of-truth table.
func (s *AuthorizedStore) GetGraphSubgraph(ctx context.Context, rootPublicID string, depth, maxNodes int) (*domain.GraphSubgraph, error) {
	if maxNodes <= 0 || maxNodes > 200 {
		maxNodes = 100
	}
	root, err := s.GetObservationByPublicID(ctx, rootPublicID)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, authz.ResourceGraph, authz.ActionRead, root.Project, "", ""); err != nil {
		return nil, err
	}
	related, err := s.GetRelatedObservations(ctx, root.ID, depth)
	if err != nil {
		return nil, err
	}
	observations := append([]*domain.Observation{root}, related...)
	result := &domain.GraphSubgraph{Root: "observation:" + root.PublicID, Nodes: make([]domain.GraphNode, 0, maxNodes), Edges: make([]domain.GraphLink, 0)}
	ids := make([]int64, 0, len(observations))
	byInternalID := make(map[int64]string, len(observations))
	hops := make(map[int64]int, len(observations))
	for index, observation := range observations {
		if len(result.Nodes) >= maxNodes {
			result.Truncated = true
			break
		}
		id := "observation:" + observation.PublicID
		hop := 1
		if index == 0 {
			hop = 0
		}
		result.Nodes = append(result.Nodes, domain.GraphNode{ID: id, Kind: "observation", Subtype: observation.Type, Label: observation.Title, Project: observation.Project, Hop: hop, Metadata: map[string]any{"scope": observation.Scope, "source": observation.Source, "created_at": observation.CreatedAt}})
		ids = append(ids, observation.ID)
		byInternalID[observation.ID] = id
		hops[observation.ID] = hop
	}

	err = s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.appendSemanticEdges(ctx, tx, ids, byInternalID, result); err != nil {
			return err
		}
		if err := s.appendEntityProjection(ctx, tx, ids, byInternalID, hops, maxNodes, result); err != nil {
			return err
		}
		return s.appendProvenanceProjection(ctx, tx, ids, byInternalID, maxNodes, result)
	})
	return result, err
}

func (s *AuthorizedStore) appendSemanticEdges(ctx context.Context, tx pgx.Tx, ids []int64, nodes map[int64]string, result *domain.GraphSubgraph) error {
	rows, err := tx.Query(ctx, `SELECT public_id::text,from_observation_id,to_observation_id,relation_type,weight,confidence,source,reasoning,assertion_kind,assertion_status,valid_from,valid_until FROM edges WHERE tenant_id=public.cortex_current_tenant() AND from_observation_id=ANY($1) AND to_observation_id=ANY($1) AND assertion_status='accepted'`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, relation, source, reasoning, kind, status string
		var from, to int64
		var weight, confidence float64
		var validFrom, validUntil any
		if err := rows.Scan(&id, &from, &to, &relation, &weight, &confidence, &source, &reasoning, &kind, &status, &validFrom, &validUntil); err != nil {
			return err
		}
		result.Edges = append(result.Edges, domain.GraphLink{ID: "edge:" + id, Source: nodes[from], Target: nodes[to], Type: relation, Weight: weight, Confidence: confidence, AssertionKind: kind, AssertionStatus: status, Metadata: map[string]any{"source": source, "reasoning": reasoning, "valid_from": validFrom, "valid_until": validUntil}})
	}
	return rows.Err()
}

func (s *AuthorizedStore) appendEntityProjection(ctx context.Context, tx pgx.Tx, ids []int64, observations map[int64]string, hops map[int64]int, maxNodes int, result *domain.GraphSubgraph) error {
	rows, err := tx.Query(ctx, `SELECT e.public_id::text,e.entity_type,e.entity_key,e.normalized_value,e.provenance,oe.observation_id,oe.confidence FROM observation_entities oe JOIN entities e ON e.tenant_id=oe.tenant_id AND e.id=oe.entity_id WHERE oe.tenant_id=public.cortex_current_tenant() AND oe.observation_id=ANY($1) ORDER BY e.entity_type,e.entity_key`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := make(map[string]bool)
	for rows.Next() {
		var publicID, typ, label, normalized, provenance string
		var observationID int64
		var confidence float64
		if err := rows.Scan(&publicID, &typ, &label, &normalized, &provenance, &observationID, &confidence); err != nil {
			return err
		}
		entityID := "entity:" + publicID
		if !seen[entityID] {
			if len(result.Nodes) >= maxNodes {
				result.Truncated = true
				continue
			}
			result.Nodes = append(result.Nodes, domain.GraphNode{ID: entityID, Kind: "entity", Subtype: typ, Label: label, Hop: hops[observationID] + 1, Metadata: map[string]any{"normalized_value": normalized, "provenance": provenance}})
			seen[entityID] = true
		}
		if seen[entityID] {
			result.Edges = append(result.Edges, domain.GraphLink{ID: "derived:mentions:" + strconv.FormatInt(observationID, 10) + ":" + publicID, Source: observations[observationID], Target: entityID, Type: "mentions", Confidence: confidence, AssertionKind: "deterministic", AssertionStatus: "accepted"})
		}
	}
	return rows.Err()
}

func (s *AuthorizedStore) appendProvenanceProjection(ctx context.Context, tx pgx.Tx, ids []int64, observations map[int64]string, maxNodes int, result *domain.GraphSubgraph) error {
	rows, err := tx.Query(ctx, `SELECT o.id,COALESCE(o.created_by::text,''),COALESCE(u.display_name,a.subject,''),se.public_id::text,COALESCE(o.project_key,'') FROM observations o JOIN sessions se ON se.tenant_id=o.tenant_id AND se.id=o.session_id LEFT JOIN actor_subjects a ON a.tenant_id=o.tenant_id AND a.public_id=o.created_by LEFT JOIN app_users u ON u.tenant_id=o.tenant_id AND u.public_id=o.created_by WHERE o.tenant_id=public.cortex_current_tenant() AND o.id=ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := make(map[string]bool)
	for rows.Next() {
		var observationID int64
		var actorID, actorLabel, sessionID, project string
		if err := rows.Scan(&observationID, &actorID, &actorLabel, &sessionID, &project); err != nil {
			return err
		}
		for _, node := range []domain.GraphNode{{ID: "actor:" + actorID, Kind: "actor", Subtype: "user", Label: actorLabel}, {ID: "session:" + sessionID, Kind: "session", Label: "Session " + sessionID[:8]}, {ID: "project:" + project, Kind: "project", Label: project}} {
			if node.ID == "actor:" || node.ID == "project:" || seen[node.ID] {
				continue
			}
			if len(result.Nodes) >= maxNodes {
				result.Truncated = true
				continue
			}
			result.Nodes = append(result.Nodes, node)
			seen[node.ID] = true
		}
		observationNode := observations[observationID]
		if actorID != "" && seen["actor:"+actorID] {
			result.Edges = append(result.Edges, domain.GraphLink{ID: fmt.Sprintf("derived:created_by:%d", observationID), Source: observationNode, Target: "actor:" + actorID, Type: "created_by", AssertionKind: "derived", AssertionStatus: "accepted"})
		}
		if seen["session:"+sessionID] {
			result.Edges = append(result.Edges, domain.GraphLink{ID: fmt.Sprintf("derived:produced_in:%d", observationID), Source: observationNode, Target: "session:" + sessionID, Type: "produced_in", AssertionKind: "derived", AssertionStatus: "accepted"})
		}
		if project != "" && seen["project:"+project] {
			result.Edges = append(result.Edges, domain.GraphLink{ID: fmt.Sprintf("derived:belongs_to:%d", observationID), Source: observationNode, Target: "project:" + project, Type: "belongs_to", AssertionKind: "derived", AssertionStatus: "accepted"})
		}
	}
	return rows.Err()
}
