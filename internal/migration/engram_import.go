package migration

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lleontor705/cortex/internal/domain"

	_ "modernc.org/sqlite"
)

// EngramImportResult describes what was imported from an Engram database.
type EngramImportResult struct {
	Sessions     int
	Observations int
	Prompts      int
}

// EngramImportTarget groups the repositories needed by the importer.
type EngramImportTarget struct {
	Observations domain.ObservationRepository
	Sessions     domain.SessionRepository
	Prompts      domain.PromptRepository
}

// ImportFromEngram imports sessions, observations, and prompts from an Engram SQLite database.
func ImportFromEngram(ctx context.Context, path string, target EngramImportTarget) (*EngramImportResult, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open engram db: %w", err)
	}
	defer db.Close()

	result := &EngramImportResult{}

	if err := importSessions(ctx, db, target, result); err != nil {
		return nil, err
	}
	if err := importObservations(ctx, db, target, result); err != nil {
		return nil, err
	}
	if err := importPrompts(ctx, db, target, result); err != nil {
		return nil, err
	}

	return result, nil
}

func importSessions(ctx context.Context, db *sql.DB, target EngramImportTarget, result *EngramImportResult) error {
	rows, err := db.QueryContext(ctx, `SELECT id, project, directory, started_at, ended_at, summary FROM sessions`)
	if err != nil {
		return fmt.Errorf("import sessions query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, project, directory, startedAt string
		var endedAt, summary sql.NullString
		if err := rows.Scan(&id, &project, &directory, &startedAt, &endedAt, &summary); err != nil {
			return fmt.Errorf("import sessions scan: %w", err)
		}

		if _, err := target.Sessions.GetByID(ctx, id); err == nil {
			continue
		}

		session := &domain.Session{
			ID:        id,
			Project:   project,
			Directory: directory,
			StartedAt: parseImportTime(startedAt),
			Summary:   summary.String,
		}

		if err := target.Sessions.Create(ctx, session); err != nil {
			return fmt.Errorf("import session %s: %w", id, err)
		}
		if endedAt.Valid {
			_ = target.Sessions.End(ctx, id, summary.String)
		}
		result.Sessions++
	}

	return rows.Err()
}

func importObservations(ctx context.Context, db *sql.DB, target EngramImportTarget, result *EngramImportResult) error {
	rows, err := db.QueryContext(ctx, `SELECT session_id, type, title, content, project, scope, topic_key, created_at, updated_at FROM observations WHERE deleted_at IS NULL`)
	if err != nil {
		return fmt.Errorf("import observations query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sessionID, typ, title, content, createdAt, updatedAt string
		var project, scope, topicKey sql.NullString
		if err := rows.Scan(&sessionID, &typ, &title, &content, &project, &scope, &topicKey, &createdAt, &updatedAt); err != nil {
			return fmt.Errorf("import observations scan: %w", err)
		}

		obs := &domain.Observation{
			SessionID: sessionID,
			Type:      typ,
			Title:     title,
			Content:   content,
			Project:   project.String,
			Scope:     scope.String,
			TopicKey:  topicKey.String,
			CreatedAt: parseImportTime(createdAt),
			UpdatedAt: parseImportTime(updatedAt),
		}
		if err := target.Observations.Save(ctx, obs); err != nil {
			return fmt.Errorf("import observation %q: %w", title, err)
		}
		result.Observations++
	}

	return rows.Err()
}

func importPrompts(ctx context.Context, db *sql.DB, target EngramImportTarget, result *EngramImportResult) error {
	rows, err := db.QueryContext(ctx, `SELECT session_id, content, project, created_at FROM user_prompts`)
	if err != nil {
		return fmt.Errorf("import prompts query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sessionID, content, createdAt string
		var project sql.NullString
		if err := rows.Scan(&sessionID, &content, &project, &createdAt); err != nil {
			return fmt.Errorf("import prompts scan: %w", err)
		}

		p := &domain.Prompt{
			SessionID: sessionID,
			Content:   content,
			Project:   project.String,
			CreatedAt: parseImportTime(createdAt),
		}
		if err := target.Prompts.Save(ctx, p); err != nil {
			return fmt.Errorf("import prompt: %w", err)
		}
		result.Prompts++
	}

	return rows.Err()
}

func parseImportTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Now().UTC()
}
