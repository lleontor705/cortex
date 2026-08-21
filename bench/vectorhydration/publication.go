package vectorhydration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type outputManifest struct {
	SchemaVersion string   `json:"schema_version"`
	Campaign      string   `json:"campaign"`
	Phase         string   `json:"phase"`
	Run           string   `json:"run"`
	InputSHA256   string   `json:"input_sha256"`
	ResultCount   int      `json:"result_count"`
	RecordsSHA256 string   `json:"records_sha256"`
	Raw           []rawRef `json:"raw"`
}
type rawRef struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

const outputManifestVersion = "1.0.0"

func hashBytes(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func canonical(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func ValidateOutput(dir string) error {
	if err := validatePathComponents(dir, true); err != nil {
		return err
	}
	for _, name := range []string{".complete", "manifest.json", "records.json", "input-manifest.json"} {
		if err := validateFilePath(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	marker, err := os.ReadFile(filepath.Join(dir, ".complete"))
	if err != nil || string(marker) != "complete\n" {
		return fmt.Errorf("incomplete output: %w", err)
	}
	mb, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}
	var om outputManifest
	if err = json.Unmarshal(mb, &om); err != nil {
		return err
	}
	if om.SchemaVersion != outputManifestVersion || om.Campaign != CampaignID {
		return errors.New("invalid output schema or campaign")
	}
	rb, err := os.ReadFile(filepath.Join(dir, "records.json"))
	if err != nil {
		return err
	}
	if hashBytes(rb) != om.RecordsSHA256 {
		return errors.New("records hash mismatch")
	}
	if om.ResultCount <= 0 || len(om.Raw) != om.ResultCount*2 {
		return errors.New("invalid output count")
	}
	var records []Result
	if err := json.Unmarshal(rb, &records); err != nil {
		return err
	}
	if len(records) != om.ResultCount {
		return errors.New("record count mismatch")
	}
	for i, record := range records {
		expected := sealedRequestIdentity(Manifest{Campaign: CampaignManifest{ID: om.Campaign}, Phase: PhaseManifest{ID: om.Phase}, Run: RunManifest{ID: om.Run}}, ScheduleEntry{Block: record.Block, Cell: record.Cell}, record.Measurement, record.Sequence)
		if record.Request.Identity != expected {
			return fmt.Errorf("record %d request identity mismatch", i)
		}
	}
	if err := validateRawConsumption(om.Raw, records); err != nil {
		return err
	}
	return verifyRawHashes(dir, om.Raw)
}
func verifyRawHashes(root string, refs []rawRef) error {
	seen := map[string]bool{}
	rawDir := filepath.Join(root, "raw")
	if err := validatePathComponents(rawDir, true); err != nil {
		return err
	}
	entries, err := os.ReadDir(rawDir)
	if err != nil {
		return err
	}
	if len(entries) != len(refs) {
		return errors.New("raw directory contains unreferenced files")
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref.Name) == "" || filepath.Base(ref.Name) != ref.Name || filepath.IsAbs(ref.Name) || seen[ref.Name] {
			return errors.New("raw reference must be a unique basename")
		}
		seen[ref.Name] = true
		path := filepath.Join(rawDir, ref.Name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("raw reference cannot be symlink")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if hashBytes(b) != ref.SHA256 {
			return fmt.Errorf("raw hash mismatch: %s", ref.Name)
		}
	}
	for _, entry := range entries {
		if !seen[entry.Name()] {
			return errors.New("raw directory contains unreferenced files")
		}
	}
	return nil
}

func validatePathComponents(path string, directory bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	current := volume + string(filepath.Separator)
	rest := strings.TrimPrefix(abs, volume)
	for _, part := range strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' }) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("publication path cannot contain symlinks")
		}
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if directory && !info.IsDir() {
		return errors.New("publication path is not a directory")
	}
	return nil
}

func validateFilePath(path string) error {
	if err := validatePathComponents(path, false); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("publication file must be regular")
	}
	return nil
}

func validateRawConsumption(refs []rawRef, records []Result) error {
	if len(refs) != len(records)*2 {
		return errors.New("raw references must match record count")
	}
	declared := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.Name) == "" || filepath.Base(ref.Name) != ref.Name || filepath.IsAbs(ref.Name) || declared[ref.Name] {
			return errors.New("raw reference must be a unique basename")
		}
		declared[ref.Name] = true
	}
	used := make(map[string]int, len(refs))
	for i, record := range records {
		if record.RawStdout == "" || record.RawStderr == "" || record.RawStdout == record.RawStderr {
			return fmt.Errorf("record %d must consume distinct stdout and stderr refs", i)
		}
		if !declared[record.RawStdout] || !declared[record.RawStderr] {
			return fmt.Errorf("record %d references undeclared raw output", i)
		}
		used[record.RawStdout]++
		used[record.RawStderr]++
	}
	for name := range declared {
		if used[name] != 1 {
			return fmt.Errorf("raw reference %q must be consumed exactly once", name)
		}
	}
	return nil
}
