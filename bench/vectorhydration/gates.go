package vectorhydration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// ExternalGates contains only preregistered file digests and structured gate
// files. Gate outcomes are never accepted from caller-provided booleans.
type ExternalGates struct {
	artifacts      map[string]GateArtifact
	trustedSHA256  map[string]string
	registry       TrustRegistry
	registrySHA256 string
}

type GateArtifact struct {
	Content []byte `json:"-"`
	SHA256  string `json:"-"`
}

type gateArtifactJSON struct {
	SchemaVersion    string `json:"schema_version"`
	CampaignID       string `json:"campaign_id"`
	AmendmentVersion string `json:"amendment_version"`
	ManifestDigest   string `json:"manifest_digest"`
	SourceCommit     string `json:"source_commit"`
	PhaseID          string `json:"phase"`
	TestBinarySHA256 string `json:"test_binary_sha256"`
	GateName         string `json:"gate_name"`
	Command          string `json:"command"`
	Exit             int    `json:"exit"`
	Result           string `json:"result"`
}

type TrustRegistry struct {
	SchemaVersion    string            `json:"schema_version"`
	CampaignID       string            `json:"campaign_id"`
	AmendmentVersion string            `json:"amendment_version"`
	PhaseID          string            `json:"phase"`
	SourceCommit     string            `json:"source_commit"`
	TestBinarySHA256 string            `json:"test_binary_sha256"`
	Gates            map[string]string `json:"gates"`
}

const gateSchemaVersion = "vec-bench-gate/v2"
const trustSchemaVersion = "vec-bench-trust/v1"

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validateSHA(name, value string) error {
	if !sha256Pattern.MatchString(value) || value == strings.Repeat("0", 64) {
		return fmt.Errorf("%s must be a lowercase SHA-256 digest", name)
	}
	return nil
}

// LoadExternalGates loads only externally supplied, regular files. It does not
// create or approve either trust registries or gate artifacts.
func LoadExternalGates(registryPath, expectedRegistrySHA256 string, artifactPaths map[string]string) (ExternalGates, error) {
	var out ExternalGates
	if err := validateSHA("registry_sha256", expectedRegistrySHA256); err != nil {
		return out, err
	}
	registryBytes, err := readStrictGateFile(registryPath)
	if err != nil {
		return out, err
	}
	registrySHA := hashBytes(registryBytes)
	if registrySHA != expectedRegistrySHA256 {
		return out, errors.New("trust registry digest mismatch")
	}
	if err := rejectDuplicateJSONFields(registryBytes); err != nil {
		return out, err
	}
	dec := json.NewDecoder(bytes.NewReader(registryBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out.registry); err != nil {
		return out, err
	}
	if err := ensureJSONEOF(dec); err != nil {
		return out, err
	}
	if err := validateRegistry(out.registry); err != nil {
		return out, err
	}
	out.registrySHA256 = registrySHA
	if len(artifactPaths) != len(requiredGateNames) {
		return out, errors.New("gate artifacts must contain exactly the required names")
	}
	out.artifacts, out.trustedSHA256 = map[string]GateArtifact{}, map[string]string{}
	for _, name := range requiredGateNames {
		path, ok := artifactPaths[name]
		if !ok {
			return out, fmt.Errorf("missing gate artifact %s", name)
		}
		b, e := readStrictGateFile(path)
		if e != nil {
			return out, e
		}
		gateSHA := hashBytes(b)
		if gateSHA != out.registry.Gates[name] {
			return out, errors.New("gate artifact digest mismatch")
		}
		if e = validateGateSyntax(b, name); e != nil {
			return out, e
		}
		out.artifacts[name] = GateArtifact{Content: b, SHA256: gateSHA}
		out.trustedSHA256[name] = out.registry.Gates[name]
	}
	return out, nil
}

func validateGateSyntax(data []byte, expectedName string) error {
	if err := rejectDuplicateJSONFields(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var g gateArtifactJSON
	if err := dec.Decode(&g); err != nil {
		return err
	}
	if err := ensureJSONEOF(dec); err != nil {
		return err
	}
	if g.SchemaVersion != gateSchemaVersion || g.GateName != expectedName || g.Command == "" || g.Exit != 0 || g.Result != "PASS" {
		return errors.New("invalid gate artifact")
	}
	if g.ManifestDigest == "" || !commitPattern.MatchString(g.SourceCommit) {
		return errors.New("invalid gate identity")
	}
	return validateSHA("test_binary_sha256", g.TestBinarySHA256)
}

func readStrictGateFile(path string) ([]byte, error) {
	if err := validatePathComponents(path, false); err != nil {
		return nil, err
	}
	i, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !infoIsRegular(i) {
		return nil, errors.New("gate file must be regular")
	}
	return os.ReadFile(path)
}

func infoIsRegular(i os.FileInfo) bool {
	return i != nil && i.Mode().IsRegular()
}

var requiredGateNames = [...]string{"semantic", "query", "allocation"}

func validateRegistry(r TrustRegistry) error {
	if r.SchemaVersion != trustSchemaVersion || r.CampaignID != CampaignID || r.AmendmentVersion != AmendmentVersion || r.PhaseID == "" || r.SourceCommit == "" {
		return errors.New("invalid trust registry identity")
	}
	if !commitPattern.MatchString(r.SourceCommit) || r.SourceCommit == "0000000000000000000000000000000000000000" {
		return errors.New("trust registry source_commit must be a non-zero lowercase Git commit")
	}
	if err := validateSHA("test_binary_sha256", r.TestBinarySHA256); err != nil {
		return err
	}
	if len(r.Gates) != len(requiredGateNames) {
		return errors.New("trust registry must contain exactly the required gates")
	}
	for _, name := range requiredGateNames {
		if err := validateSHA("gate "+name, r.Gates[name]); err != nil {
			return err
		}
	}
	for name := range r.Gates {
		if !containsGate(name) {
			return fmt.Errorf("unknown gate %s", name)
		}
	}
	return nil
}
func containsGate(name string) bool {
	for _, n := range requiredGateNames {
		if n == name {
			return true
		}
	}
	return false
}

func gateResults(in AnalysisInput) (bool, []string) {
	ok := true
	reasons := []string{}
	if err := validateRegistry(in.Gates.registry); err != nil || in.Gates.registrySHA256 == "" || in.Gates.registry.PhaseID != in.Manifest.Phase.ID || in.Gates.registry.SourceCommit != in.Manifest.SourceCommit {
		return false, []string{"invalid or missing trust registry"}
	}
	for _, name := range requiredGateNames {
		a, exists := in.Gates.artifacts[name]
		trusted, trustedExists := in.Gates.trustedSHA256[name]
		var g gateArtifactJSON
		valid := exists && trustedExists && trusted == in.Gates.registry.Gates[name] && a.SHA256 != "" && trusted == a.SHA256 && hashBytes(a.Content) == a.SHA256
		if valid {
			if rejectDuplicateJSONFields(a.Content) != nil {
				valid = false
			}
			dec := json.NewDecoder(bytes.NewReader(a.Content))
			dec.DisallowUnknownFields()
			valid = valid && dec.Decode(&g) == nil && dec.Decode(&struct{}{}) == io.EOF && g.SchemaVersion == gateSchemaVersion && g.CampaignID == CampaignID && g.AmendmentVersion == AmendmentVersion && g.ManifestDigest == in.Manifest.ManifestDigest() && g.SourceCommit == in.Manifest.SourceCommit && g.PhaseID == in.Manifest.Phase.ID && g.GateName == name && g.TestBinarySHA256 == in.Gates.registry.TestBinarySHA256 && g.Command != "" && g.Exit == 0 && g.Result == "PASS"
		}
		if !valid {
			ok = false
			reasons = append(reasons, "invalid or missing gate artifact: "+name)
		}
	}
	return ok, reasons
}

func validatePreparedBinary(in AnalysisInput) error {
	b := in.Binary
	if b.SourceCommit != in.Manifest.SourceCommit || b.SourceCommit != in.Gates.registry.SourceCommit {
		return fmt.Errorf("prepared binary source commit is not bound")
	}
	if err := validateSHA("prepared binary sha256", b.SHA256); err != nil {
		return err
	}
	if b.SHA256 != in.Gates.registry.TestBinarySHA256 {
		return fmt.Errorf("prepared binary digest does not match trust registry")
	}
	for _, name := range requiredGateNames {
		var g gateArtifactJSON
		if err := json.Unmarshal(in.Gates.artifacts[name].Content, &g); err != nil || g.TestBinarySHA256 != b.SHA256 {
			return fmt.Errorf("gate %s is not bound to prepared binary", name)
		}
	}
	actual, err := hashRegularFile(b.BinaryPath)
	if err != nil || actual != b.SHA256 {
		return fmt.Errorf("prepared binary does not match digest")
	}
	return nil
}

func (m Manifest) ManifestDigest() string {
	b, err := m.CanonicalJSON()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
