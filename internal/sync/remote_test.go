package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/migration"
	"github.com/lleontor705/cortex/v2/internal/transportpolicy"
	_ "modernc.org/sqlite"
)

func TestRemoteSyncPushesLocalAndPullsServerData(t *testing.T) {
	db, err := sql.Open("sqlite", "file:remote-sync-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	baseline, err := migration.NewV2Baseline()
	if err != nil {
		t.Fatal(err)
	}
	if err := baseline.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := db.Exec(`INSERT INTO sessions(id,project,directory,started_at) VALUES('local-session','cortex','/repo',?)`, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO observations(session_id,type,title,content,project,scope,confidence,source,created_at,updated_at) VALUES('local-session','manual','Local','local value','cortex','project',1,'manual',?,?)`, now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	var pushed domain.SyncBatch
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/sync/push":
			var request domain.SyncBatch
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			pushed.Sessions = append(pushed.Sessions, request.Sessions...)
			pushed.Observations = append(pushed.Observations, request.Observations...)
			pushed.Prompts = append(pushed.Prompts, request.Prompts...)
			pushed.Edges = append(pushed.Edges, request.Edges...)
			pushed.CodeSymbols = append(pushed.CodeSymbols, request.CodeSymbols...)
			pushed.CodeRelations = append(pushed.CodeRelations, request.CodeRelations...)
			_ = json.NewEncoder(w).Encode(domain.SyncResult{Accepted: countBatch(&request)})
		case "/api/sync/changes":
			_ = json.NewEncoder(w).Encode(domain.SyncPage{SyncBatch: domain.SyncBatch{Sessions: []domain.SyncSession{{SyncID: "server-session", Project: "remote", Directory: "/remote", StartedAt: now, UpdatedAt: now}}}, Cursor: 7})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	syncer, err := NewRemoteSyncer(db, server.URL, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := syncer.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Cursor != 7 || result.Pushed != 3 || result.Pulled != 1 {
		t.Fatalf("result = %+v", result)
	}
	if len(pushed.Sessions) != 2 || pushed.Sessions[0].SyncID != "local-session" || pushed.Sessions[1].SyncID != "server-session" || len(pushed.Observations) != 1 || pushed.Observations[0].SyncID == "" {
		t.Fatalf("pushed = %+v", pushed)
	}
	var project string
	if err := db.QueryRow(`SELECT project FROM sessions WHERE id='server-session'`).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if project != "remote" {
		t.Fatalf("pulled project = %q", project)
	}
}

// newTransportTestDB opens an isolated in-memory SQLite database with the v2
// baseline applied and one local session, so a full sync cycle (pull + push)
// exercises the Authorization path.
func newTransportTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	baseline, err := migration.NewV2Baseline()
	if err != nil {
		t.Fatal(err)
	}
	if err := baseline.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := db.Exec(`INSERT INTO sessions(id,project,directory,started_at) VALUES('local-session','cortex','/repo',?)`, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestNewRemoteSyncerEnforcesBearerTransportPolicy(t *testing.T) {
	db := newTransportTestDB(t)
	tests := []struct {
		name     string
		url      string
		wantErr  bool
		wantCode string
	}{
		{name: "https remote accepted", url: "https://cortex.example.com"},
		{name: "https remote with port accepted", url: "https://cortex.example.com:8443"},
		{name: "http strict loopback IPv4 accepted", url: "http://127.0.0.1:7438"},
		{name: "http loopback 127/8 accepted", url: "http://127.0.0.2:7438"},
		{name: "http strict loopback IPv6 accepted", url: "http://[::1]:7438"},
		{name: "http localhost accepted", url: "http://localhost:7438"},
		{name: "http remote rejected", url: "http://cortex.example.com", wantErr: true, wantCode: transportpolicy.CodeInsecureScheme},
		{name: "http private network rejected", url: "http://192.168.1.10:7438", wantErr: true, wantCode: transportpolicy.CodeInsecureScheme},
		{name: "http public IP rejected", url: "http://8.8.8.8:7438", wantErr: true, wantCode: transportpolicy.CodeInsecureScheme},
		{name: "non-HTTP scheme rejected", url: "ftp://cortex.example.com", wantErr: true, wantCode: transportpolicy.CodeUnsupportedScheme},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer, err := NewRemoteSyncer(db, tt.url, "secret", time.Second)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NewRemoteSyncer(%q) = nil error, want rejection %q", tt.url, tt.wantCode)
				}
				var policyErr *transportpolicy.Error
				if !errors.As(err, &policyErr) {
					t.Fatalf("error %v is not *transportpolicy.Error", err)
				}
				if policyErr.Code != tt.wantCode {
					t.Fatalf("code = %q, want %q", policyErr.Code, tt.wantCode)
				}
				if syncer != nil {
					t.Fatal("rejected constructor must not return a syncer")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewRemoteSyncer(%q) = %v, want accepted", tt.url, err)
			}
		})
	}
}

// decoyServer records every request it receives and whether any of them
// carried an Authorization header.
func decoyServer(t *testing.T) (*httptest.Server, *int32, *int32) {
	t.Helper()
	var hits, authorized int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("Authorization") != "" {
			atomic.AddInt32(&authorized, 1)
		}
		_ = json.NewEncoder(w).Encode(domain.SyncPage{})
	}))
	t.Cleanup(server.Close)
	return server, &hits, &authorized
}

func TestRemoteSyncerRefusesRedirectDowngradeBeforeAuthorization(t *testing.T) {
	db := newTransportTestDB(t)
	decoy, hits, authorized := decoyServer(t)
	// The origin is an HTTPS loopback endpoint (allowed) that tries to
	// redirect the Bearer-authenticated sync request down to plain HTTP.
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, decoy.URL+"/api/sync/changes", http.StatusFound)
	}))
	defer origin.Close()

	syncer, err := NewRemoteSyncer(db, origin.URL, "secret", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	syncer.client.Transport = origin.Client().Transport

	_, syncErr := syncer.Sync(context.Background())
	if syncErr == nil {
		t.Fatal("sync over a downgrading redirect must fail")
	}
	var policyErr *transportpolicy.Error
	if !errors.As(syncErr, &policyErr) {
		t.Fatalf("sync error %v is not *transportpolicy.Error", syncErr)
	}
	if policyErr.Code != transportpolicy.CodeSchemeDowngrade {
		t.Fatalf("code = %q, want %q", policyErr.Code, transportpolicy.CodeSchemeDowngrade)
	}
	if atomic.LoadInt32(hits) != 0 || atomic.LoadInt32(authorized) != 0 {
		t.Fatalf("downgraded decoy received requests (hits=%d, authorized=%d); the block must happen before any request", atomic.LoadInt32(hits), atomic.LoadInt32(authorized))
	}
}

func TestRemoteSyncerRefusesCrossOriginRedirectBeforeAuthorization(t *testing.T) {
	db := newTransportTestDB(t)
	decoy, hits, authorized := decoyServer(t)
	// Both endpoints are plain-HTTP strict loopback (individually allowed),
	// but the redirect changes the origin (different port).
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, decoy.URL+"/api/sync/changes", http.StatusFound)
	}))
	defer origin.Close()

	syncer, err := NewRemoteSyncer(db, origin.URL, "secret", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	_, syncErr := syncer.Sync(context.Background())
	if syncErr == nil {
		t.Fatal("sync over a cross-origin redirect must fail")
	}
	var policyErr *transportpolicy.Error
	if !errors.As(syncErr, &policyErr) {
		t.Fatalf("sync error %v is not *transportpolicy.Error", syncErr)
	}
	if policyErr.Code != transportpolicy.CodeOriginChange {
		t.Fatalf("code = %q, want %q", policyErr.Code, transportpolicy.CodeOriginChange)
	}
	if atomic.LoadInt32(hits) != 0 || atomic.LoadInt32(authorized) != 0 {
		t.Fatalf("cross-origin decoy received requests (hits=%d, authorized=%d); the block must happen before any request", atomic.LoadInt32(hits), atomic.LoadInt32(authorized))
	}
}

func TestRemoteSyncerAcceptsHTTPSLoopbackEndToEnd(t *testing.T) {
	db := newTransportTestDB(t)
	var accepted int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		atomic.AddInt32(&accepted, 1)
		switch r.URL.Path {
		case "/api/sync/push":
			var request domain.SyncBatch
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(domain.SyncResult{Accepted: countBatch(&request)})
		case "/api/sync/changes":
			_ = json.NewEncoder(w).Encode(domain.SyncPage{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()

	syncer, err := NewRemoteSyncer(db, origin.URL, "secret", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	syncer.client.Transport = origin.Client().Transport

	result, err := syncer.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Pushed != 1 || result.Pulled != 0 {
		t.Fatalf("result = %+v", result)
	}
	if atomic.LoadInt32(&accepted) == 0 {
		t.Fatal("HTTPS loopback sync must be accepted end-to-end")
	}
}
