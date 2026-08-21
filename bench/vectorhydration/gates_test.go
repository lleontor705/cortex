package vectorhydration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGateFixture(t *testing.T, registryBytes []byte, gates map[string][]byte) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "registry.json")
	if err := os.WriteFile(registryPath, registryBytes, 0600); err != nil {
		t.Fatal(err)
	}
	paths := make(map[string]string, len(gates))
	for name, data := range gates {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
	}
	return registryPath, paths
}

func loaderFixture(t *testing.T) ([]byte, map[string][]byte) {
	t.Helper()
	binary := strings.Repeat("b", 64)
	gates := map[string][]byte{}
	refs := map[string]string{}
	for _, name := range requiredGateNames {
		data, err := json.Marshal(gateArtifactJSON{SchemaVersion: gateSchemaVersion, CampaignID: CampaignID, AmendmentVersion: AmendmentVersion, ManifestDigest: strings.Repeat("a", 64), SourceCommit: strings.Repeat("1", 40), PhaseID: "phase", TestBinarySHA256: binary, GateName: name, Command: "gate", Result: "PASS"})
		if err != nil {
			t.Fatal(err)
		}
		gates[name] = data
		refs[name] = hashBytes(data)
	}
	registry, err := json.Marshal(TrustRegistry{SchemaVersion: trustSchemaVersion, CampaignID: CampaignID, AmendmentVersion: AmendmentVersion, PhaseID: "phase", SourceCommit: strings.Repeat("1", 40), TestBinarySHA256: binary, Gates: refs})
	if err != nil {
		t.Fatal(err)
	}
	return registry, gates
}

func TestLoadExternalGatesAuthenticatesBeforeParsing(t *testing.T) {
	registry, gates := loaderFixture(t)
	registryPath, paths := writeGateFixture(t, registry, gates)
	loaded, err := LoadExternalGates(registryPath, hashBytes(registry), paths)
	if err != nil || loaded.registrySHA256 != hashBytes(registry) {
		t.Fatalf("valid authenticated load failed: %v", err)
	}
	malformed := []byte(`{"schema_version":`)
	path, paths := writeGateFixture(t, malformed, gates)
	if _, err := LoadExternalGates(path, hashBytes(registry), paths); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("malformed registry with wrong digest was parsed or accepted: %v", err)
	}
}

func TestLoadExternalGatesRejectsZeroAndUnknownDigests(t *testing.T) {
	registry, gates := loaderFixture(t)
	path, paths := writeGateFixture(t, registry, gates)
	for _, digest := range []string{strings.Repeat("0", 64), strings.Repeat("A", 64), "short"} {
		if _, err := LoadExternalGates(path, digest, paths); err == nil {
			t.Fatalf("accepted invalid registry digest %q", digest)
		}
	}
	var trust TrustRegistry
	if err := json.Unmarshal(registry, &trust); err != nil {
		t.Fatal(err)
	}
	trust.Gates["extra"] = trust.Gates["semantic"]
	registryWithExtra, err := json.Marshal(trust)
	if err != nil {
		t.Fatal(err)
	}
	registryPath, paths := writeGateFixture(t, registryWithExtra, gates)
	if _, err := LoadExternalGates(registryPath, hashBytes(registryWithExtra), paths); err == nil || !strings.Contains(err.Error(), "exactly the required gates") {
		t.Fatalf("accepted extra registry gate: %v", err)
	}
	missingTrust := TrustRegistry{SchemaVersion: trustSchemaVersion, CampaignID: CampaignID, AmendmentVersion: AmendmentVersion, PhaseID: trust.PhaseID, SourceCommit: trust.SourceCommit, TestBinarySHA256: trust.TestBinarySHA256, Gates: map[string]string{"query": trust.Gates["query"], "allocation": trust.Gates["allocation"]}}
	registryMissing, err := json.Marshal(missingTrust)
	if err != nil {
		t.Fatal(err)
	}
	registryPath, paths = writeGateFixture(t, registryMissing, gates)
	if _, err := LoadExternalGates(registryPath, hashBytes(registryMissing), paths); err == nil || !strings.Contains(err.Error(), "exactly the required gates") {
		t.Fatalf("accepted missing registry gate: %v", err)
	}
}

func TestLoadExternalGatesRejectsAlteredGateBeforeSyntax(t *testing.T) {
	registry, gates := loaderFixture(t)
	gates["semantic"] = []byte(`{"schema_version":`)
	path, paths := writeGateFixture(t, registry, gates)
	if _, err := LoadExternalGates(path, hashBytes(registry), paths); err == nil || err.Error() != "gate artifact digest mismatch" {
		t.Fatalf("altered malformed gate did not fail on digest: %v", err)
	}
}

func TestLoadExternalGatesRejectsStrictJSONAfterDigest(t *testing.T) {
	for _, tc := range []struct {
		name, suffix, want string
	}{
		{"unknown field", `,"unknown":true}`, "json: unknown field"},
		{"duplicate field", `,"phase":"phase"}`, "duplicate manifest JSON field"},
		{"trailing value", `} {}`, "manifest JSON must contain exactly one value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, gates := loaderFixture(t)
			var artifact map[string]any
			if err := json.Unmarshal(gates["semantic"], &artifact); err != nil {
				t.Fatal(err)
			}
			base := strings.TrimSuffix(string(gates["semantic"]), "}")
			if tc.name == "duplicate field" {
				gates["semantic"] = []byte(base + tc.suffix + "}")
			} else {
				gates["semantic"] = []byte(base + tc.suffix)
			}
			var trust TrustRegistry
			if err := json.Unmarshal(registry, &trust); err != nil {
				t.Fatal(err)
			}
			trust.Gates["semantic"] = hashBytes(gates["semantic"])
			registry, err := json.Marshal(trust)
			if err != nil {
				t.Fatal(err)
			}
			path, paths := writeGateFixture(t, registry, gates)
			if _, err := LoadExternalGates(path, hashBytes(registry), paths); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("strict JSON mutation was not authenticated then rejected: %v", err)
			}
		})
	}
}

func TestLoadExternalGatesRejectsNonRegularBeforeParsing(t *testing.T) {
	registry, gates := loaderFixture(t)
	path, paths := writeGateFixture(t, registry, gates)
	paths["semantic"] = filepath.Dir(paths["semantic"])
	if _, err := LoadExternalGates(path, hashBytes(registry), paths); err == nil || err.Error() != "gate file must be regular" {
		t.Fatalf("non-regular gate was parsed or accepted: %v", err)
	}

	symlink := filepath.Join(t.TempDir(), "semantic.json")
	if err := os.Symlink(paths["query"], symlink); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	paths["semantic"] = symlink
	if _, err := LoadExternalGates(path, hashBytes(registry), paths); err == nil || err.Error() != "publication path cannot contain symlinks" {
		t.Fatalf("symlink gate was parsed or accepted: %v", err)
	}
}
