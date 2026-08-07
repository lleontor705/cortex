package identity

import "time"

// UserCreate contains administrator-controlled identity data. Authority is
// resolved from these persisted grants, never copied from ordinary requests.
type UserCreate struct {
	Email                   string
	DisplayName             string
	Roles                   []string
	Workspaces              []string
	Projects                []string
	Scopes                  []string
	ClassificationClearance []string
}

type UserRecord struct {
	ID                      string
	Email                   string
	DisplayName             string
	Active                  bool
	Roles                   []string
	Workspaces              []string
	Projects                []string
	Scopes                  []string
	ClassificationClearance []string
	GrantVersion            int64
	CreatedAt               time.Time
}
