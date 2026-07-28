package obsidian

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"gopkg.in/yaml.v3"
)

// FrontmatterOptions supplies projection metadata that is not stored on an observation.
type FrontmatterOptions struct {
	Aliases    []string
	Provenance string
	Lifecycle  string
	Revision   int
}

type frontmatter struct {
	CortexID   string            `yaml:"cortex_id"`
	Project    string            `yaml:"project"`
	Scope      string            `yaml:"scope"`
	Type       string            `yaml:"type"`
	Tags       []string          `yaml:"tags,omitempty"`
	Aliases    []string          `yaml:"aliases,omitempty"`
	Provenance string            `yaml:"provenance"`
	Lifecycle  lifecycleMetadata `yaml:"lifecycle"`
	Temporal   temporalMetadata  `yaml:"temporal"`
	Revision   revisionMetadata  `yaml:"revision"`
}
type lifecycleMetadata struct {
	Status string `yaml:"status"`
}
type temporalMetadata struct {
	CreatedAt string `yaml:"created_at"`
	UpdatedAt string `yaml:"updated_at"`
}
type revisionMetadata struct {
	Number int `yaml:"number"`
}

// RenderFrontmatter renders a complete YAML frontmatter block. It deliberately
// uses yaml.v3 structs so scalars are quoted/escaped safely and deterministically.
func RenderFrontmatter(obs *domain.Observation, opts FrontmatterOptions) (string, error) {
	if obs == nil || obs.ID <= 0 {
		return "", fmt.Errorf("obsidian: cortex_id is required")
	}
	status := opts.Lifecycle
	if status == "" {
		status = "active"
	}
	prov := opts.Provenance
	if prov == "" {
		prov = obs.Source
		if prov == "" {
			prov = "cortex"
		}
	}
	data, err := yaml.Marshal(frontmatter{CortexID: strconv.FormatInt(obs.ID, 10), Project: obs.Project, Scope: obs.Scope, Type: obs.Type, Tags: obs.Tags, Aliases: opts.Aliases, Provenance: prov, Lifecycle: lifecycleMetadata{Status: status}, Temporal: temporalMetadata{CreatedAt: formatTime(obs.CreatedAt), UpdatedAt: formatTime(obs.UpdatedAt)}, Revision: revisionMetadata{Number: opts.Revision}})
	if err != nil {
		return "", fmt.Errorf("obsidian: marshal frontmatter: %w", err)
	}
	return "---\n" + string(data) + "---\n\n", nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseFrontmatter(content string) (frontmatter, error) {
	var fm frontmatter
	if !strings.HasPrefix(content, "---\n") {
		return fm, fmt.Errorf("obsidian: frontmatter missing")
	}
	rest := strings.TrimPrefix(content, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fm, fmt.Errorf("obsidian: frontmatter missing terminator")
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
		return fm, fmt.Errorf("obsidian: invalid frontmatter: %w", err)
	}
	if fm.CortexID == "" {
		return fm, fmt.Errorf("obsidian: cortex_id missing")
	}
	return fm, nil
}
