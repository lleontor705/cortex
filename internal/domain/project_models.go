package domain

import (
	"time"
)

// ProjectRule represents a corporate or project governance rule / system prompt.
type ProjectRule struct {
	Key     string `json:"key"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Scope   string `json:"scope"` // "project" or "workspace_default"
}

// ProjectSkill represents a specialized workflow, guideline or skill.
type ProjectSkill struct {
	ID          string         `json:"id"`
	Key         string         `json:"key"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Content     string         `json:"content"`
	Scope       string         `json:"scope"`
	Project     string         `json:"project,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Revision    int64          `json:"revision"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// ProjectSkillSummary is a lightweight overview of an available skill.
type ProjectSkillSummary struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	Project     string `json:"project,omitempty"`
}

// ProjectContext aggregates corporate governance rules and available skills.
type ProjectContext struct {
	Project      string                `json:"project"`
	SystemPrompt string                `json:"system_prompt"`
	Rules        []ProjectRule         `json:"rules"`
	Skills       []ProjectSkillSummary `json:"skills"`
}

// ProjectArtifactItem represents a rule or skill artifact row for administration.
type ProjectArtifactItem struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"` // "rule" or "skill"
	Key         string         `json:"key"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Content     string         `json:"content"`
	Scope       string         `json:"scope"` // "project" or "workspace_default"
	Project     string         `json:"project,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Revision    int64          `json:"revision"`
	Status      string         `json:"status"` // "active" or "deleted"
	UpdatedAt   time.Time      `json:"updated_at"`
}

// SaveProjectArtifactInput is the input for creating or updating a project artifact.
type SaveProjectArtifactInput struct {
	Kind        string         `json:"kind"` // "rule" or "skill"
	Key         string         `json:"key"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Content     string         `json:"content"`
	Scope       string         `json:"scope"` // "project" or "workspace_default"
	Project     string         `json:"project,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}
