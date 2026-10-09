package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/migration"
	"github.com/lleontor705/cortex/v2/testutil"
)

// newV2TestDB builds an in-memory SQLite database with the real v2 migration
// line applied (baseline 2001 + follow-ups) and the cortex_meta schema
// identity stamped, mirroring what app.Open / InitV2Database produce.
func newV2TestDB(t *testing.T) *sql.DB {
	t.Helper()
	v2reg, err := migration.NewV2Registry()
	if err != nil {
		t.Fatalf("newV2Registry: %v", err)
	}
	reg := migration.NewRegistry()
	for _, m := range v2reg.V2Migrations() {
		reg.Register(m)
	}
	testDB := testutil.NewTestDBWithMigrations(t, reg)
	db := testDB.DB()

	// Stamp the schema identity exactly as writeIdentity does for the
	// baseline (family cortex-v2, version 001, baseline checksum).
	baseline, err := migration.NewV2Baseline()
	if err != nil {
		t.Fatalf("newV2Baseline: %v", err)
	}
	id := baseline.Identity()
	for _, row := range [][2]string{
		{"schema_family", id.Family},
		{"schema_version", id.Version},
		{"schema_checksum", id.Checksum},
	} {
		if _, err := db.Exec(`INSERT INTO cortex_meta (key, value) VALUES (?, ?)`, row[0], row[1]); err != nil {
			t.Fatalf("stamp cortex_meta %s: %v", row[0], err)
		}
	}
	return db
}

func seedFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`INSERT INTO sessions (id, project, directory, started_at) VALUES ('ses-1', 'proj-a', '/tmp/a', '2026-10-01T10:00:00Z')`,
		`INSERT INTO sessions (id, project, directory, started_at, ended_at, summary) VALUES ('ses-2', 'proj-b', '/tmp/b', '2026-10-02T11:00:00Z', '2026-10-02T12:00:00Z', 'done')`,
		`INSERT INTO observations (id, session_id, type, title, content, project, scope, confidence, source, created_at, updated_at)
		 VALUES (1, 'ses-1', 'decision', 'Use SQLite', 'Zero-CGO sqlite chosen for local mode', 'proj-a', 'project', 1.0, 'manual', '2026-10-01T10:05:00Z', '2026-10-01T10:05:00Z')`,
		`INSERT INTO observations (id, session_id, type, title, content, project, scope, confidence, source, tags, created_at, updated_at)
		 VALUES (2, 'ses-2', 'bugfix', 'Fixed N+1', 'Batched GetByIDs in list path', 'proj-b', 'project', 0.9, 'ai', '["perf"]', '2026-10-02T11:30:00Z', '2026-10-02T11:30:00Z')`,
		`INSERT INTO observations (id, session_id, type, title, content, project, scope, confidence, source, created_at, updated_at)
		 VALUES (3, 'ses-1', 'manual', 'Note', 'A standalone note', 'proj-a', 'personal', 1.0, 'manual', '2026-10-03T09:00:00Z', '2026-10-03T09:00:00Z')`,
		`INSERT INTO edges (id, from_obs_id, to_obs_id, relation_type, weight, confidence, source)
		 VALUES (100, 1, 2, 'references', 2.0, 0.8, 'ai')`,
		`INSERT INTO observation_vectors (observation_id, embedding_model, dimensions) VALUES (1, 'qwen3-embedding', 4096)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed: %v\nsql: %s", err, s)
		}
	}
}

func mustExport(t *testing.T, db *sql.DB, out string, opts ...func(*ExportOptions)) *ExportResult {
	t.Helper()
	eo := ExportOptions{Out: out, ToolVersion: "test"}
	for _, fn := range opts {
		fn(&eo)
	}
	res, err := (&Exporter{DB: db}).Export(context.Background(), eo)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	return res
}

// snapshotRows reads the portable projections from a database for comparison.
func snapshotRows(t *testing.T, db *sql.DB) (obs [][2]string, edges []string, sessions []string) {
	t.Helper()
	rows, err := db.Query(`SELECT id, title FROM observations ORDER BY id`)
	if err != nil {
		t.Fatalf("read obs: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			t.Fatal(err)
		}
		obs = append(obs, [2]string{fmtInt(id), title})
	}
	erows, err := db.Query(`SELECT from_obs_id, to_obs_id, relation_type FROM edges ORDER BY id`)
	if err != nil {
		t.Fatalf("read edges: %v", err)
	}
	defer func() { _ = erows.Close() }()
	for erows.Next() {
		var f, to int64
		var rt string
		if err := erows.Scan(&f, &to, &rt); err != nil {
			t.Fatal(err)
		}
		edges = append(edges, fmtInt(f)+"->"+fmtInt(to)+":"+rt)
	}
	srows, err := db.Query(`SELECT id, project FROM sessions ORDER BY id`)
	if err != nil {
		t.Fatalf("read sessions: %v", err)
	}
	defer func() { _ = srows.Close() }()
	for srows.Next() {
		var id, project string
		if err := srows.Scan(&id, &project); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, id+":"+project)
	}
	return obs, edges, sessions
}

func fmtInt(n int64) string { return strconv.FormatInt(n, 10) }

func TestRoundTripRestore(t *testing.T) {
	src := newV2TestDB(t)
	seedFixture(t, src)

	out := filepath.Join(t.TempDir(), "roundtrip.tar.gz")
	res := mustExport(t, src, out)
	if res.Observations != 3 || res.Edges != 1 || res.Sessions != 2 {
		t.Fatalf("export counts = obs=%d edges=%d sessions=%d, want 3/1/2", res.Observations, res.Edges, res.Sessions)
	}
	if res.SchemaVer != migration.V2BaselineVersion {
		t.Fatalf("export schema version = %q, want %q", res.SchemaVer, migration.V2BaselineVersion)
	}

	// Wipe scenario: a fresh empty v2 database.
	dst := newV2TestDB(t)
	got, err := (&Restorer{DB: dst}).Restore(context.Background(), RestoreOptions{From: out})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got.ObservationsRestored != 3 || got.EdgesRestored != 1 || got.SessionsRestored != 2 {
		t.Fatalf("restore counts = %+v, want 3 obs / 1 edge / 2 sessions", got)
	}

	// Equal reads across source and restored databases.
	srcObs, srcEdges, srcSess := snapshotRows(t, src)
	dstObs, dstEdges, dstSess := snapshotRows(t, dst)
	if len(srcObs) != len(dstObs) || len(srcEdges) != len(dstEdges) || len(srcSess) != len(dstSess) {
		t.Fatalf("row counts differ: obs %d/%d edges %d/%d sessions %d/%d",
			len(srcObs), len(dstObs), len(srcEdges), len(dstEdges), len(srcSess), len(dstSess))
	}
	for i := range srcObs {
		if srcObs[i] != dstObs[i] {
			t.Errorf("obs %d: got %v want %v", i, dstObs[i], srcObs[i])
		}
	}
	for i := range srcEdges {
		if srcEdges[i] != dstEdges[i] {
			t.Errorf("edge %d: got %v want %v", i, dstEdges[i], srcEdges[i])
		}
	}
	for i := range srcSess {
		if srcSess[i] != dstSess[i] {
			t.Errorf("session %d: got %v want %v", i, dstSess[i], srcSess[i])
		}
	}

	// Content fidelity spot check including full text.
	var content string
	if err := dst.QueryRow(`SELECT content FROM observations WHERE id = 2`).Scan(&content); err != nil {
		t.Fatalf("read restored content: %v", err)
	}
	if content != "Batched GetByIDs in list path" {
		t.Errorf("restored content = %q", content)
	}

	// Embedding state is exported but vectors are not restored: the restored
	// DB has no vectors (pending re-embed), the source does.
	var vecCount int
	if err := dst.QueryRow(`SELECT COUNT(*) FROM observation_vectors`).Scan(&vecCount); err != nil {
		t.Fatal(err)
	}
	if vecCount != 0 {
		t.Errorf("restored observation_vectors rows = %d, want 0 (vectors are regenerated, not restored)", vecCount)
	}
}

func TestRestoreIdempotent(t *testing.T) {
	src := newV2TestDB(t)
	seedFixture(t, src)
	out := filepath.Join(t.TempDir(), "idem.tar.gz")
	mustExport(t, src, out)

	dst := newV2TestDB(t)
	r := &Restorer{DB: dst}
	first, err := r.Restore(context.Background(), RestoreOptions{From: out})
	if err != nil {
		t.Fatalf("first restore: %v", err)
	}
	second, err := r.Restore(context.Background(), RestoreOptions{From: out})
	if err != nil {
		t.Fatalf("second restore: %v", err)
	}
	if second.ObservationsSkipped != first.ObservationsRestored ||
		second.EdgesSkipped != first.EdgesRestored ||
		second.SessionsSkipped != first.SessionsRestored {
		t.Fatalf("idempotency broken: first=%+v second=%+v", first, second)
	}
	if second.ObservationsRestored != 0 || second.EdgesRestored != 0 || second.SessionsRestored != 0 {
		t.Fatalf("re-run restored rows again: %+v", second)
	}
	var n int
	if err := dst.QueryRow(`SELECT COUNT(*) FROM observations`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("observations after re-run = %d (%v), want 3", n, err)
	}
}

func TestRestoreRefusesNewerSchema(t *testing.T) {
	src := newV2TestDB(t)
	seedFixture(t, src)
	out := filepath.Join(t.TempDir(), "future.tar.gz")
	mustExport(t, src, out)

	// Target claims an OLDER schema than the archive → refuse.
	dst := newV2TestDB(t)
	if _, err := dst.Exec(`UPDATE cortex_meta SET value = '000' WHERE key = 'schema_version'`); err != nil {
		t.Fatal(err)
	}
	_, err := (&Restorer{DB: dst}).Restore(context.Background(), RestoreOptions{From: out})
	var compatErr *ErrIncompatibleSchema
	if !errors.As(err, &compatErr) {
		t.Fatalf("restore error = %v, want ErrIncompatibleSchema", err)
	}
	if compatErr.Backup != migration.V2BaselineVersion || compatErr.Current != "000" {
		t.Errorf("compat err = %+v", compatErr)
	}
	// Nothing was written.
	var n int
	if err := dst.QueryRow(`SELECT COUNT(*) FROM observations`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("observations after refused restore = %d (%v), want 0", n, err)
	}
}

func TestRestoreRefusesTamperedManifest(t *testing.T) {
	src := newV2TestDB(t)
	seedFixture(t, src)
	out := filepath.Join(t.TempDir(), "tampered.tar.gz")
	mustExport(t, src, out)

	tampered := filepath.Join(t.TempDir(), "tampered-copy.tar.gz")
	if err := rewriteTarTamperPart(out, tampered, ObservationsPart); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	dst := newV2TestDB(t)
	_, err := (&Restorer{DB: dst}).Restore(context.Background(), RestoreOptions{From: tampered})
	var tamperErr *ErrTampered
	if !errors.As(err, &tamperErr) {
		t.Fatalf("restore error = %v, want ErrTampered", err)
	}
	if tamperErr.Part != ObservationsPart {
		t.Errorf("tampered part = %q, want %q", tamperErr.Part, ObservationsPart)
	}
	var n int
	if err := dst.QueryRow(`SELECT COUNT(*) FROM observations`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("observations after tampered restore = %d (%v), want 0", n, err)
	}
}

// rewriteTarTamperPart rewrites the archive flipping one byte inside the
// named part WITHOUT updating the manifest (simulated corruption/tamper).
func rewriteTarTamperPart(srcPath, dstPath, part string) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	outFile, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = outFile.Close() }()
	gzOut := gzip.NewWriter(outFile)
	tw := tar.NewWriter(gzOut)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if hdr.Name == part && len(data) > 10 {
			data[5] ^= 0xFF
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gzOut.Close()
}

func TestNoSecretsExported(t *testing.T) {
	src := newV2TestDB(t)
	seedFixture(t, src)
	secret := "sk-live-abcdef1234567890"
	t.Setenv("CORTEX_EMBEDDING_API_KEY", secret)
	secrets := CollectEnvSecrets()
	if len(secrets) == 0 {
		t.Fatal("CollectEnvSecrets returned no secrets for a set env key")
	}
	eo := ExportOptions{Out: "", ToolVersion: "test", Secrets: secrets}

	// Positive path first: no secret in content → export succeeds and the
	// archive bytes (raw and decompressed) never contain the secret value.
	clean := filepath.Join(t.TempDir(), "clean.tar.gz")
	eo.Out = clean
	if _, err := (&Exporter{DB: src}).Export(context.Background(), eo); err != nil {
		t.Fatalf("clean export: %v", err)
	}
	scanArchiveForSecret(t, clean, secret)

	// Fail-closed path: an observation whose content contains the configured
	// key value must abort the export.
	if _, err := src.Exec(`UPDATE observations SET content = ? WHERE id = 3`, "token "+secret+" leaked"); err != nil {
		t.Fatal(err)
	}
	leak := filepath.Join(t.TempDir(), "leak.tar.gz")
	eo.Out = leak
	_, err := (&Exporter{DB: src}).Export(context.Background(), eo)
	var secretErr *ErrSecretDetected
	if !errors.As(err, &secretErr) {
		t.Fatalf("export error = %v, want ErrSecretDetected", err)
	}
	if secretErr.Name != "CORTEX_EMBEDDING_API_KEY" {
		t.Errorf("secret name = %q", secretErr.Name)
	}
}

func scanArchiveForSecret(t *testing.T, path, secret string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("archive bytes contain the configured secret value")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	decompressed, err := io.ReadAll(gzr)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(decompressed, []byte(secret)) {
		t.Fatal("decompressed archive contains the configured secret value")
	}
}

func TestCollectEnvSecretsIgnoresShortValues(t *testing.T) {
	t.Setenv("CORTEX_HTTP_TOKEN", "abc")
	for _, s := range CollectEnvSecrets() {
		if s.Name == "CORTEX_HTTP_TOKEN" {
			t.Fatalf("short env value collected: %+v", s)
		}
	}
	// Long values are collected.
	t.Setenv("CORTEX_HTTP_TOKEN", "a-really-long-token-value")
	found := false
	for _, s := range CollectEnvSecrets() {
		if s.Name == "CORTEX_HTTP_TOKEN" {
			found = true
		}
	}
	if !found {
		t.Fatal("long CORTEX_HTTP_TOKEN not collected")
	}
}

func TestSchemaVersionParsing(t *testing.T) {
	if err := checkSchemaCompatible("001", "001"); err != nil {
		t.Errorf("equal versions refused: %v", err)
	}
	if err := checkSchemaCompatible("000", "001"); err != nil {
		t.Errorf("older archive refused: %v", err)
	}
	if err := checkSchemaCompatible("002", "001"); err == nil {
		t.Error("newer archive accepted")
	}
	if err := checkSchemaCompatible("bogus", "001"); err == nil {
		t.Error("malformed archive version accepted")
	}
	if err := checkSchemaCompatible("001", ""); err == nil {
		t.Error("empty target version accepted")
	}
}

func TestExportRefusesNonV2Database(t *testing.T) {
	// A database without cortex_meta identity must be refused (fail-closed).
	reg := migration.NewRegistry()
	reg.Register(migration.Migration{
		Version: 1,
		Name:    "bare",
		UpSQL:   "CREATE TABLE t (id INTEGER PRIMARY KEY)",
	})
	db := testutil.NewTestDBWithMigrations(t, reg).DB()
	_, err := (&Exporter{DB: db}).Export(context.Background(), ExportOptions{Out: filepath.Join(t.TempDir(), "x.tar.gz")})
	if err == nil || !strings.Contains(err.Error(), "schema identity") {
		t.Fatalf("export error = %v, want schema identity refusal", err)
	}
}

// TestRestoreIgnoresTraversalEntries proves Zip Slip immunity: an archive
// containing a traversal entry must not write anything outside the staging
// directory, and the restore of the legit parts still succeeds.
func TestRestoreIgnoresTraversalEntries(t *testing.T) {
	src := newV2TestDB(t)
	seedFixture(t, src)
	out := filepath.Join(t.TempDir(), "zipslip.tar.gz")
	mustExport(t, src, out)

	// Rebuild the archive with an extra malicious traversal entry.
	malicious := filepath.Join(t.TempDir(), "zipslip-copy.tar.gz")
	if err := appendTarEntry(out, malicious, "../evil.txt", []byte("pwned")); err != nil {
		t.Fatalf("append entry: %v", err)
	}
	outside := filepath.Join(filepath.Dir(t.TempDir()), "evil.txt")

	dst := newV2TestDB(t)
	res, err := (&Restorer{DB: dst}).Restore(context.Background(), RestoreOptions{From: malicious})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if res.ObservationsRestored != 3 {
		t.Fatalf("observations restored = %d, want 3", res.ObservationsRestored)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("traversal entry escaped the staging directory: %v", err)
	}
}

// appendTarEntry copies the archive adding one extra entry.
func appendTarEntry(srcPath, dstPath, name string, data []byte) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	outFile, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = outFile.Close() }()
	gzOut := gzip.NewWriter(outFile)
	tw := tar.NewWriter(gzOut)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(body); err != nil {
			return err
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data))}); err != nil {
		return err
	}
	if _, err := tw.Write(data); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gzOut.Close()
}

func TestManifestShape(t *testing.T) {
	src := newV2TestDB(t)
	seedFixture(t, src)
	out := filepath.Join(t.TempDir(), "shape.tar.gz")
	mustExport(t, src, out)

	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	found := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		found[hdr.Name] = true
		if hdr.Name == ManifestName {
			var m Manifest
			if err := json.NewDecoder(tr).Decode(&m); err != nil {
				t.Fatalf("decode manifest: %v", err)
			}
			if m.Format != Format || m.FormatVersion != FormatVersion {
				t.Errorf("manifest identity = %s/%d", m.Format, m.FormatVersion)
			}
			if m.SchemaVersion != migration.V2BaselineVersion {
				t.Errorf("manifest schema_version = %q", m.SchemaVersion)
			}
			for _, part := range []string{ObservationsPart, EdgesPart, SessionsPart} {
				p, ok := m.Parts[part]
				if !ok {
					t.Errorf("manifest missing part %q", part)
					continue
				}
				if p.Count == 0 {
					t.Errorf("part %q count = 0", part)
				}
				if len(p.SHA256) != 64 {
					t.Errorf("part %q sha256 length = %d", part, len(p.SHA256))
				}
			}
			if m.Notes["embeddings"] == "" {
				t.Error("manifest missing embeddings note")
			}
		}
	}
	for _, want := range []string{ManifestName, ObservationsPart, EdgesPart, SessionsPart} {
		if !found[want] {
			t.Errorf("archive missing entry %q", want)
		}
	}
}

// TestSHA256Reference guards the hashing used for manifests.
func TestSHA256Reference(t *testing.T) {
	sum := sha256.Sum256([]byte("abc"))
	if hex.EncodeToString(sum[:]) != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal("sha256 reference mismatch")
	}
}
