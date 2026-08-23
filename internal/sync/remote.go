package sync

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/transportpolicy"
)

const (
	remoteBatchLimit = 100
	pushBatchLimit   = 25
)

type RemoteResult struct {
	Pushed int   `json:"pushed"`
	Pulled int   `json:"pulled"`
	Cursor int64 `json:"cursor"`
}

// RemoteSyncer replicates the local SQLite database through the authenticated
// server API. Re-sending the full local set is intentional: server client IDs
// and timestamps make retries idempotent without a lossy fire-and-forget queue.
type RemoteSyncer struct {
	db      *sql.DB
	baseURL string
	token   string
	client  *http.Client
}

func NewRemoteSyncer(db *sql.DB, baseURL, token string, timeout time.Duration) (*RemoteSyncer, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || u.Host == "" {
		return nil, errors.New("remote sync: invalid server URL")
	}
	// Enforce the shared Bearer transport policy (REM-TRANSPORT-001) before
	// any request — and therefore before any Authorization header — exists:
	// HTTPS for non-loopback destinations, plain HTTP only on strict
	// loopback.
	if err := transportpolicy.ValidateBearerDestination(u.String()); err != nil {
		return nil, fmt.Errorf("remote sync: %w", err)
	}
	if db == nil || strings.TrimSpace(token) == "" {
		return nil, errors.New("remote sync: database and token are required")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &RemoteSyncer{
		db:      db,
		baseURL: u.String(),
		token:   token,
		client: &http.Client{
			Timeout:       timeout,
			CheckRedirect: transportpolicy.CheckBearerRedirect,
		},
	}, nil
}

func (s *RemoteSyncer) Sync(ctx context.Context) (*RemoteResult, error) {
	if err := s.ensureState(ctx); err != nil {
		return nil, err
	}
	result := &RemoteResult{}
	cursor, err := s.cursor(ctx)
	if err != nil {
		return nil, err
	}
	for {
		var page domain.SyncPage
		path := "/api/sync/changes?cursor=" + strconv.FormatInt(cursor, 10) + "&limit=" + strconv.Itoa(remoteBatchLimit)
		if err = s.request(ctx, http.MethodGet, path, nil, &page); err != nil {
			return nil, err
		}
		if err = s.apply(ctx, &page); err != nil {
			return nil, err
		}
		result.Pulled += countBatch(&page.SyncBatch)
		cursor = page.Cursor
		if !page.HasMore {
			break
		}
	}
	result.Cursor = cursor
	batch, err := s.export(ctx)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(batch.Sessions); start += pushBatchLimit {
		end := min(start+pushBatchLimit, len(batch.Sessions))
		if err = s.push(ctx, &domain.SyncBatch{Sessions: batch.Sessions[start:end]}, result); err != nil {
			return nil, err
		}
	}
	for start := 0; start < len(batch.Observations); start += pushBatchLimit {
		end := min(start+pushBatchLimit, len(batch.Observations))
		if err = s.push(ctx, &domain.SyncBatch{Observations: batch.Observations[start:end]}, result); err != nil {
			return nil, err
		}
	}
	for start := 0; start < len(batch.Prompts); start += pushBatchLimit {
		end := min(start+pushBatchLimit, len(batch.Prompts))
		if err = s.push(ctx, &domain.SyncBatch{Prompts: batch.Prompts[start:end]}, result); err != nil {
			return nil, err
		}
	}
	for start := 0; start < len(batch.Edges); start += pushBatchLimit {
		end := min(start+pushBatchLimit, len(batch.Edges))
		if err = s.push(ctx, &domain.SyncBatch{Edges: batch.Edges[start:end]}, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *RemoteSyncer) push(ctx context.Context, batch *domain.SyncBatch, total *RemoteResult) error {
	var pushed domain.SyncResult
	if err := s.request(ctx, http.MethodPost, "/api/sync/push", batch, &pushed); err != nil {
		return err
	}
	total.Pushed += pushed.Accepted
	return nil
}

func countBatch(b *domain.SyncBatch) int {
	if b == nil {
		return 0
	}
	return len(b.Sessions) + len(b.Observations) + len(b.Prompts) + len(b.Edges)
}

func (s *RemoteSyncer) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("remote sync: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("remote sync: server returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if output != nil {
		if err := json.NewDecoder(resp.Body).Decode(output); err != nil {
			return fmt.Errorf("remote sync: decode response: %w", err)
		}
	}
	return nil
}

func (s *RemoteSyncer) ensureState(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS remote_sync_state (key TEXT PRIMARY KEY,value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS remote_sync_sessions (local_id TEXT PRIMARY KEY,sync_id TEXT NOT NULL UNIQUE,updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS remote_sync_edges (local_id INTEGER PRIMARY KEY,sync_id TEXT NOT NULL UNIQUE,from_sync_id TEXT NOT NULL,to_sync_id TEXT NOT NULL,relation_type TEXT NOT NULL,weight REAL NOT NULL,confidence REAL NOT NULL,source TEXT,reasoning TEXT,valid_from TEXT,valid_until TEXT,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,deleted INTEGER NOT NULL DEFAULT 0)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS observations_sync_id_uq ON observations(sync_id) WHERE sync_id IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS prompts_sync_id_uq ON user_prompts(sync_id) WHERE sync_id IS NOT NULL`,
		`DROP TRIGGER IF EXISTS obs_fts_update`,
		`CREATE TRIGGER obs_fts_update AFTER UPDATE OF title,content,tool_name,type,project,scope,topic_key ON observations BEGIN INSERT INTO observations_fts(observations_fts,rowid,title,content,tool_name,type,project,scope,topic_key) VALUES('delete',old.id,old.title,old.content,old.tool_name,old.type,old.project,old.scope,old.topic_key); INSERT INTO observations_fts(rowid,title,content,tool_name,type,project,scope,topic_key) VALUES(new.id,new.title,COALESCE(new.type,'') || ' ' || COALESCE(new.topic_key,'') || ' ' || COALESCE(new.project,'') || ' ' || new.content,new.tool_name,new.type,new.project,new.scope,new.topic_key); END`,
		`DROP TRIGGER IF EXISTS prompt_fts_update`,
		`CREATE TRIGGER prompt_fts_update AFTER UPDATE OF content,project ON user_prompts BEGIN INSERT INTO prompts_fts(prompts_fts,rowid,content,project) VALUES('delete',old.id,old.content,old.project); INSERT INTO prompts_fts(rowid,content,project) VALUES(new.id,new.content,new.project); END`,
		`INSERT OR IGNORE INTO remote_sync_sessions(local_id,sync_id,updated_at) SELECT id,id,COALESCE(ended_at,started_at) FROM sessions`,
		`UPDATE observations SET sync_id=lower(hex(randomblob(16))) WHERE sync_id IS NULL OR sync_id=''`,
		`UPDATE user_prompts SET sync_id=lower(hex(randomblob(16))) WHERE sync_id IS NULL OR sync_id=''`,
		`INSERT OR IGNORE INTO remote_sync_edges(local_id,sync_id,from_sync_id,to_sync_id,relation_type,weight,confidence,source,reasoning,valid_from,valid_until,created_at,updated_at) SELECT e.id,lower(hex(randomblob(16))),a.sync_id,b.sync_id,e.relation_type,e.weight,e.confidence,e.source,e.reasoning,e.valid_from,e.valid_until,e.created_at,e.created_at FROM edges e JOIN observations a ON a.id=e.from_obs_id JOIN observations b ON b.id=e.to_obs_id`,
		`CREATE TRIGGER IF NOT EXISTS remote_sync_session_insert AFTER INSERT ON sessions BEGIN INSERT OR IGNORE INTO remote_sync_sessions(local_id,sync_id,updated_at) VALUES(NEW.id,NEW.id,COALESCE(NEW.ended_at,NEW.started_at)); END`,
		`CREATE TRIGGER IF NOT EXISTS remote_sync_session_update AFTER UPDATE ON sessions BEGIN UPDATE remote_sync_sessions SET updated_at=datetime('now') WHERE local_id=NEW.id; END`,
		`CREATE TRIGGER IF NOT EXISTS remote_sync_observation_insert AFTER INSERT ON observations WHEN NEW.sync_id IS NULL OR NEW.sync_id='' BEGIN UPDATE observations SET sync_id=lower(hex(randomblob(16))) WHERE id=NEW.id; END`,
		`CREATE TRIGGER IF NOT EXISTS remote_sync_prompt_insert AFTER INSERT ON user_prompts WHEN NEW.sync_id IS NULL OR NEW.sync_id='' BEGIN UPDATE user_prompts SET sync_id=lower(hex(randomblob(16))) WHERE id=NEW.id; END`,
		`CREATE TRIGGER IF NOT EXISTS remote_sync_edge_insert AFTER INSERT ON edges BEGIN INSERT OR IGNORE INTO remote_sync_edges(local_id,sync_id,from_sync_id,to_sync_id,relation_type,weight,confidence,source,reasoning,valid_from,valid_until,created_at,updated_at) VALUES(NEW.id,lower(hex(randomblob(16))),(SELECT sync_id FROM observations WHERE id=NEW.from_obs_id),(SELECT sync_id FROM observations WHERE id=NEW.to_obs_id),NEW.relation_type,NEW.weight,NEW.confidence,NEW.source,NEW.reasoning,NEW.valid_from,NEW.valid_until,NEW.created_at,NEW.created_at); END`,
		`CREATE TRIGGER IF NOT EXISTS remote_sync_edge_update AFTER UPDATE ON edges BEGIN UPDATE remote_sync_edges SET from_sync_id=(SELECT sync_id FROM observations WHERE id=NEW.from_obs_id),to_sync_id=(SELECT sync_id FROM observations WHERE id=NEW.to_obs_id),relation_type=NEW.relation_type,weight=NEW.weight,confidence=NEW.confidence,source=NEW.source,reasoning=NEW.reasoning,valid_from=NEW.valid_from,valid_until=NEW.valid_until,updated_at=datetime('now'),deleted=0 WHERE local_id=NEW.id; END`,
		`CREATE TRIGGER IF NOT EXISTS remote_sync_edge_delete BEFORE DELETE ON edges BEGIN UPDATE remote_sync_edges SET updated_at=datetime('now'),deleted=1 WHERE local_id=OLD.id; END`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("remote sync state: %w", err)
		}
	}
	var repaired string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM remote_sync_state WHERE key='fts_sync_trigger_version'`).Scan(&repaired)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = s.db.ExecContext(ctx, `INSERT INTO observations_fts(observations_fts) VALUES('rebuild')`); err != nil {
			return fmt.Errorf("remote sync rebuild observations FTS: %w", err)
		}
		if _, err = s.db.ExecContext(ctx, `INSERT INTO prompts_fts(prompts_fts) VALUES('rebuild')`); err != nil {
			return fmt.Errorf("remote sync rebuild prompts FTS: %w", err)
		}
		if _, err = s.db.ExecContext(ctx, `INSERT INTO remote_sync_state(key,value) VALUES('fts_sync_trigger_version','1')`); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

func (s *RemoteSyncer) export(ctx context.Context) (*domain.SyncBatch, error) {
	b := new(domain.SyncBatch)
	rows, err := s.db.QueryContext(ctx, `SELECT m.sync_id,s.project,s.directory,s.started_at,s.ended_at,COALESCE(s.summary,''),m.updated_at FROM sessions s JOIN remote_sync_sessions m ON m.local_id=s.id ORDER BY s.started_at`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v domain.SyncSession
		var started string
		var updated string
		var ended sql.NullString
		if err = rows.Scan(&v.SyncID, &v.Project, &v.Directory, &started, &ended, &v.Summary, &updated); err != nil {
			_ = rows.Close()
			return nil, err
		}
		v.StartedAt = parseSyncTime(started)
		v.UpdatedAt = parseSyncTime(updated)
		if ended.Valid {
			t := parseSyncTime(ended.String)
			v.EndedAt = &t
		}
		b.Sessions = append(b.Sessions, v)
	}
	_ = rows.Close()
	rows, err = s.db.QueryContext(ctx, `SELECT o.sync_id,m.sync_id,o.title,o.content,o.type,COALESCE(o.project,''),o.scope,COALESCE(o.topic_key,''),o.confidence,o.source,COALESCE(o.tags,'[]'),o.created_at,o.updated_at,o.deleted_at FROM observations o JOIN remote_sync_sessions m ON m.local_id=o.session_id ORDER BY o.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v domain.SyncObservation
		var tags, created, updated string
		var deleted sql.NullString
		if err = rows.Scan(&v.SyncID, &v.SessionSyncID, &v.Title, &v.Content, &v.Type, &v.Project, &v.Scope, &v.TopicKey, &v.Confidence, &v.Source, &tags, &created, &updated, &deleted); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(tags), &v.Tags)
		v.CreatedAt = parseSyncTime(created)
		v.UpdatedAt = parseSyncTime(updated)
		v.Deleted = deleted.Valid
		b.Observations = append(b.Observations, v)
	}
	_ = rows.Close()
	rows, err = s.db.QueryContext(ctx, `SELECT p.sync_id,m.sync_id,p.content,COALESCE(p.project,''),p.created_at FROM user_prompts p JOIN remote_sync_sessions m ON m.local_id=p.session_id ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v domain.SyncPrompt
		var created string
		if err = rows.Scan(&v.SyncID, &v.SessionSyncID, &v.Content, &v.Project, &created); err != nil {
			_ = rows.Close()
			return nil, err
		}
		v.CreatedAt = parseSyncTime(created)
		v.UpdatedAt = v.CreatedAt
		b.Prompts = append(b.Prompts, v)
	}
	_ = rows.Close()
	rows, err = s.db.QueryContext(ctx, `SELECT sync_id,from_sync_id,to_sync_id,relation_type,weight,confidence,COALESCE(source,''),COALESCE(reasoning,''),valid_from,valid_until,created_at,updated_at,deleted FROM remote_sync_edges ORDER BY local_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v domain.SyncEdge
		var validFrom, validUntil sql.NullString
		var created, updated string
		if err = rows.Scan(&v.SyncID, &v.FromSyncID, &v.ToSyncID, &v.Relation, &v.Weight, &v.Confidence, &v.Source, &v.Reasoning, &validFrom, &validUntil, &created, &updated, &v.Deleted); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if validFrom.Valid {
			t := parseSyncTime(validFrom.String)
			v.ValidFrom = &t
		}
		if validUntil.Valid {
			t := parseSyncTime(validUntil.String)
			v.ValidUntil = &t
		}
		v.CreatedAt = parseSyncTime(created)
		v.UpdatedAt = parseSyncTime(updated)
		b.Edges = append(b.Edges, v)
	}
	_ = rows.Close()
	return b, nil
}

func (s *RemoteSyncer) cursor(ctx context.Context) (int64, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM remote_sync_state WHERE key='cursor'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(raw, 10, 64)
}

func (s *RemoteSyncer) apply(ctx context.Context, page *domain.SyncPage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, v := range page.Sessions {
		localID := v.SyncID
		var existing string
		err = tx.QueryRowContext(ctx, `SELECT local_id FROM remote_sync_sessions WHERE sync_id=?`, v.SyncID).Scan(&existing)
		if err == nil {
			localID = existing
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var ended any
		if v.EndedAt != nil {
			ended = v.EndedAt.UTC().Format(time.RFC3339Nano)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO sessions(id,project,directory,started_at,ended_at,summary) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET project=excluded.project,directory=excluded.directory,ended_at=excluded.ended_at,summary=excluded.summary`, localID, v.Project, v.Directory, v.StartedAt.UTC().Format(time.RFC3339Nano), ended, v.Summary)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO remote_sync_sessions(local_id,sync_id,updated_at) VALUES(?,?,?) ON CONFLICT(local_id) DO UPDATE SET sync_id=excluded.sync_id,updated_at=excluded.updated_at`, localID, v.SyncID, v.UpdatedAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
	}
	for _, v := range page.Observations {
		if v.TopicKey != "" {
			if _, err = tx.ExecContext(ctx, `UPDATE observations SET sync_id=? WHERE project=? AND topic_key=? AND deleted_at IS NULL AND sync_id<>?`, v.SyncID, v.Project, v.TopicKey, v.SyncID); err != nil {
				return err
			}
		}
		var sessionID string
		if err = tx.QueryRowContext(ctx, `SELECT local_id FROM remote_sync_sessions WHERE sync_id=?`, v.SessionSyncID).Scan(&sessionID); err != nil {
			return fmt.Errorf("remote sync observation session: %w", err)
		}
		tags, _ := json.Marshal(v.Tags)
		deleted := any(nil)
		if v.Deleted {
			deleted = v.UpdatedAt.UTC().Format(time.RFC3339Nano)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO observations(sync_id,session_id,type,title,content,project,scope,topic_key,normalized_hash,confidence,source,tags,created_at,updated_at,deleted_at) VALUES(?,?,?,?,?,?,?,?,lower(hex(randomblob(16))),?,?,?,?,?,?) ON CONFLICT(sync_id) WHERE sync_id IS NOT NULL DO UPDATE SET session_id=excluded.session_id,type=excluded.type,title=excluded.title,content=excluded.content,project=excluded.project,scope=excluded.scope,topic_key=excluded.topic_key,confidence=excluded.confidence,source=excluded.source,tags=excluded.tags,updated_at=excluded.updated_at,deleted_at=excluded.deleted_at WHERE excluded.updated_at>=observations.updated_at`, v.SyncID, sessionID, v.Type, v.Title, v.Content, v.Project, v.Scope, nullString(v.TopicKey), v.Confidence, v.Source, string(tags), v.CreatedAt.UTC().Format(time.RFC3339Nano), v.UpdatedAt.UTC().Format(time.RFC3339Nano), deleted)
		if err != nil {
			return err
		}
	}
	for _, v := range page.Prompts {
		var sessionID string
		if err = tx.QueryRowContext(ctx, `SELECT local_id FROM remote_sync_sessions WHERE sync_id=?`, v.SessionSyncID).Scan(&sessionID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO user_prompts(sync_id,session_id,content,project,created_at) VALUES(?,?,?,?,?) ON CONFLICT(sync_id) WHERE sync_id IS NOT NULL DO UPDATE SET session_id=excluded.session_id,content=excluded.content,project=excluded.project`, v.SyncID, sessionID, v.Content, v.Project, v.CreatedAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
	}
	for _, v := range page.Edges {
		if v.Deleted {
			var localID int64
			err = tx.QueryRowContext(ctx, `SELECT local_id FROM remote_sync_edges WHERE sync_id=?`, v.SyncID).Scan(&localID)
			if err == nil && localID > 0 {
				if _, err = tx.ExecContext(ctx, `DELETE FROM edges WHERE id=?`, localID); err != nil {
					return err
				}
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE remote_sync_edges SET deleted=1,updated_at=datetime('now') WHERE sync_id=?`, v.SyncID); err != nil {
				return err
			}
			continue
		}
		var fromID, toID int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM observations WHERE sync_id=?`, v.FromSyncID).Scan(&fromID); err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, `SELECT id FROM observations WHERE sync_id=?`, v.ToSyncID).Scan(&toID); err != nil {
			return err
		}
		var localID int64
		err = tx.QueryRowContext(ctx, `SELECT local_id FROM remote_sync_edges WHERE sync_id=?`, v.SyncID).Scan(&localID)
		if errors.Is(err, sql.ErrNoRows) && !v.Deleted {
			res, e := tx.ExecContext(ctx, `INSERT INTO edges(from_obs_id,to_obs_id,relation_type,weight,confidence,source,reasoning,valid_from,valid_until,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, fromID, toID, v.Relation, v.Weight, v.Confidence, v.Source, v.Reasoning, timePtr(v.ValidFrom), timePtr(v.ValidUntil), v.CreatedAt.UTC().Format(time.RFC3339Nano))
			if e != nil {
				return e
			}
			localID, _ = res.LastInsertId()
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if localID > 0 && v.Deleted {
			if _, err = tx.ExecContext(ctx, `DELETE FROM edges WHERE id=?`, localID); err != nil {
				return err
			}
		} else if localID > 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE edges SET from_obs_id=?,to_obs_id=?,relation_type=?,weight=?,confidence=?,source=?,reasoning=?,valid_from=?,valid_until=? WHERE id=?`, fromID, toID, v.Relation, v.Weight, v.Confidence, v.Source, v.Reasoning, timePtr(v.ValidFrom), timePtr(v.ValidUntil), localID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO remote_sync_edges(local_id,sync_id,from_sync_id,to_sync_id,relation_type,weight,confidence,source,reasoning,valid_from,valid_until,created_at,updated_at,deleted) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(sync_id) DO UPDATE SET local_id=excluded.local_id,from_sync_id=excluded.from_sync_id,to_sync_id=excluded.to_sync_id,relation_type=excluded.relation_type,weight=excluded.weight,confidence=excluded.confidence,source=excluded.source,reasoning=excluded.reasoning,valid_from=excluded.valid_from,valid_until=excluded.valid_until,updated_at=excluded.updated_at,deleted=excluded.deleted`, localID, v.SyncID, v.FromSyncID, v.ToSyncID, v.Relation, v.Weight, v.Confidence, v.Source, v.Reasoning, timePtr(v.ValidFrom), timePtr(v.ValidUntil), v.CreatedAt.UTC().Format(time.RFC3339Nano), v.UpdatedAt.UTC().Format(time.RFC3339Nano), v.Deleted)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO remote_sync_state(key,value) VALUES('cursor',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.FormatInt(page.Cursor, 10))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func parseSyncTime(raw string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
func nullString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
func timePtr(v *time.Time) any {
	if v == nil {
		return nil
	}
	return v.UTC().Format(time.RFC3339Nano)
}
