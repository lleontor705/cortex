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

	"github.com/lleontor705/cortex/internal/domain"
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

	// Score and sort observations
	var items []scored
	for _, o := range obs {
		sc := scored{obs: o, score: 0.5} // base score
		if s.scoring != nil {
			if is, err := s.scoring.GetScore(ctx, o.ID); err == nil && is != nil {
				sc.score = is.Score
			}
		}
		if s.edges != nil {
			if cnt, err := s.edges.CountEdgesByObservation(ctx, o.ID); err == nil {
				sc.edges = cnt
			}
		}
		items = append(items, sc)
	}

	sort.Slice(items, func(i, j int) bool { return items[i].score > items[j].score })

	// Group by type
	byType := make(map[string][]scored)
	for _, it := range items {
		byType[it.obs.Type] = append(byType[it.obs.Type], it)
	}

	// Build DNA markdown
	var b strings.Builder
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
