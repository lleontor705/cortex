// Package dna generates Project DNA — a structured summary of a project's
// key decisions, patterns, tech stack, and gotchas extracted from observations.
//
// The DNA is auto-generated from high-importance observations and persisted
// as an observation with topic_key "project-dna/{project}".
package dna

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// ObservationLister lists observations with filters.
type ObservationLister interface {
	List(ctx context.Context, filter domain.ObservationFilter) ([]*domain.Observation, error)
}

// ScoreProvider retrieves importance scores.
type ScoreProvider interface {
	GetScore(ctx context.Context, obsID int64) (*domain.ImportanceScore, error)
}

// EdgeCounter counts graph edges.
type EdgeCounter interface {
	CountEdgesByObservation(ctx context.Context, obsID int64) (int, error)
}

// BatchScoreProvider is an optional capability that hydrates importance
// scores for many observations with a single store call.
type BatchScoreProvider interface {
	GetScoresByObservationIDs(ctx context.Context, obsIDs []int64) (map[int64]*domain.ImportanceScore, error)
}

// BatchEdgeCounter is an optional capability that counts connected edges for
// many observations with a single store call.
type BatchEdgeCounter interface {
	CountEdgesByObservationIDs(ctx context.Context, obsIDs []int64) (map[int64]int, error)
}

// Service generates Project DNA from observations.
type Service struct {
	observations ObservationLister
	scoring      ScoreProvider
	edges        EdgeCounter
}

// NewService creates a new DNA service.
func NewService(obs ObservationLister, scoring ScoreProvider, edges EdgeCounter) *Service {
	return &Service{observations: obs, scoring: scoring, edges: edges}
}

// scored wraps an observation with its importance score.
type scored struct {
	obs   *domain.Observation
	score float64
	edges int
}

// defaultScore is the base score applied when no importance score exists.
const defaultScore = 0.5

// Generate creates a Project DNA markdown summary for the given project.
func (s *Service) Generate(ctx context.Context, project string) (string, error) {
	// Fetch all observations for the project
	obs, err := s.observations.List(ctx, domain.ObservationFilter{
		Project: project,
		Limit:   500,
	})
	if err != nil {
		return "", fmt.Errorf("dna: list observations: %w", err)
	}

	if len(obs) == 0 {
		return fmt.Sprintf("# Project DNA: %s\n\nNo observations found.", project), nil
	}

	// Hydrate scores and edge counts (batched when the optional capabilities
	// are available, per-item otherwise).
	ids := make([]int64, len(obs))
	for i, o := range obs {
		ids[i] = o.ID
	}
	scores := s.fetchScores(ctx, ids)
	edgeCounts := s.fetchEdgeCounts(ctx, ids)

	items := make([]scored, 0, len(obs))
	for _, o := range obs {
		sc := scored{obs: o, score: defaultScore} // base score
		if v, ok := scores[o.ID]; ok {
			sc.score = v
		}
		if cnt, ok := edgeCounts[o.ID]; ok {
			sc.edges = cnt
		}
		items = append(items, sc)
	}

	// Total deterministic order: score descending, then created_at
	// descending, then observation ID descending. Equivalent rows therefore
	// render byte-identical markdown independent of insertion/query order.
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if !a.obs.CreatedAt.Equal(b.obs.CreatedAt) {
			return a.obs.CreatedAt.After(b.obs.CreatedAt)
		}
		return a.obs.ID > b.obs.ID
	})

	// Group by type
	byType := make(map[string][]scored, 5)
	for _, it := range items {
		byType[it.obs.Type] = append(byType[it.obs.Type], it)
	}

	// Build DNA markdown
	var b strings.Builder
	// The summary is dominated by the bounded per-type previews. Reserving a
	// useful lower bound avoids repeated growth for the common N=500 path while
	// retaining the exact output and its deterministic ordering.
	b.Grow(128 + len(obs)*24)
	fmt.Fprintf(&b, "# Project DNA: %s\n\n", project)
	fmt.Fprintf(&b, "Auto-generated from %d observations.\n\n", len(obs))

	// Key Decisions
	if decisions := byType[domain.TypeDecision]; len(decisions) > 0 {
		b.WriteString("## Key Decisions\n\n")
		for i, d := range decisions {
			if i >= 10 {
				break
			}
			fmt.Fprintf(&b, "- **%s** (score: %.1f)\n", d.obs.Title, d.score)
			if preview := firstLine(d.obs.Content); preview != "" {
				fmt.Fprintf(&b, "  %s\n", preview)
			}
		}
		b.WriteString("\n")
	}

	// Patterns
	if patterns := byType[domain.TypePattern]; len(patterns) > 0 {
		b.WriteString("## Patterns & Conventions\n\n")
		for i, p := range patterns {
			if i >= 10 {
				break
			}
			fmt.Fprintf(&b, "- **%s**\n", p.obs.Title)
			if preview := firstLine(p.obs.Content); preview != "" {
				fmt.Fprintf(&b, "  %s\n", preview)
			}
		}
		b.WriteString("\n")
	}

	// Config
	if configs := byType[domain.TypeConfig]; len(configs) > 0 {
		b.WriteString("## Configuration & Stack\n\n")
		for i, c := range configs {
			if i >= 8 {
				break
			}
			fmt.Fprintf(&b, "- %s\n", c.obs.Title)
		}
		b.WriteString("\n")
	}

	// Bugfixes (gotchas)
	if bugfixes := byType[domain.TypeBugfix]; len(bugfixes) > 0 {
		b.WriteString("## Known Gotchas\n\n")
		for i, bf := range bugfixes {
			if i >= 8 {
				break
			}
			fmt.Fprintf(&b, "- **%s** (score: %.1f)\n", bf.obs.Title, bf.score)
			if preview := firstLine(bf.obs.Content); preview != "" {
				fmt.Fprintf(&b, "  %s\n", preview)
			}
		}
		b.WriteString("\n")
	}

	// Discoveries
	if discoveries := byType[domain.TypeDiscovery]; len(discoveries) > 0 {
		b.WriteString("## Discoveries\n\n")
		for i, d := range discoveries {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "- %s\n", d.obs.Title)
		}
		b.WriteString("\n")
	}

	// Stats footer
	fmt.Fprintf(&b, "---\n\n")
	fmt.Fprintf(&b, "**Stats:** %d observations", len(obs))
	typeList := make([]string, 0, len(byType))
	for t, items := range byType {
		typeList = append(typeList, fmt.Sprintf("%s: %d", t, len(items)))
	}
	sort.Strings(typeList)
	fmt.Fprintf(&b, " (%s)\n", strings.Join(typeList, ", "))

	return b.String(), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx > 0 {
		s = s[:idx]
	}
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

// fetchScores hydrates importance scores for the given observation IDs.
// When the scoring provider implements BatchScoreProvider, one batch call
// replaces the per-ID loop; a batch error falls back to the tolerant
// per-item lookups so output remains identical to the legacy path.
// IDs missing from the result keep the default score.
func (s *Service) fetchScores(ctx context.Context, ids []int64) map[int64]float64 {
	if s.scoring == nil || len(ids) == 0 {
		return nil
	}
	if batch, ok := s.scoring.(BatchScoreProvider); ok {
		if rows, err := batch.GetScoresByObservationIDs(ctx, ids); err == nil {
			out := make(map[int64]float64, len(rows))
			for id, is := range rows {
				if is != nil {
					out[id] = is.Score
				}
			}
			return out
		}
		// Batch failed: degrade to the compatible per-item path.
	}
	out := make(map[int64]float64, len(ids))
	for _, id := range ids {
		if is, err := s.scoring.GetScore(ctx, id); err == nil && is != nil {
			out[id] = is.Score
		}
	}
	return out
}

// fetchEdgeCounts hydrates connected-edge counts for the given observation
// IDs. When the edge provider implements BatchEdgeCounter, one batch call
// replaces the per-ID loop; a batch error falls back to the tolerant
// per-item counts. IDs missing from the result keep a zero count.
func (s *Service) fetchEdgeCounts(ctx context.Context, ids []int64) map[int64]int {
	if s.edges == nil || len(ids) == 0 {
		return nil
	}
	if batch, ok := s.edges.(BatchEdgeCounter); ok {
		if counts, err := batch.CountEdgesByObservationIDs(ctx, ids); err == nil {
			return counts
		}
		// Batch failed: degrade to the compatible per-item path.
	}
	out := make(map[int64]int, len(ids))
	for _, id := range ids {
		if cnt, err := s.edges.CountEdgesByObservation(ctx, id); err == nil {
			out[id] = cnt
		}
	}
	return out
}
