package obsidian

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
)

type fakeObs struct{ list []*domain.Observation }

func (f fakeObs) Save(context.Context, *domain.Observation) error { return nil }
func (f fakeObs) GetByID(_ context.Context, id int64) (*domain.Observation, error) {
	for _, o := range f.list {
		if o.ID == id {
			return o, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (f fakeObs) GetByTopicKey(context.Context, string, string) (*domain.Observation, error) {
	return nil, domain.ErrNotFound
}
func (f fakeObs) Update(context.Context, *domain.Observation) error { return nil }
func (f fakeObs) Delete(context.Context, int64) error               { return nil }
func (f fakeObs) List(context.Context, domain.ObservationFilter) ([]*domain.Observation, error) {
	return f.list, nil
}
func (f fakeObs) CountAll(context.Context) (int, error)           { return len(f.list), nil }
func (f fakeObs) CountByRoot(context.Context, int64) (int, error) { return 0, nil }
func (f fakeObs) GetBySource(context.Context, string, int) ([]*domain.Observation, error) {
	return nil, nil
}
func (f fakeObs) GetByType(context.Context, string, int) ([]*domain.Observation, error) {
	return nil, nil
}

type fakeGraph struct{ edges map[int64][]*domain.Edge }

func (f fakeGraph) CreateEdge(context.Context, *domain.Edge) error { return nil }
func (f fakeGraph) GetRelated(context.Context, int64, int) ([]*domain.Observation, error) {
	return nil, nil
}
func (f fakeGraph) DeleteEdge(context.Context, int64) error { return nil }
func (f fakeGraph) GetEdgesForObservation(_ context.Context, id int64) ([]*domain.Edge, error) {
	return f.edges[id], nil
}
func (f fakeGraph) GetEdge(context.Context, int64) (*domain.Edge, error) { return nil, nil }
func (f fakeGraph) GetEvolutionChain(context.Context, int64, int64) ([]*domain.Edge, error) {
	return nil, nil
}
func (f fakeGraph) CountEdgesByObservation(context.Context, int64) (int, error) { return 0, nil }
func (f fakeGraph) CountAllEdges(context.Context) (int, error)                  { return 0, nil }
func (f fakeGraph) GetContradictions(context.Context, time.Time, time.Time) ([]*domain.Edge, error) {
	return nil, nil
}
func (f fakeGraph) UpdateEdge(context.Context, *domain.Edge) error { return nil }

func TestExportWritesDeterministicObsidianProjectionAndWikilink(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	obs := []*domain.Observation{{ID: 1, Title: "First / note", Content: "body", Project: "my/project", Scope: "project", Type: "decision", CreatedAt: now, UpdatedAt: now}, {ID: 2, Title: "Second", Content: "two", Project: "my/project", Scope: "project", Type: "manual", CreatedAt: now, UpdatedAt: now}}
	e := NewWriter(fakeObs{obs}, fakeGraph{map[int64][]*domain.Edge{1: {{FromObsID: 1, ToObsID: 2, RelationType: domain.RelationReferences}}}}, nil, nil)
	result, err := e.Export(context.Background(), Options{Vault: dir})
	if err != nil {
		t.Fatal(err)
	}
	if result.Written != 2 {
		t.Fatalf("written=%d", result.Written)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "cortex", "projects", "my-project", "observations", "*.md"))
	if len(files) != 2 {
		t.Fatalf("files=%d", len(files))
	}
	b, _ := os.ReadFile(files[0])
	if !strings.Contains(string(b), "cortex_id:") {
		t.Fatal("missing cortex_id")
	}
	if !strings.Contains(string(b), "[[") {
		t.Fatal("missing wikilink")
	}
	result, err = e.Export(context.Background(), Options{Vault: dir})
	if err != nil {
		t.Fatal(err)
	}
	if result.Skipped == 0 {
		t.Fatal("expected incremental skip")
	}
}

func TestExportExcludesPersonalUnlessOptedIn(t *testing.T) {
	dir := t.TempDir()
	obs := []*domain.Observation{{ID: 1, Title: "private", Scope: domain.ScopePersonal, CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	e := NewWriter(fakeObs{obs}, nil, nil, nil)
	r, err := e.Export(context.Background(), Options{Vault: dir})
	if err != nil {
		t.Fatal(err)
	}
	if r.Written != 0 || len(r.Warnings) == 0 {
		t.Fatalf("privacy filter failed: %+v", r)
	}
}

func TestExportExcludesPrivateAndWarnsOnPersonalOptIn(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	obs := []*domain.Observation{{ID: 3, Title: "private", Scope: "private", CreatedAt: now, UpdatedAt: now}}
	e := NewWriter(fakeObs{obs}, nil, nil, nil)
	r, err := e.Export(context.Background(), Options{Vault: dir})
	if err != nil || r.Written != 0 || len(r.Warnings) == 0 {
		t.Fatalf("private filter failed: %+v %v", r, err)
	}
	r, err = e.Export(context.Background(), Options{Vault: dir, IncludePersonal: true})
	if err != nil || r.Written != 1 || len(r.Warnings) == 0 {
		t.Fatalf("opt-in warning failed: %+v %v", r, err)
	}
}

func TestExportRefusesUnownedOverwriteAndMatchesRenameByCortexID(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	obs := []*domain.Observation{{ID: 7, Title: "Stable", Content: "body", Project: "p", Scope: "project", CreatedAt: now, UpdatedAt: now}}
	e := NewWriter(fakeObs{obs}, nil, nil, nil)
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "cortex", "projects", "p", "observations", "*.md"))
	if len(files) != 1 {
		t.Fatal("initial projection missing")
	}
	rename := filepath.Join(filepath.Dir(files[0]), "human-name.md")
	if err := os.Rename(files[0], rename); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Export(context.Background(), Options{Vault: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rename); !os.IsNotExist(err) {
		t.Fatal("renamed projection should be reconciled")
	}
	files, _ = filepath.Glob(filepath.Join(dir, "cortex", "projects", "p", "observations", "*.md"))
	if len(files) != 1 {
		t.Fatal("reconciled projection missing")
	}
	conflict := filepath.Join(filepath.Dir(files[0]), "different-"+idHash("7")+".md")
	if err := os.WriteFile(conflict, []byte("user note"), 0600); err != nil {
		t.Fatal(err)
	}
	obs[0].Title = "Different"
	if _, statErr := os.Stat(conflict); statErr != nil {
		t.Fatal(statErr)
	}
	_, err := e.Export(context.Background(), Options{Vault: dir})
	if err == nil {
		t.Fatal("expected overwrite conflict")
	}
}
