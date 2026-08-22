package vectorhydration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateOutputRejectsRecordIdentityMutation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	f := &fakeExecutor{output: []byte("BenchmarkHydrateLegacyGetByID_N100-8 1 12 ns/op 3 B/op 2 allocs/op\n")}
	if err := Collect(context.Background(), collectorManifest(t), collectorConfig(dir, f)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "records.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records []Result
	if err := json.Unmarshal(b, &records); err != nil {
		t.Fatal(err)
	}
	records[0].Request.Identity += "/tampered"
	mutated, _ := canonical(records)
	if err := os.WriteFile(path, mutated, 0644); err != nil {
		t.Fatal(err)
	}
	var manifest outputManifest
	mb, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	_ = json.Unmarshal(mb, &manifest)
	manifest.RecordsSHA256 = hashBytes(mutated)
	mb, _ = canonical(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutput(dir); err == nil {
		t.Fatal("mutated request identity accepted")
	}
}

func TestValidatePathComponentsRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := validatePathComponents(filepath.Join(link, "child"), false); err == nil {
		t.Fatal("symlink path component accepted")
	}
}

func TestVerifyRawHashesRejectsUnreferencedPadding(t *testing.T) {
	root := t.TempDir()
	raw := filepath.Join(root, "raw")
	if err := os.Mkdir(raw, 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte("output")
	if err := os.WriteFile(filepath.Join(raw, "stdout"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(raw, "padding"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyRawHashes(root, []rawRef{{Name: "stdout", SHA256: hashBytes(data)}}); err == nil {
		t.Fatal("unreferenced raw file accepted")
	}
}
