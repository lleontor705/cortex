package ast

// Gold fail-closed oracle for the 14 AST adapters (Delivery A, node A0.1).
// Authority: cortex:observation/1691#A0.1: the frozen matrix below must never be
// lowered (AST_CAPABILITY_OVERCLAIM); no production code, only the corpus contract
// that nodes A2.01..A2.14 and A4.6 measure against.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const goldDir = "testdata/gold"

// goldThresholds is one frozen row of the per-language gold matrix.
type goldThresholds struct {
	Level   string  `json:"level"`
	DeclMin int     `json:"decl_min"`
	RefsMin int     `json:"refs_min"`
	DeclP   float64 `json:"decl_p"`
	DeclR   float64 `json:"decl_r"`
	RefsP   float64 `json:"refs_p"`
	RefsR   float64 `json:"refs_r"`
}

// frozenByLevel mirrors the authoritative matrix in cortex:observation/1691.
// L2 exists only as the mandated fallback if a capability level ever changes.
var frozenByLevel = map[string]goldThresholds{
	"L3": {Level: "L3", DeclMin: 50, RefsMin: 40, DeclP: .99, DeclR: .99, RefsP: .98, RefsR: .95},
	"L2": {Level: "L2", DeclMin: 35, RefsMin: 30, DeclP: .97, DeclR: .95, RefsP: .95, RefsR: .90},
	"L1": {Level: "L1", DeclMin: 25, RefsMin: 20, DeclP: .98, DeclR: .80, RefsP: .98, RefsR: .70},
	"L0": {Level: "L0"},
}

// frozenLanguages is the mandated capability level for each of the 14 adapters.
var frozenLanguages = map[string]string{
	"go": "L3", "tsjs": "L1", "python": "L1", "sql": "L1", "rust": "L1", "csharp": "L1",
	"fsharp": "L1", "vbnet": "L1", "java": "L1", "kotlin": "L1", "cpp": "L1", "php": "L1",
	"ruby": "L1", "swift": "L1",
}

// requiredCases are the edge categories every language corpus must carry.
var requiredCases = []string{"ambiguity", "no_edge", "missing_module", "empty", "homonym", "determinism"}

type goldProvenance struct {
	Origin    string `json:"origin"`
	License   string `json:"license"`
	CleanRoom bool   `json:"clean_room"`
}
type goldFixture struct {
	Path       string         `json:"path"`
	SHA256     string         `json:"sha256"`
	Provenance goldProvenance `json:"provenance"`
}

// goldLanguage embeds goldThresholds: encoding/json flattens the frozen-row
// fields into each language object exactly as the manifest schema mandates.
type goldLanguage struct {
	ID    string `json:"id"`
	Level string `json:"level"`
	goldThresholds
	Cases    []string      `json:"cases"`
	Fixtures []goldFixture `json:"fixtures"`
}
type goldManifest struct {
	Languages []goldLanguage `json:"languages"`
}

func loadGoldManifest(t *testing.T) *goldManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(goldDir, "manifest.json"))
	if err != nil {
		t.Fatalf("gold manifest unreadable (fail-closed): %v", err)
	}
	var m goldManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("gold manifest invalid JSON: %v", err)
	}
	if len(m.Languages) == 0 {
		t.Fatal("gold manifest declares no languages")
	}
	return &m
}

func TestGoldManifest_Coverage(t *testing.T) {
	m := loadGoldManifest(t)
	seen := make(map[string]bool, len(frozenLanguages))
	for _, lang := range m.Languages {
		if seen[lang.ID] {
			t.Errorf("duplicate language %q in gold manifest", lang.ID)
		}
		seen[lang.ID] = true
		want, ok := frozenLanguages[lang.ID]
		if !ok {
			t.Errorf("unexpected language %q; frozen corpus covers exactly 14 adapters", lang.ID)
			continue
		}
		if lang.Level != want {
			t.Errorf("%s: level %q but frozen matrix mandates %q (AST_CAPABILITY_OVERCLAIM)", lang.ID, lang.Level, want)
		}
	}
	for id := range frozenLanguages {
		if !seen[id] {
			t.Errorf("missing language %q: gold corpus must cover all 14 adapters", id)
		}
	}
}
func TestGoldManifest_PerLanguage(t *testing.T) {
	m := loadGoldManifest(t)
	if len(m.Languages) != len(frozenLanguages) {
		t.Fatalf("expected exactly %d languages, got %d", len(frozenLanguages), len(m.Languages))
	}
	for _, lang := range m.Languages {
		frozen, ok := frozenByLevel[lang.Level]
		if !ok {
			t.Errorf("%s: invalid level %q (allowed L0-L3)", lang.ID, lang.Level)
			continue
		}
		if lang.DeclMin != frozen.DeclMin || lang.RefsMin != frozen.RefsMin ||
			lang.DeclP != frozen.DeclP || lang.DeclR != frozen.DeclR ||
			lang.RefsP != frozen.RefsP || lang.RefsR != frozen.RefsR {
			t.Errorf("%s: minimums/thresholds deviate from frozen %s matrix (decl %d/%.2f/%.2f refs %d/%.2f/%.2f); want %+v",
				lang.ID, lang.Level, lang.DeclMin, lang.DeclP, lang.DeclR, lang.RefsMin, lang.RefsP, lang.RefsR, frozen)
		}
		if lang.Level != "L0" {
			if lang.DeclMin <= 0 || lang.RefsMin <= 0 {
				t.Errorf("%s: %s rows must declare positive minimums", lang.ID, lang.Level)
			}
			for name, v := range map[string]float64{"decl_p": lang.DeclP, "decl_r": lang.DeclR, "refs_p": lang.RefsP, "refs_r": lang.RefsR} {
				if v <= 0 || v > 1 {
					t.Errorf("%s: threshold %s=%.2f outside (0,1]", lang.ID, name, v)
				}
			}
		}
		declared := make(map[string]bool, len(lang.Cases))
		for _, c := range lang.Cases {
			declared[c] = true
		}
		for _, c := range requiredCases {
			if !declared[c] {
				t.Errorf("%s: missing required gold case category %q", lang.ID, c)
			}
		}
	}
}
func TestGoldManifest_Provenance(t *testing.T) {
	m := loadGoldManifest(t)
	for _, lang := range m.Languages {
		if len(lang.Fixtures) == 0 {
			t.Errorf("%s: no fixtures; a gold row cannot be measured", lang.ID)
			continue
		}
		for _, f := range lang.Fixtures {
			if f.Path == "" || f.SHA256 == "" {
				t.Errorf("%s: fixture entry needs both path and sha256", lang.ID)
			}
			p := f.Provenance
			if p.Origin == "" || p.License == "" || !p.CleanRoom {
				t.Errorf("%s/%s: provenance must record origin, license and clean_room=true (got %+v)", lang.ID, f.Path, p)
			}
		}
	}
}
func TestGoldManifest_Digest(t *testing.T) {
	m := loadGoldManifest(t)
	for _, lang := range m.Languages {
		for _, f := range lang.Fixtures {
			raw, err := os.ReadFile(filepath.Join(goldDir, filepath.ToSlash(f.Path)))
			if err != nil {
				t.Errorf("%s/%s: fixture missing (fail-closed): %v", lang.ID, f.Path, err)
				continue
			}
			sum := sha256.Sum256(raw)
			if got := hex.EncodeToString(sum[:]); got != f.SHA256 {
				t.Errorf("%s/%s: digest mismatch: manifest %s != actual %s (fixture tampered)", lang.ID, f.Path, f.SHA256, got)
			}
		}
	}
}

// goldEntry and goldCorpus parse real fixture content: every measurement
// below is derived from data, never from manifest declarations.
type goldEntry struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Case     string `json:"case"`
	Eligible bool   `json:"eligible"`
}
type goldCorpus struct {
	Language string      `json:"language"`
	Entries  []goldEntry `json:"entries"`
}

// loadGoldCorpus fails closed: an unreadable or invalid fixture aborts the oracle.
func loadGoldCorpus(t *testing.T, langID, path string) goldCorpus {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(goldDir, filepath.ToSlash(path)))
	if err != nil {
		t.Fatalf("%s/%s: fixture unreadable (fail-closed): %v", langID, path, err)
	}
	var c goldCorpus
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("%s/%s: fixture invalid JSON (fail-closed): %v", langID, path, err)
	}
	return c
}

// TestGoldFixture_Contents parses every fixture and derives per-language eligible
// denominators and case coverage from fixture content (the old oracle trusted
// manifest strings and never measured anything).
func TestGoldFixture_Contents(t *testing.T) {
	m := loadGoldManifest(t)
	byPath, bySHA := map[string]string{}, map[string]string{}
	for _, lang := range m.Languages {
		for _, f := range lang.Fixtures {
			if owner, dup := byPath[f.Path]; dup {
				t.Errorf("fixture %s shared by %s and %s (cross-language leak)", f.Path, owner, lang.ID)
				continue
			}
			byPath[f.Path] = lang.ID
			if owner, dup := bySHA[f.SHA256]; dup {
				t.Errorf("%s: digest collision with %s (cross-language leak)", lang.ID, owner)
				continue
			}
			bySHA[f.SHA256] = lang.ID
			c := loadGoldCorpus(t, lang.ID, f.Path)
			if c.Language != lang.ID {
				t.Errorf("%s: fixture self-identifies as %q (isolation violated)", lang.ID, c.Language)
			}
			var decls, refs int
			cases := map[string]bool{}
			for _, e := range c.Entries {
				if e.Kind != "decl" && e.Kind != "ref" {
					t.Errorf("%s: entry %q invalid kind %q", lang.ID, e.Name, e.Kind)
				}
				if e.Eligible && e.Name == "" {
					t.Errorf("%s: eligible entry with empty name", lang.ID)
				}
				if e.Case != "" {
					if !slices.Contains(requiredCases, e.Case) {
						t.Errorf("%s: entry %q unknown case %q", lang.ID, e.Name, e.Case)
					}
					cases[e.Case] = true
				}
				switch {
				case e.Eligible && e.Kind == "decl":
					decls++
				case e.Eligible && e.Kind == "ref":
					refs++
				}
			}
			if decls < lang.DeclMin || refs < lang.RefsMin {
				t.Errorf("%s: content has %d/%d eligible decls/refs, below frozen minima %d/%d: row unmeasurable",
					lang.ID, decls, refs, lang.DeclMin, lang.RefsMin)
			}
			for _, want := range requiredCases {
				if !cases[want] {
					t.Errorf("%s: fixture content lacks required case %q", lang.ID, want)
				}
			}
		}
	}
}

// goldScore carries the frozen definitions P = correct/all_emitted,
// R = correct/expected_eligible.
type goldScore struct{ P, R float64 }

// scoreGold fails closed on zero denominators (L1-L3 need emitted+eligible;
// L0 passes only when nothing was expected and nothing emitted).
func scoreGold(level string, correct, emitted, eligible int) (goldScore, error) {
	if level == "L0" {
		if eligible == 0 && emitted == 0 {
			return goldScore{}, nil
		}
		return goldScore{}, fmt.Errorf("L0 passes only with elegibles=0 and emitidos=0 (got %d/%d)", eligible, emitted)
	}
	if emitted == 0 || eligible == 0 {
		return goldScore{}, fmt.Errorf("%s: zero denominator is FAIL (correct=%d emitted=%d eligible=%d)", level, correct, emitted, eligible)
	}
	if correct > emitted || correct > eligible {
		return goldScore{}, fmt.Errorf("inconsistent counts: correct=%d emitted=%d eligible=%d", correct, emitted, eligible)
	}
	return goldScore{P: float64(correct) / float64(emitted), R: float64(correct) / float64(eligible)}, nil
}

func TestGoldManifest_ZeroDenominator(t *testing.T) {
	if _, err := scoreGold("L1", 0, 0, 25); err == nil {
		t.Error("L1 with emitted=0 must FAIL (zero denominator)")
	}
	if _, err := scoreGold("L3", 0, 50, 0); err == nil {
		t.Error("L3 with eligible=0 must FAIL (zero denominator)")
	}
	if _, err := scoreGold("L0", 0, 0, 0); err != nil {
		t.Errorf("L0 with 0/0 must PASS: %v", err)
	}
	if _, err := scoreGold("L0", 1, 1, 0); err == nil {
		t.Error("L0 emitting anything must FAIL")
	}
	s, err := scoreGold("L1", 20, 25, 25)
	if err != nil {
		t.Fatalf("valid L1 row must score: %v", err)
	}
	if s.P != 0.8 || s.R != 0.8 {
		t.Errorf("P/R want 0.8/0.8, got %.2f/%.2f", s.P, s.R)
	}
	if _, err := scoreGold("L2", 5, 4, 3); err == nil {
		t.Error("correct>emitted must FAIL as inconsistent")
	}
}
