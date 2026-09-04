package cortex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lleontor705/cortex/v2/bench/common"
)

var (
	// ErrEvidenceOutputExists identifies a run that would overwrite evidence.
	ErrEvidenceOutputExists = errors.New("evidence output already exists")
	// ErrExternalProviderConfigured identifies an offline-policy violation.
	ErrExternalProviderConfigured = errors.New("external provider configuration is forbidden")
)

var externalProviderEnvironment = []string{
	"OLLAMA_ENDPOINT",
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
	"CORTEX_EMBEDDING_PROVIDER",
	"CORTEX_EMBEDDING_API_KEY",
	"CORTEX_LLM_PROVIDER",
	"CORTEX_LLM_API_KEY",
	"CORTEX_JUDGE_PROVIDER",
	"CORTEX_BENCH_NETWORK",
}

// RefuseExternalProviders enforces the preregistered offline evidence policy.
func RefuseExternalProviders() error {
	for _, name := range externalProviderEnvironment {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return fmt.Errorf("external provider %s is configured: %w", name, ErrExternalProviderConfigured)
		}
	}
	return nil
}

// WriteEvidenceOutput validates and stages all evidence artifacts before one
// directory rename makes the complete output visible to readers.
func WriteEvidenceOutput(outputDir string, raw BaselineRun, report common.EvidenceReport, run common.IndependentRun) (err error) {
	if strings.TrimSpace(outputDir) == "" {
		return fmt.Errorf("evidence output directory is required")
	}
	if err := refuseExistingEvidenceOutput(outputDir); err != nil {
		return err
	}

	reportJSON, err := common.SerializeEvidenceReport(report)
	if err != nil {
		return fmt.Errorf("serialize evidence report: %w", err)
	}
	rawJSON, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize raw evidence: %w", err)
	}
	rawJSON = append(rawJSON, '\n')
	runJSON, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize independent run: %w", err)
	}
	runJSON = append(runJSON, '\n')

	parent := filepath.Dir(outputDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create evidence output parent: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".evidence-staging-")
	if err != nil {
		return fmt.Errorf("create evidence staging directory: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(staging); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("remove evidence staging directory: %w", removeErr))
		}
	}()

	if err := os.WriteFile(filepath.Join(staging, "raw.json"), rawJSON, 0o600); err != nil {
		return fmt.Errorf("write staged raw evidence: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "report.json"), reportJSON, 0o600); err != nil {
		return fmt.Errorf("write staged evidence report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "independent-run.json"), runJSON, 0o600); err != nil {
		return fmt.Errorf("write staged independent run: %w", err)
	}
	if _, err := os.Stat(outputDir); err == nil {
		return fmt.Errorf("%s: %w", outputDir, ErrEvidenceOutputExists)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("recheck evidence output: %w", err)
	}
	if err := os.Rename(staging, outputDir); err != nil {
		if _, statErr := os.Stat(outputDir); statErr == nil {
			return fmt.Errorf("%s: %w", outputDir, ErrEvidenceOutputExists)
		}
		return fmt.Errorf("publish evidence output: %w", err)
	}
	return nil
}

func refuseExistingEvidenceOutput(outputDir string) error {
	if strings.TrimSpace(outputDir) == "" {
		return fmt.Errorf("evidence output directory is required")
	}
	if _, err := os.Stat(outputDir); err == nil {
		return fmt.Errorf("%s: %w", outputDir, ErrEvidenceOutputExists)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat evidence output: %w", err)
	}
	return nil
}
