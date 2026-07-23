package cortex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/bench/common"
)

// TestEvidenceIdentify covers the identity validation and fresh SQLite/app
// ingestion surface owned by task 2.5B (#736) under design #720 and spec #651.
// The full TestEvidence contract in evidence_test.go (owned by 2.5A) remains
// partially RED until downstream tasks 2.5C and 2.5D supply orchestration and
// atomic output.
func TestEvidenceIdentify(t *testing.T) {
	root := filepath.Join("..", "evidence", "cortex-native", "v1")

	t.Run("NewEvidenceRunRequest loads corpus and validates identity", func(t *testing.T) {
		identity := validEvidenceIdentity(t, root)
		request, err := NewEvidenceRunRequest(root, t.TempDir(), "run-identify-001", "seed-42", "cortex-native-v1", identity)
		if err != nil {
			t.Fatalf("NewEvidenceRunRequest() error = %v", err)
		}
		if request.RunID != "run-identify-001" {
			t.Fatalf("request.RunID = %q, want %q", request.RunID, "run-identify-001")
		}
		if request.Seed != "seed-42" {
			t.Fatalf("request.Seed = %q, want %q", request.Seed, "seed-42")
		}
		if request.ProtocolVersion != "cortex-native-v1" {
			t.Fatalf("request.ProtocolVersion = %q, want %q", request.ProtocolVersion, "cortex-native-v1")
		}
		if request.EvidenceRoot != root {
			t.Fatalf("request.EvidenceRoot = %q, want %q", request.EvidenceRoot, root)
		}
		if request.Identity.Commit != identity.Commit {
			t.Fatalf("request.Identity.Commit = %q, want %q", request.Identity.Commit, identity.Commit)
		}
		if request.Identity.CorpusSHA256 != identity.CorpusSHA256 {
			t.Fatalf("request.Identity.CorpusSHA256 = %q, want %q", request.Identity.CorpusSHA256, identity.CorpusSHA256)
		}
		if len(request.Corpus.Records) == 0 {
			t.Fatal("request.Corpus.Records is empty; corpus was not loaded")
		}
	})

	t.Run("ValidateEvidenceIdentity rejects mismatched corpus hash", func(t *testing.T) {
		identity := validEvidenceIdentity(t, root)
		identity.CorpusSHA256 = strings.Repeat("0", 64)
		request, err := NewEvidenceRunRequest(root, t.TempDir(), "run-corpus-mismatch", "seed-42", "cortex-native-v1", identity)
		if err != nil {
			t.Fatalf("NewEvidenceRunRequest() error = %v", err)
		}
		err = ValidateEvidenceIdentity(request)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "corpus") {
			t.Fatalf("ValidateEvidenceIdentity() error = %v, want error containing %q", err, "corpus")
		}
	})

	t.Run("ValidateEvidenceIdentity rejects mismatched protocol hash", func(t *testing.T) {
		identity := validEvidenceIdentity(t, root)
		identity.ProtocolSHA256 = strings.Repeat("0", 64)
		request, err := NewEvidenceRunRequest(root, t.TempDir(), "run-protocol-mismatch", "seed-42", "cortex-native-v1", identity)
		if err != nil {
			t.Fatalf("NewEvidenceRunRequest() error = %v", err)
		}
		err = ValidateEvidenceIdentity(request)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "protocol") {
			t.Fatalf("ValidateEvidenceIdentity() error = %v, want error containing %q", err, "protocol")
		}
	})

	t.Run("ValidateEvidenceIdentity rejects mismatched binary hash", func(t *testing.T) {
		identity := validEvidenceIdentity(t, root)
		identity.BinarySHA256 = strings.Repeat("0", 64)
		request, err := NewEvidenceRunRequest(root, t.TempDir(), "run-binary-mismatch", "seed-42", "cortex-native-v1", identity)
		if err != nil {
			t.Fatalf("NewEvidenceRunRequest() error = %v", err)
		}
		err = ValidateEvidenceIdentity(request)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "binary") {
			t.Fatalf("ValidateEvidenceIdentity() error = %v, want error containing %q", err, "binary")
		}
	})

	t.Run("ValidateEvidenceIdentity rejects mismatched commit", func(t *testing.T) {
		identity := validEvidenceIdentity(t, root)
		identity.Commit = "different-commit"
		request, err := NewEvidenceRunRequest(root, t.TempDir(), "run-commit-mismatch", "seed-42", "cortex-native-v1", identity)
		if err != nil {
			t.Fatalf("NewEvidenceRunRequest() error = %v", err)
		}
		err = ValidateEvidenceIdentity(request)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "commit") {
			t.Fatalf("ValidateEvidenceIdentity() error = %v, want error containing %q", err, "commit")
		}
	})

	t.Run("ValidateEvidenceIdentity rejects mismatched hardware", func(t *testing.T) {
		identity := validEvidenceIdentity(t, root)
		identity.Hardware.CPU = "different-cpu"
		request, err := NewEvidenceRunRequest(root, t.TempDir(), "run-hardware-mismatch", "seed-42", "cortex-native-v1", identity)
		if err != nil {
			t.Fatalf("NewEvidenceRunRequest() error = %v", err)
		}
		err = ValidateEvidenceIdentity(request)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "hardware") {
			t.Fatalf("ValidateEvidenceIdentity() error = %v, want error containing %q", err, "hardware")
		}
	})

	t.Run("ValidateEvidenceIdentity accepts a valid identity", func(t *testing.T) {
		identity := validEvidenceIdentity(t, root)
		request, err := NewEvidenceRunRequest(root, t.TempDir(), "run-valid-identity", "seed-42", "cortex-native-v1", identity)
		if err != nil {
			t.Fatalf("NewEvidenceRunRequest() error = %v", err)
		}
		if err := ValidateEvidenceIdentity(request); err != nil {
			t.Fatalf("ValidateEvidenceIdentity() error = %v, want nil", err)
		}
	})

	t.Run("NewFreshBenchStores creates a fresh file-based SQLite per invocation", func(t *testing.T) {
		ctx := context.Background()
		dir1 := t.TempDir()
		stores1, err := NewFreshBenchStores(ctx, dir1)
		if err != nil {
			t.Fatalf("NewFreshBenchStores(dir1) error = %v", err)
		}
		t.Cleanup(func() { _ = stores1.Close() })

		dbPath1 := filepath.Join(dir1, "cortex.db")
		if _, statErr := os.Stat(dbPath1); statErr != nil {
			t.Fatalf("database file was not created at %q: %v", dbPath1, statErr)
		}

		dir2 := t.TempDir()
		stores2, err := NewFreshBenchStores(ctx, dir2)
		if err != nil {
			t.Fatalf("NewFreshBenchStores(dir2) error = %v", err)
		}
		t.Cleanup(func() { _ = stores2.Close() })

		dbPath2 := filepath.Join(dir2, "cortex.db")
		if _, statErr := os.Stat(dbPath2); statErr != nil {
			t.Fatalf("database file was not created at %q: %v", dbPath2, statErr)
		}

		if dbPath1 == dbPath2 {
			t.Fatal("two invocations share the same database path; fresh SQLite was not created")
		}
		if stores1 == stores2 {
			t.Fatal("two invocations returned the same BenchStores instance; no fresh app was created")
		}
	})

	t.Run("IngestEvidenceCorpus ingests records through existing app APIs", func(t *testing.T) {
		ctx := context.Background()
		dir := t.TempDir()
		stores, err := NewFreshBenchStores(ctx, dir)
		if err != nil {
			t.Fatalf("NewFreshBenchStores() error = %v", err)
		}
		t.Cleanup(func() { _ = stores.Close() })

		corpus := loadCorpusForTest(t, root)
		stableIDs, err := IngestEvidenceCorpus(ctx, stores, corpus)
		if err != nil {
			t.Fatalf("IngestEvidenceCorpus() error = %v", err)
		}
		if len(stableIDs) != len(corpus.Records) {
			t.Fatalf("len(stableIDs) = %d, want %d", len(stableIDs), len(corpus.Records))
		}
		for _, record := range corpus.Records {
			found := false
			for _, stableID := range stableIDs {
				if stableID == record.ID {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("corpus record %q was not ingested; stable ID missing from map", record.ID)
			}
		}
	})

	t.Run("IngestEvidenceCorpus uses a fresh database per invocation", func(t *testing.T) {
		ctx := context.Background()
		corpus := loadCorpusForTest(t, root)

		dir1 := t.TempDir()
		stores1, err := NewFreshBenchStores(ctx, dir1)
		if err != nil {
			t.Fatalf("NewFreshBenchStores(dir1) error = %v", err)
		}
		t.Cleanup(func() { _ = stores1.Close() })
		stableIDs1, err := IngestEvidenceCorpus(ctx, stores1, corpus)
		if err != nil {
			t.Fatalf("IngestEvidenceCorpus(stores1) error = %v", err)
		}

		dir2 := t.TempDir()
		stores2, err := NewFreshBenchStores(ctx, dir2)
		if err != nil {
			t.Fatalf("NewFreshBenchStores(dir2) error = %v", err)
		}
		t.Cleanup(func() { _ = stores2.Close() })
		stableIDs2, err := IngestEvidenceCorpus(ctx, stores2, corpus)
		if err != nil {
			t.Fatalf("IngestEvidenceCorpus(stores2) error = %v", err)
		}

		if len(stableIDs1) == 0 || len(stableIDs2) == 0 {
			t.Fatal("stable IDs are empty; ingestion did not occur")
		}
		// Database IDs are auto-incremented and depend on insertion order
		// (Go map iteration is non-deterministic). The correct invariant is
		// that both fresh databases contain the same set of corpus record IDs.
		records1 := make(map[string]struct{}, len(stableIDs1))
		for _, stableID := range stableIDs1 {
			records1[stableID] = struct{}{}
		}
		records2 := make(map[string]struct{}, len(stableIDs2))
		for _, stableID := range stableIDs2 {
			records2[stableID] = struct{}{}
		}
		if len(records1) != len(records2) {
			t.Fatalf("record count differs: %d vs %d", len(records1), len(records2))
		}
		for recordID := range records1 {
			if _, exists := records2[recordID]; !exists {
				t.Fatalf("record %q exists in first database but not second", recordID)
			}
		}
	})
}

// validEvidenceIdentity constructs an EvidenceIdentity that matches the
// committed corpus and protocol at root and the current test binary.
func validEvidenceIdentity(t *testing.T, root string) EvidenceIdentity {
	t.Helper()
	corpus := loadCorpusForTest(t, root)
	binary, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	return EvidenceIdentity{
		Commit:         corpus.Build.Commit,
		BinarySHA256:   fileHashSHA256(t, binary),
		CorpusSHA256:   fileHashSHA256(t, filepath.Join(root, "corpus.json")),
		ProtocolSHA256: fileHashSHA256(t, filepath.Join(root, "protocol.json")),
		Hardware:       corpus.Hardware,
	}
}

func loadCorpusForTest(t *testing.T, root string) common.Corpus {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, "corpus.json"))
	if err != nil {
		t.Fatalf("ReadFile(corpus.json) error = %v", err)
	}
	var corpus common.Corpus
	if err := json.Unmarshal(contents, &corpus); err != nil {
		t.Fatalf("Unmarshal(corpus.json) error = %v", err)
	}
	return corpus
}

func fileHashSHA256(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}
