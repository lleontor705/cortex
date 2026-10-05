// Package sqlite_blob: REQ-VEC-FILTER-001 selectivity heuristic pins.
//
// chooseScanPlan and the metadata filter parsing are pure, so they run in the
// DEFAULT build: table-driven boundaries, determinism, and the recognized key
// set — no database, no build tag.
package sqlite_blob

import "testing"

func TestChooseScanPlan_SelectivityBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		total    int
		matching int
		want     scanPlan
	}{
		{"zero match short-circuits", 100, 0, planNoCandidates},
		{"negative match never reads corpus", 100, -1, planNoCandidates},
		{"empty corpus no candidates", 0, 0, planNoCandidates},
		{"whole corpus matches stays constrained", 100, 100, planConstrained},
		{"one eighth of corpus exact boundary", 800, 100, planExact},
		{"just above selectivity boundary constrained", 799, 100, planConstrained},
		{"single row of eight exact", 8, 1, planExact},
		{"single row of seven constrained", 7, 1, planConstrained},
		{"candidate ceiling exact", 32768, 4096, planExact},
		{"above candidate ceiling constrained", 40000, 4097, planConstrained},
		{"huge corpus over ceiling constrained", 1 << 20, 4097, planConstrained},
		{"selective small subset exact", 900, 5, planExact},
		{"moderately selective stays constrained", 900, 200, planConstrained},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := chooseScanPlan(tc.total, tc.matching)
			if got != tc.want {
				t.Fatalf("chooseScanPlan(%d, %d) = %v, want %v", tc.total, tc.matching, got, tc.want)
			}
		})
	}
}

func TestChooseScanPlan_DeterministicAcrossRuns(t *testing.T) {
	for i := range 500 {
		total := i*7919%100000 + 1
		matching := i * 104729 % (total + 1)
		first := chooseScanPlan(total, matching)
		for run := 0; run < 5; run++ {
			if got := chooseScanPlan(total, matching); got != first {
				t.Fatalf("chooseScanPlan(%d, %d) run %d = %v, first run = %v",
					total, matching, run, got, first)
			}
		}
	}
}

func TestParseMetadataFilters_RecognizedKeySetMirrorsRetrieval(t *testing.T) {
	parsed := parseMetadataFilters(map[string]any{
		"project":   "p1",
		"scope":     "PERSONAL",
		"type":      "bugfix",
		"source":    "import",
		"tenant_id": "ignored",
		"limit":     10,
	})
	if !parsed.active() {
		t.Fatal("active() = false, want true for populated recognized keys")
	}
	if parsed.project != "p1" {
		t.Errorf("project = %q, want %q", parsed.project, "p1")
	}
	if parsed.scope != "personal" {
		t.Errorf("scope = %q, want normalized %q", parsed.scope, "personal")
	}
	if parsed.typ != "bugfix" {
		t.Errorf("type = %q, want %q", parsed.typ, "bugfix")
	}
	if parsed.source != "import" {
		t.Errorf("source = %q, want %q", parsed.source, "import")
	}
}

func TestParseMetadataFilters_EmptyNonStringAndUnknownAreUnset(t *testing.T) {
	parsed := parseMetadataFilters(map[string]any{
		"project": "",
		"scope":   nil,
		"type":    42,
		"source":  "",
		"unknown": "value",
	})
	if parsed.active() {
		t.Errorf("active() = true for non-constraining values, want false (%+v)", parsed)
	}
	if parsed.scope != "" {
		t.Errorf("unset scope = %q, want empty (must not normalize to a default)", parsed.scope)
	}
	if inactive := parseMetadataFilters(nil); inactive.active() {
		t.Error("nil filter map must be inactive")
	}
}

func TestMetadataFilters_SharedConditionBuilderOrder(t *testing.T) {
	conditions, args := parseMetadataFilters(map[string]any{
		"project": "p1",
		"type":    "decision",
	}).filterConditions()
	wantConditions := []string{"o.project = ?", "o.type = ?"}
	if len(conditions) != len(wantConditions) {
		t.Fatalf("conditions = %v, want %v", conditions, wantConditions)
	}
	for i := range wantConditions {
		if conditions[i] != wantConditions[i] {
			t.Errorf("condition[%d] = %q, want %q", i, conditions[i], wantConditions[i])
		}
	}
	wantArgs := []any{"p1", "decision"}
	if len(args) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", args, wantArgs)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Errorf("arg[%d] = %v, want %v", i, args[i], wantArgs[i])
		}
	}

	allConditions, allArgs := (metadataFilters{
		project: "a", scope: "personal", typ: "b", source: "c",
	}).filterConditions()
	if len(allConditions) != 4 || len(allArgs) != 4 {
		t.Fatalf("full filter conditions = %v (args %v), want 4 of each", allConditions, allArgs)
	}
	if none, noneArgs := (metadataFilters{}).filterConditions(); len(none) != 0 || len(noneArgs) != 0 {
		t.Errorf("inactive filter produced conditions %v args %v, want none", none, noneArgs)
	}
}
