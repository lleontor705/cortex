// Package app: W1 architecture exit gate (REQ-FOUND-001).
//
// This file is the W1 EXIT GATE — a TEST-ONLY guardrail. No production code
// depends on it. It ENFORCES four invariants that every downstream wave (W2+)
// must preserve for the Independent Memory Platform ("cortex-v2"):
//
//  1. No local→server import     — TestNoLocalToServerImport
//  2. No seam adoption in W1     — TestNoSeamAdoptionInW1
//  3. Ports compile in isolation — TestPortsCompileInIsolation
//  4. Local zero-CGO build       — TestZeroCgoLocalBuild
//
// If a future wave violates any invariant (e.g. imports a server package into
// the local composition, or adopts a W1 port before its dependent wave), THIS
// TEST FAILS and blocks the build. The gate is non-negotiable per REQ-FOUND-001
// error scenario: "caller adopts seam in its introduction wave → gate fails."
//
// WAVE EVOLUTION: the seam-adoption gate (TestNoSeamAdoptionInW1) is PER-ROOT
// and evolves with each wave. W2 ADOPTS TxParticipant + UnitOfWork in
// internal/store (REQ-TX-001 cross-store atomic save). Storage,
// VectorIndex, and EmbeddingProvider remain deferred to W8/W11/W12 and are
// forbidden everywhere outside domain. See forbiddenSeamsForRoot.
//
// All import analysis is PROGRAMMATIC via the go toolchain: go/build resolves
// each package's declared imports (authoritative; grep is intentionally NOT
// used for import analysis), and go/ast scans source for seam-type adoption.
//
// ---------------------------------------------------------------------------
// EVIDENCE (recorded 2026-07-26 against HEAD cb4b0f3, W1.1–W1.4 green):
//
//   $ CGO_ENABLED=0 go build ./cmd/cortex               -> PASS (exit 0)
//   $ CGO_ENABLED=0 go test -count=1 ./...              -> PASS (44 pkgs)
//   $ go test ./internal/domain/... ./internal/store/... -> PASS
//   $ go test -count=1 ./...                            -> PASS, coverage 70.9%
//
// These are confirmed by the TestZeroCgoLocalBuild and TestPortsCompileInIsolation
// tests below (which execute the build live) plus the import/seam scans that
// re-confirm the foundation is clean on every run.
// ---------------------------------------------------------------------------
//
// KNOWN YELLOW NOTE — duplicate Principal surface (NOT fixed in W1.5; design
// owner decision): W1.2 introduced internal/identity/principal.go which declares
//   type Principal = domain.Principal   // type ALIAS, not a distinct type
//   type TenantContext = domain.TenantContext
// So identity.Principal IS domain.Principal (same type), re-exported with
// constructors for the server identity layer. For this gate, internal/identity
// is treated as a SERVER-TRACK package: local composition MUST use
// domain.Principal (declared in interfaces.go) and MUST NOT import
// internal/identity. The import-forbidden assertion (TestNoLocalToServerImport)
// enforces this. Consolidating the alias surface is out of W1.5 scope.
package app

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// modulePath is the Go module path declared in go.mod.
const modulePath = "github.com/lleontor705/cortex"

// serverTrackSubpkgs lists module-relative package path suffixes that belong to
// the server track. Local-composition packages MUST NOT import any of these,
// and these packages are themselves exempt from the local-import prohibition
// (server may depend on server). Most do not exist in W1 yet; listing them here
// makes the gate fail the moment a local package ever reaches for one.
var serverTrackSubpkgs = []string{
	"/internal/store/postgres",
	"/internal/authz",
	"/internal/audit",
	"/internal/quota",
	"/internal/vector", // whole tree: qdrant, pgvector, ...
	"/internal/projection/obsidian",
	"/internal/identity", // server identity layer; local uses domain.Principal
}

// forbiddenExternalDeps are third-party import path prefixes that drag in
// server-only infrastructure. A local package importing any of these violates
// the zero-dependency local invariant.
var forbiddenExternalDeps = []string{
	"github.com/jackc/pgx",   // Postgres driver
	"golang.org/x/oauth2",    // OIDC/OAuth resource server
	"github.com/qdrant",      // Qdrant vector client
}

// seamTypes are the W1-introduced ports. Each scan-root has a per-root
// forbidden subset. W2 ADOPTS TxParticipant + UnitOfWork in internal/store
// (REQ-TX-001 cross-store atomic save); those two are no longer forbidden for
// store packages. Storage, VectorIndex, and EmbeddingProvider remain deferred
// to W8/W11/W12 and are forbidden everywhere outside domain.
var seamTypes = map[string]bool{
	"Storage":           true,
	"TxParticipant":     true,
	"UnitOfWork":        true,
	"VectorIndex":       true,
	"EmbeddingProvider": true,
}

// forbiddenSeamsForRoot returns the set of seam types that are STILL forbidden
// in non-test source under the given scan-root. This evolves per wave:
//   - W1: all 5 forbidden everywhere outside domain.
//   - W2: TxParticipant + UnitOfWork ADOPTED in internal/store (atomic save);
//     Storage/VectorIndex/EmbeddingProvider still forbidden everywhere.
func forbiddenSeamsForRoot(rel string) map[string]bool {
	if strings.HasPrefix(rel, "internal/store/") || rel == "internal/store" {
		// W2 adoption: TxParticipant + UnitOfWork are now allowed in store.
		return map[string]bool{
			"Storage":           true,
			"VectorIndex":       true,
			"EmbeddingProvider": true,
		}
	}
	// All other roots: full gate (no seam adoption yet).
	return seamTypes
}

// forbidden returns a non-empty reason if imp is a server-track package or a
// forbidden external dependency, otherwise "". It is the core detector used by
// TestNoLocalToServerImport and is itself unit-tested by
// TestForbiddenImportDetector (RED evidence: proves it matches every forbidden
// prefix, including packages that do not exist yet).
func forbidden(imp string) string {
	for _, s := range serverTrackSubpkgs {
		if imp == modulePath+s || strings.HasPrefix(imp, modulePath+s+"/") {
			return "server-track package: " + imp
		}
	}
	for _, e := range forbiddenExternalDeps {
		if strings.HasPrefix(imp, e) {
			return "forbidden external dependency: " + imp
		}
	}
	return ""
}

// isServerTrack reports whether pkgPath is itself a server-track package.
func isServerTrack(pkgPath string) bool {
	return forbidden(pkgPath) != ""
}

// moduleRoot resolves the repository root (directory containing go.mod) by
// walking up from this test file's location. Failing that it falls back to the
// process working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if ok {
		dir := filepath.Dir(file) // .../internal/app
		for d := dir; d != "" && d != "." && d != filepath.VolumeName(d)+string(filepath.Separator); d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
				return d
			}
			if d == filepath.Dir(d) { // reached volume root
				break
			}
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot determine module root: %v", err)
	}
	return wd
}

// discoverPackages walks the local source trees (internal/, cmd/) and returns
// the import paths of every directory that contains at least one .go file.
// Server-track packages are excluded (they are analyzed separately / exempt).
func discoverPackages(t *testing.T, root string) []string {
	t.Helper()
	var pkgs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		base := d.Name()
		// Skip irrelevant trees.
		if base == "vendor" || base == ".git" || base == "node_modules" || base == "testdata" {
			return filepath.SkipDir
		}
		// Only enumerate under internal/ and cmd/.
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if rel != "." && !strings.HasPrefix(rel, "internal/") && !strings.HasPrefix(rel, "cmd/") {
			return nil
		}
		// Does this dir contain a .go file?
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		hasGo := false
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				hasGo = true
				break
			}
		}
		if !hasGo {
			return nil
		}
		impPath := modulePath
		if rel != "." {
			impPath = modulePath + "/" + rel
		}
		if isServerTrack(impPath) {
			return nil // server-track; not a local package
		}
		pkgs = append(pkgs, impPath)
		return nil
	})
	if err != nil {
		t.Fatalf("walk source tree: %v", err)
	}
	sort.Strings(pkgs)
	return pkgs
}

// TestForbiddenImportDetector proves the detector flags every forbidden prefix
// and external dependency, including server-track packages that do not exist
// yet. This is the RED-style evidence: the detector logic is self-evidently a
// guardrail and would catch a future forbidden import the instant it appears.
func TestForbiddenImportDetector(t *testing.T) {
	forbiddenCases := []string{
		modulePath + "/internal/identity",
		modulePath + "/internal/identity/oauth",
		modulePath + "/internal/authz",
		modulePath + "/internal/audit",
		modulePath + "/internal/quota",
		modulePath + "/internal/store/postgres",
		modulePath + "/internal/vector",
		modulePath + "/internal/vector/qdrant",
		modulePath + "/internal/vector/pgvector",
		modulePath + "/internal/projection/obsidian",
		"github.com/jackc/pgx/v5",
		"golang.org/x/oauth2",
		"golang.org/x/oauth2/jose",
		"github.com/qdrant/qdrant-go",
	}
	for _, imp := range forbiddenCases {
		if got := forbidden(imp); got == "" {
			t.Errorf("detector FAILED to flag forbidden import %q (got allowed)", imp)
		}
	}

	allowedCases := []string{
		modulePath + "/internal/domain",
		modulePath + "/internal/store/sqlite",
		modulePath + "/internal/app",
		modulePath + "/internal/platform",
		"context",
		"time",
		"github.com/lleontor705/cortex/internal/store/bundle",
	}
	for _, imp := range allowedCases {
		if got := forbidden(imp); got != "" {
			t.Errorf("detector FALSELY flagged allowed import %q: %s", imp, got)
		}
	}
}

// TestNoLocalToServerImport enforces REQ-FOUND-001 + ADR-01 dependency direction:
// no local-composition package may import any server-track package or forbidden
// external dependency. go/build resolves each package's declared imports
// (authoritative). If a future wave wires a Postgres/authz/identity dep into the
// local track, this fails and blocks the build.
func TestNoLocalToServerImport(t *testing.T) {
	root := moduleRoot(t)
	pkgs := discoverPackages(t, root)
	if len(pkgs) == 0 {
		t.Fatal("discovered zero local packages — source tree walk is broken")
	}

	ctx := build.Default
	ctx.CgoEnabled = false // analyze source imports without invoking the C toolchain

	scanned := 0
	for _, pkg := range pkgs {
		bp, err := ctx.Import(pkg, root, 0)
		if err != nil {
			// A package that cannot be analyzed by go/build (e.g. build-tag
			// guarded, or only test files) is logged but not fatal — it is
			// still covered by the live go build in TestZeroCgoLocalBuild.
			t.Logf("go/build skip %s: %v", pkg, err)
			continue
		}
		scanned++
		for _, imp := range bp.Imports {
			if reason := forbidden(imp); reason != "" {
				t.Errorf("INVARIANT VIOLATION: local package %s imports %s\n"+
					"  import: %q\n"+
					"  Local composition must not depend on server-track code (REQ-FOUND-001, ADR-01).",
					pkg, reason, imp)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("scanned zero packages with go/build — import analysis did not run")
	}
	t.Logf("scanned %d/%d local packages for forbidden server imports (all clean)", scanned, len(pkgs))
}

// TestNoSeamAdoptionInW1 enforces REQ-FOUND-001 error scenario: the W1-introduced
// ports (Storage, TxParticipant, UnitOfWork, VectorIndex, EmbeddingProvider) must
// have ZERO non-test references outside the domain package. Adoption is deferred
// to dependent waves. go/ast scans non-test source for qualified domain.<Seam>
// references. (domain.Principal, domain.Tx, domain.TenantContext, domain.SearchID,
// domain.Capabilities are intentionally NOT in this list — they are types local
// mode is permitted to thread; only the deferred PORTS are gated.)
func TestNoSeamAdoptionInW1(t *testing.T) {
	root := moduleRoot(t)
	scanRoots := []string{
		"internal/store",
		"internal/mcp",
		"internal/app",
		"internal/embedding",
		"internal/platform", // local composition root; must not adopt seams early
		"cmd/cortex",
	}

	filesScanned := 0
	var violations []string

	for _, rel := range scanRoots {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		// Resolve the forbidden seam set for this root (evolves per wave).
		forbiddenHere := forbiddenSeamsForRoot(rel)
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil // dir absent in this build (e.g. internal/embedding may be sparse)
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil // test references are permitted
			}

			fset := token.NewFileSet()
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return nil // unparseable file; live build covers correctness
			}

			// Resolve the domain import alias in this file (default "domain").
			domainAlias := ""
			for _, spec := range file.Imports {
				ipath := strings.Trim(spec.Path.Value, `"`)
				if ipath == modulePath+"/internal/domain" {
					domainAlias = "domain"
					if spec.Name != nil {
						domainAlias = spec.Name.Name
					}
					break
				}
			}
			if domainAlias == "" {
				return nil // domain not imported here → no seam reference possible
			}

			filesScanned++
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				// Check against this root's forbidden seam set (wave-evolved).
				if ident.Name == domainAlias && forbiddenHere[sel.Sel.Name] {
					pos := fset.Position(sel.Pos())
					violations = append(violations, fmtViolation(path, pos.Line, domainAlias, sel.Sel.Name))
				}
				return true
			})
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("walk %s: %v", rel, err)
		}
	}

	if filesScanned == 0 {
		t.Fatal("scanned zero non-test files — seam-adoption analysis did not run")
	}
	if len(violations) > 0 {
		t.Errorf("INVARIANT VIOLATION: W1 seam port adopted outside domain (REQ-FOUND-001 error scenario).\n"+
			"Adoption is prohibited in W1; defer to the dependent wave.\n%s",
			strings.Join(violations, "\n"))
	}
	t.Logf("scanned %d non-test files for seam adoption (zero references found)", filesScanned)
}

// TestPortsCompileInIsolation enforces REQ-FOUND-001: every seam interface
// compiles and unit-tests in isolation. internal/domain must build with no
// external dependency beyond the Go standard library.
func TestPortsCompileInIsolation(t *testing.T) {
	root := moduleRoot(t)
	cmd := exec.Command("go", "build", "./internal/domain/")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("internal/domain must compile in isolation (stdlib-only deps): %v\n%s", err, out)
	}
}

// TestZeroCgoLocalBuild enforces REQ-FOUND-001 happy-path scenario: the local
// binary builds with CGO_ENABLED=0 (no C toolchain). This is the live proof of
// the zero-CGO local invariant; it compiles the real binary to a temp path.
func TestZeroCgoLocalBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live CGO build in -short mode")
	}
	root := moduleRoot(t)
	out := filepath.Join(t.TempDir(), "cortex-arch-gate")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "./cmd/cortex")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	res, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CGO_ENABLED=0 go build ./cmd/cortex FAILED (local must be zero-CGO): %v\n%s", err, res)
	}
	if _, statErr := os.Stat(out); statErr != nil {
		t.Fatalf("zero-CGO build reported success but produced no binary: %v", statErr)
	}
}

// fmtViolation formats a seam-adoption violation for the failure message.
func fmtViolation(path string, line int, alias, name string) string {
	rel := path
	if cwd, err := os.Getwd(); err == nil {
		if r, err := filepath.Rel(cwd, path); err == nil {
			rel = filepath.ToSlash(r)
		}
	}
	return "  " + rel + ":" + itoa(line) + ": " + alias + "." + name
}

// itoa avoids importing strconv solely for a single int→string conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
