package cortex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lleontor705/cortex/bench/common"
	"github.com/lleontor705/cortex/internal/app"
	"github.com/lleontor705/cortex/internal/domain"
)

// EvidenceIdentity binds the immutable build, binary, corpus, protocol, and
// hardware identities that must match across independent baseline runs under
// design #720. Identity is validated before any database creation.
type EvidenceIdentity struct {
	Commit         string                  `json:"commit"`
	BinarySHA256   string                  `json:"binary_sha256"`
	CorpusSHA256   string                  `json:"corpus_sha256"`
	ProtocolSHA256 string                  `json:"protocol_sha256"`
	Hardware       common.HardwareMetadata `json:"hardware"`
}

// EvidenceRunRequest describes one independent evidence invocation. It is
// constructed by NewEvidenceRunRequest and consumed by RunEvidence.
type EvidenceRunRequest struct {
	EvidenceRoot    string           `json:"evidence_root"`
	OutputDir       string           `json:"output_dir"`
	WorkDir         string           `json:"work_dir"`
	RunID           string           `json:"run_id"`
	Seed            string           `json:"seed"`
	ProtocolVersion string           `json:"protocol_version"`
	Identity        EvidenceIdentity `json:"identity"`
	Corpus          common.Corpus    `json:"-"`
}

// Typed identity validation errors. Each error message contains the field
// name so callers and tests can match on the expected substring.
var (
	ErrCorpusHashMismatch   = errors.New("corpus hash mismatch")
	ErrProtocolHashMismatch = errors.New("protocol hash mismatch")
	ErrBinaryHashMismatch   = errors.New("binary hash mismatch")
	ErrCommitMismatch       = errors.New("commit mismatch")
	ErrHardwareMismatch     = errors.New("hardware mismatch")
)

// NewEvidenceRunRequest loads the corpus from root, stores the identity, and
// returns a request ready for identity validation and ingestion. It does NOT
// validate the identity — call ValidateEvidenceIdentity separately so that
// identity checks run before any database creation.
func NewEvidenceRunRequest(root, outputDir, runID, seed, protocolVersion string, identity EvidenceIdentity) (EvidenceRunRequest, error) {
	if strings.TrimSpace(root) == "" {
		return EvidenceRunRequest{}, fmt.Errorf("evidence root is required")
	}
	if strings.TrimSpace(outputDir) == "" {
		return EvidenceRunRequest{}, fmt.Errorf("output directory is required")
	}
	if strings.TrimSpace(runID) == "" {
		return EvidenceRunRequest{}, fmt.Errorf("run ID is required")
	}
	if strings.TrimSpace(seed) == "" {
		return EvidenceRunRequest{}, fmt.Errorf("seed is required")
	}

	corpus, err := loadEvidenceCorpus(root)
	if err != nil {
		return EvidenceRunRequest{}, fmt.Errorf("load corpus: %w", err)
	}
	if err := corpus.Validate(); err != nil {
		return EvidenceRunRequest{}, fmt.Errorf("corpus validation: %w", err)
	}

	return EvidenceRunRequest{
		EvidenceRoot:    root,
		OutputDir:       outputDir,
		RunID:           runID,
		Seed:            seed,
		ProtocolVersion: protocolVersion,
		Identity:        identity,
		Corpus:          corpus,
	}, nil
}

// ValidateEvidenceIdentity checks that the committed corpus, protocol, commit,
// binary, and hardware identities match the request before any database or
// output is created. Each mismatch returns a typed error whose message contains
// the field name for test matching.
func ValidateEvidenceIdentity(request EvidenceRunRequest) error {
	root := request.EvidenceRoot

	corpusHash, err := evidenceFileSHA256(filepath.Join(root, "corpus.json"))
	if err != nil {
		return fmt.Errorf("corpus hash: %w", err)
	}
	if corpusHash != request.Identity.CorpusSHA256 {
		return fmt.Errorf("corpus hash mismatch: want %s, got %s: %w", corpusHash, request.Identity.CorpusSHA256, ErrCorpusHashMismatch)
	}

	protocolHash, err := evidenceFileSHA256(filepath.Join(root, "protocol.json"))
	if err != nil {
		return fmt.Errorf("protocol hash: %w", err)
	}
	if protocolHash != request.Identity.ProtocolSHA256 {
		return fmt.Errorf("protocol hash mismatch: want %s, got %s: %w", protocolHash, request.Identity.ProtocolSHA256, ErrProtocolHashMismatch)
	}

	if request.Identity.Commit != request.Corpus.Build.Commit {
		return fmt.Errorf("commit mismatch: want %s, got %s: %w", request.Corpus.Build.Commit, request.Identity.Commit, ErrCommitMismatch)
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable for binary hash: %w", err)
	}
	binaryHash, err := evidenceFileSHA256(binaryPath)
	if err != nil {
		return fmt.Errorf("binary hash: %w", err)
	}
	if binaryHash != request.Identity.BinarySHA256 {
		return fmt.Errorf("binary hash mismatch: want %s, got %s: %w", binaryHash, request.Identity.BinarySHA256, ErrBinaryHashMismatch)
	}

	if request.Identity.Hardware != request.Corpus.Hardware {
		return fmt.Errorf("hardware mismatch: want %+v, got %+v: %w", request.Corpus.Hardware, request.Identity.Hardware, ErrHardwareMismatch)
	}

	return nil
}

// NewFreshBenchStores creates a fresh file-based SQLite database in dbDir and
// returns a BenchStores wrapping the new app instance. Each invocation gets
// its own database file and app — no shared state across calls.
func NewFreshBenchStores(ctx context.Context, dbDir string) (*common.BenchStores, error) {
	if strings.TrimSpace(dbDir) == "" {
		return nil, fmt.Errorf("database directory is required")
	}
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	dbPath := filepath.Join(dbDir, "cortex.db")
	os.Setenv("CORTEX_DATABASE_PATH", dbPath)
	os.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")

	app, err := app.Open(ctx, app.Options{InMemory: false})
	if err != nil {
		return nil, fmt.Errorf("open fresh app: %w", err)
	}
	return &common.BenchStores{App: app}, nil
}

// IngestEvidenceCorpus ingests corpus records through the existing
// BenchStores.App.Stores APIs (IngestSession) and returns a map from
// database observation ID to the immutable corpus record ID. No
// internal/* retrieval code is copied or reimplemented.
func IngestEvidenceCorpus(ctx context.Context, stores *common.BenchStores, corpus common.Corpus) (map[int64]string, error) {
	if stores == nil || stores.App == nil || stores.App.Stores == nil {
		return nil, fmt.Errorf("bench stores are unavailable")
	}

	recordsByProject := make(map[string][]common.CorpusRecord)
	for _, record := range corpus.Records {
		recordsByProject[record.Project] = append(recordsByProject[record.Project], record)
	}

	stableIDs := make(map[int64]string, len(corpus.Records))
	for project, records := range recordsByProject {
		observations := make([]domain.Observation, len(records))
		for i, record := range records {
			observations[i] = domain.Observation{
				Title:    record.ID,
				Content:  record.Content,
				Type:     record.Type,
				Project:  record.Project,
				Scope:    record.Scope,
				TopicKey: record.TopicKey,
				Source:   domain.SourceImport,
			}
		}
		sessionID := "evidence-" + project
		if err := stores.IngestSession(ctx, sessionID, project, observations); err != nil {
			return nil, fmt.Errorf("ingest project %q: %w", project, err)
		}
		for i, record := range records {
			stableIDs[observations[i].ID] = record.ID
		}
	}

	return stableIDs, nil
}

func loadEvidenceCorpus(root string) (common.Corpus, error) {
	contents, err := os.ReadFile(filepath.Join(root, "corpus.json"))
	if err != nil {
		return common.Corpus{}, fmt.Errorf("read corpus: %w", err)
	}
	var corpus common.Corpus
	if err := json.Unmarshal(contents, &corpus); err != nil {
		return common.Corpus{}, fmt.Errorf("unmarshal corpus: %w", err)
	}
	return corpus, nil
}

func evidenceFileSHA256(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:]), nil
}
