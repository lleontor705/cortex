package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
)

type matrixCase struct {
	name    string
	wantErr error
	sess    *domain.Session
	obs     *domain.Observation
	pmt     *domain.Prompt
}

func getMatrixCases(canary string, now time.Time) []matrixCase {
	badObs := func(t, c, p string, tags []string, top, sess, sc, typ string) *domain.Observation {
		return &domain.Observation{SessionID: sess, Title: t, Content: c, Project: p, Tags: tags, TopicKey: top, Scope: sc, Type: typ, CreatedAt: now, UpdatedAt: now}
	}
	return []matrixCase{
		{"obs_unclosed_content", privacy.ErrInvalidMarker, nil, badObs("T", "Bad <private>"+canary, "p", nil, "", "s1", "project", "manual"), nil},
		{"obs_stray_close_content", privacy.ErrInvalidMarker, nil, badObs("T", "Bad </private>", "p", nil, "", "s1", "project", "manual"), nil},
		{"obs_nested_content", privacy.ErrInvalidMarker, nil, badObs("T", "<private>a <private>b</private></private>", "p", nil, "", "s1", "project", "manual"), nil},
		{"obs_attribute_marker", privacy.ErrInvalidMarker, nil, badObs("T", "<private k=\"x\">"+canary+"</private>", "p", nil, "", "s1", "project", "manual"), nil},
		{"obs_whitespace_open_tag", privacy.ErrInvalidMarker, nil, badObs("T", "< private>"+canary+"</private>", "p", nil, "", "s1", "project", "manual"), nil},
		{"obs_whitespace_close_tag", privacy.ErrInvalidMarker, nil, badObs("T", "<private>"+canary+"</ private>", "p", nil, "", "s1", "project", "manual"), nil},
		{"obs_reversed_marker", privacy.ErrInvalidMarker, nil, badObs("T", "Bad </private><private>rev</private>", "p", nil, "", "s1", "project", "manual"), nil},
		{"obs_required_empty_content", privacy.ErrRequiredEmpty, nil, badObs("T", "<private>"+canary+"</private>", "p", nil, "", "s1", "project", "manual"), nil},
		{"obs_whitespace_residual_content", privacy.ErrRequiredEmpty, nil, badObs("T", " \t <private>"+canary+"</private> \n", "p", nil, "", "s1", "project", "manual"), nil},
		{"obs_unclosed_title", privacy.ErrInvalidMarker, nil, badObs("T <private>"+canary, "Body", "p", nil, "", "s1", "project", "manual"), nil},
		{"obs_stray_close_title", privacy.ErrInvalidMarker, nil, badObs("T </private>", "Body", "p", nil, "", "s1", "project", "manual"), nil},
		{"obs_required_empty_title", privacy.ErrRequiredEmpty, nil, badObs("<private>"+canary+"</private>", "Body", "p", nil, "", "s1", "project", "manual"), nil},
		{"obs_meta_project", privacy.ErrMetadataRejected, nil, badObs("T", "Body", "p<private>"+canary+"</private>", nil, "", "s1", "project", "manual"), nil},
		{"obs_meta_tags", privacy.ErrMetadataRejected, nil, badObs("T", "Body", "p", []string{"<private>" + canary + "</private>"}, "", "s1", "project", "manual"), nil},
		{"obs_meta_topic", privacy.ErrMetadataRejected, nil, badObs("T", "Body", "p", nil, "k/<private>"+canary+"</private>", "s1", "project", "manual"), nil},
		{"obs_meta_session_id", privacy.ErrMetadataRejected, nil, badObs("T", "Body", "p", nil, "", "s/<private>"+canary+"</private>", "project", "manual"), nil},
		{"obs_meta_scope", privacy.ErrMetadataRejected, nil, badObs("T", "Body", "p", nil, "", "s1", "<private>"+canary+"</private>", "manual"), nil},
		{"obs_meta_type", privacy.ErrMetadataRejected, nil, badObs("T", "Body", "p", nil, "", "s1", "project", "<private>"+canary+"</private>"), nil},
		{"sess_unclosed_summary", privacy.ErrInvalidMarker, &domain.Session{ID: "s2", Project: "p", Directory: "/d", Summary: "S <private>" + canary, StartedAt: now}, nil, nil},
		{"sess_meta_id", privacy.ErrMetadataRejected, &domain.Session{ID: "s/<private>" + canary + "</private>", Project: "p", Directory: "/d", StartedAt: now}, nil, nil},
		{"sess_meta_project", privacy.ErrMetadataRejected, &domain.Session{ID: "s2", Project: "p/<private>" + canary + "</private>", Directory: "/d", StartedAt: now}, nil, nil},
		{"sess_meta_directory", privacy.ErrMetadataRejected, &domain.Session{ID: "s2", Project: "p", Directory: "/d/<private>" + canary + "</private>", StartedAt: now}, nil, nil},
		{"prompt_unclosed_content", privacy.ErrInvalidMarker, nil, nil, &domain.Prompt{SessionID: "s1", Project: "p", Content: "P <private>" + canary, CreatedAt: now}},
		{"prompt_required_empty", privacy.ErrRequiredEmpty, nil, nil, &domain.Prompt{SessionID: "s1", Project: "p", Content: "<private>" + canary + "</private>", CreatedAt: now}},
	}
}

func makePrefixChunk(tc matrixCase, now time.Time) ([]*domain.Session, []*domain.Observation, []*domain.Prompt, string) {
	vObs := &domain.Observation{SessionID: "s1", Title: "Valid", Content: "Valid <private>sec_v</private>", Project: "p", Scope: "project", CreatedAt: now, UpdatedAt: now}
	s := []*domain.Session{{ID: "s1", Project: "p", Directory: "/d", StartedAt: now}}
	if tc.sess != nil {
		s = append(s, tc.sess)
	}
	o := []*domain.Observation{vObs}
	origBad := ""
	if tc.obs != nil {
		origBad = tc.obs.Content
		o = append(o, tc.obs)
	}
	p := []*domain.Prompt{{SessionID: "s1", Project: "p", Content: "P <private>sec_v</private>", CreatedAt: now}}
	if tc.pmt != nil {
		p = append(p, tc.pmt)
	}
	return s, o, p, origBad
}

func TestSync_ChunkPrivacyPreflight(t *testing.T) {
	ctx, now := context.Background(), time.Now().UTC()
	const canary = "canary_sec_7733"

	t.Run("Export_PositiveControl", func(t *testing.T) {
		transport, store := NewFileTransport(t.TempDir()), newMockStore()
		sSum, oTitle, oBody, pBody := "Sum <private>sec_s1</private>", "T <private>sec_t1</private>", "B <private>sec_b1</private>", "P <private>sec_p1</private>"
		store.exportData = &sqlitestore.ExportData{
			Sessions:     []*domain.Session{{ID: "s1", Project: "p", Directory: "/d", Summary: sSum, StartedAt: now}},
			Observations: []*domain.Observation{{ID: 1, SessionID: "s1", Title: oTitle, Content: oBody, Project: "p", Scope: "project", CreatedAt: now, UpdatedAt: now}},
			Prompts:      []*domain.Prompt{{ID: 1, SessionID: "s1", Project: "p", Content: pBody, CreatedAt: now}},
		}
		sy := NewSyncer(store, transport)
		res, err := sy.Export(ctx, "author", "p")
		if err != nil || res == nil || res.IsEmpty || res.SessionsExported != 1 || res.ObservationsExported != 1 || res.PromptsExported != 1 {
			t.Fatalf("Export failed: res=%+v, err=%v", res, err)
		}
		if store.exportData.Sessions[0].Summary != sSum || store.exportData.Observations[0].Content != oBody || store.exportData.Prompts[0].Content != pBody {
			t.Fatal("source export data mutated")
		}
		raw, err := transport.ReadChunk(res.ChunkID)
		if err != nil {
			t.Fatalf("ReadChunk failed: %v", err)
		}
		for _, c := range []string{"sec_s1", "sec_t1", "sec_b1", "sec_p1", "<private>", "</private>"} {
			if strings.Contains(string(raw), c) {
				t.Fatalf("canary %q leaked in chunk: %s", c, string(raw))
			}
		}
		h := sha256.Sum256(raw)
		if wantID := hex.EncodeToString(h[:])[:8]; res.ChunkID != wantID {
			t.Fatalf("chunk ID mismatch: got %s, want %s", res.ChunkID, wantID)
		}
		m, _ := transport.ReadManifest()
		if len(m.Chunks) != 1 || m.Chunks[0].ID != res.ChunkID || !store.syncedChunks[res.ChunkID] {
			t.Fatalf("manifest/store mismatch: %+v", m)
		}
	})

	t.Run("Import_PositiveControl", func(t *testing.T) {
		transport, store := NewFileTransport(t.TempDir()), newMockStore()
		origObs := "Obs <private>c_imp</private> text"
		chunk := &ChunkData{
			Sessions:     []*domain.Session{{ID: "s-imp", Project: "p", Directory: "/d", Summary: "S <private>s_imp</private>", StartedAt: now}},
			Observations: []*domain.Observation{{ID: 1, SessionID: "s-imp", Title: "T <private>t_imp</private>", Content: origObs, Project: "p", Scope: "project", CreatedAt: now, UpdatedAt: now}},
			Prompts:      []*domain.Prompt{{ID: 1, SessionID: "s-imp", Project: "p", Content: "P <private>p_imp</private>", CreatedAt: now}},
		}
		data, _ := json.Marshal(chunk)
		h := sha256.Sum256(data)
		id := hex.EncodeToString(h[:])[:8]
		_ = transport.WriteChunk(id, data, ChunkEntry{ID: id})
		_ = transport.WriteManifest(&Manifest{Version: 1, Chunks: []ChunkEntry{{ID: id}}})
		sy := NewSyncer(store, transport)
		res, err := sy.Import(ctx)
		if err != nil || res == nil || res.ChunksImported != 1 {
			t.Fatalf("Import failed: res=%+v, err=%v", res, err)
		}
		if chunk.Observations[0].Content != origObs {
			t.Fatal("source ChunkData mutated")
		}
		if store.importedData == nil || len(store.importedData.Observations) != 1 || store.importedData.Observations[0].Content != "Obs [REDACTED] text" || !store.syncedChunks[id] {
			t.Fatalf("imported observation mismatch: %+v", store.importedData)
		}
	})

	t.Run("Controls_And_Precedence", func(t *testing.T) {
		transport, store := NewFileTransport(t.TempDir()), newMockStore()
		sy := NewSyncer(store, transport)
		store.exportData = &sqlitestore.ExportData{
			Sessions:     []*domain.Session{{ID: "s-opt", Project: "p", Directory: "/d", Summary: "<private>only_secret</private>", StartedAt: now}},
			Observations: []*domain.Observation{{ID: 10, SessionID: "s-opt", Title: "Valid", Content: "Valid body", Project: "p", Scope: "project", CreatedAt: now, UpdatedAt: now}},
		}
		res, err := sy.Export(ctx, "author", "p")
		if err != nil || res == nil || res.SessionsExported != 1 {
			t.Fatalf("optional summary export failed: res=%+v, err=%v", res, err)
		}
		raw, _ := transport.ReadChunk(res.ChunkID)
		if !strings.Contains(string(raw), `"summary":"[REDACTED]"`) {
			t.Fatalf("expected summary redacted in raw chunk: %s", string(raw))
		}

		// Timestamp precedence: UpdatedAt > CreatedAt
		tPrev := now.Add(-time.Hour).Format(time.RFC3339)
		_ = transport.WriteManifest(&Manifest{Version: 1, Chunks: []ChunkEntry{{ID: "prev", CreatedAt: tPrev}}})
		store.exportData = &sqlitestore.ExportData{
			Observations: []*domain.Observation{
				{ID: 11, SessionID: "s1", Title: "Updated", Content: "Content", Project: "p", Scope: "project", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(time.Minute)},
				{ID: 12, SessionID: "s1", Title: "Old", Content: "Content", Project: "p", Scope: "project", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-90 * time.Minute)},
			},
		}
		resPrec, errPrec := sy.Export(ctx, "author", "p")
		if errPrec != nil || resPrec == nil || resPrec.ObservationsExported != 1 {
			t.Fatalf("precedence export failed: res=%+v err=%v", resPrec, errPrec)
		}
	})

	t.Run("Export_RejectionsZeroEffects", func(t *testing.T) {
		for _, tc := range getMatrixCases(canary, now) {
			t.Run(tc.name, func(t *testing.T) {
				transport, store := NewFileTransport(t.TempDir()), newMockStore()
				s, o, p, origBad := makePrefixChunk(tc, now)
				store.exportData = &sqlitestore.ExportData{Sessions: s, Observations: o, Prompts: p}

				sy := NewSyncer(store, transport)
				res, err := sy.Export(ctx, "author", "")
				if err == nil || !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				if res != nil {
					t.Fatalf("expected nil result on failure, got %+v", res)
				}
				if strings.Contains(err.Error(), canary) {
					t.Fatalf("error leaked canary: %s", err)
				}
				if o[0].Content != "Valid <private>sec_v</private>" || (tc.obs != nil && tc.obs.Content != origBad) {
					t.Fatal("source struct mutated on error")
				}
				m, _ := transport.ReadManifest()
				if len(m.Chunks) != 0 || len(store.syncedChunks) != 0 {
					t.Fatalf("effects recorded on error: m=%+v synced=%+v", m, store.syncedChunks)
				}
			})
		}
	})

	t.Run("Import_RejectionsZeroEffects", func(t *testing.T) {
		for _, tc := range getMatrixCases(canary, now) {
			t.Run(tc.name, func(t *testing.T) {
				transport, store := NewFileTransport(t.TempDir()), newMockStore()
				s, o, p, origBad := makePrefixChunk(tc, now)
				chunk := &ChunkData{Sessions: s, Observations: o, Prompts: p}
				data, _ := json.Marshal(chunk)
				h := sha256.Sum256(data)
				id := hex.EncodeToString(h[:])[:8]
				_ = transport.WriteChunk(id, data, ChunkEntry{ID: id})
				_ = transport.WriteManifest(&Manifest{Version: 1, Chunks: []ChunkEntry{{ID: id}}})

				sy := NewSyncer(store, transport)
				res, err := sy.Import(ctx)
				if err == nil || !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				if res != nil {
					t.Fatalf("expected nil result on failure, got %+v", res)
				}
				if strings.Contains(err.Error(), canary) {
					t.Fatalf("error leaked canary: %s", err)
				}
				if o[0].Content != "Valid <private>sec_v</private>" || (tc.obs != nil && tc.obs.Content != origBad) {
					t.Fatal("source ChunkData mutated on error")
				}
				if store.importedData != nil || store.syncedChunks[id] {
					t.Fatalf("store state modified on rejection: imported=%+v synced=%+v", store.importedData, store.syncedChunks)
				}
			})
		}
	})
}
