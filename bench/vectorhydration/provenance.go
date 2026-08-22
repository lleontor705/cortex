package vectorhydration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const (
	BinaryIdentitySchemaVersion   = "binary-identity/v1"
	ProtocolIdentitySchemaVersion = "protocol-identity/v1"
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var toolVersionPattern = regexp.MustCompile(`^go[0-9]+\.[0-9]+\.[0-9]+$`)

const (
	ApprovedBuildIdentity = "go-test-c-trimpath-v1"
	QualificationPhase    = "qualification"
	HeldOutPhase          = "held-out"
)

// BinaryIdentity seals the exact executable and toolchain used by a campaign.
type BinaryIdentity struct {
	SchemaVersion    string `json:"schema_version"`
	BinarySHA256     string `json:"binary_sha256"`
	SourceCommit     string `json:"source_commit"`
	SourceTreeSHA256 string `json:"source_tree_sha256"`
	ToolSHA256       string `json:"tool_sha256"`
	ArgvSHA256       string `json:"argv_sha256"`
	ToolVersion      string `json:"tool_version"`
	BuildIdentity    string `json:"build_identity"`
}

func (b BinaryIdentity) Validate() error {
	if b.SchemaVersion != BinaryIdentitySchemaVersion {
		return fmt.Errorf("binary identity schema_version must be %q", BinaryIdentitySchemaVersion)
	}
	for name, value := range map[string]string{"binary_sha256": b.BinarySHA256, "source_tree_sha256": b.SourceTreeSHA256, "tool_sha256": b.ToolSHA256, "argv_sha256": b.ArgvSHA256} {
		if !digestPattern.MatchString(value) || strings.Trim(value, "0") == "" {
			return fmt.Errorf("%s must be a non-zero lowercase SHA-256 digest", name)
		}
	}
	if !commitPattern.MatchString(b.SourceCommit) || strings.Trim(b.SourceCommit, "0") == "" {
		return errorsField("source_commit")
	}
	if !toolVersionPattern.MatchString(b.ToolVersion) {
		return fmt.Errorf("tool_version must be a portable Go version (for example go1.26.5)")
	}
	if b.BuildIdentity != ApprovedBuildIdentity {
		return fmt.Errorf("build_identity must be %q", ApprovedBuildIdentity)
	}
	return nil
}

func errorsField(name string) error { return fmt.Errorf("%s is required and must be lowercase", name) }

// ProtocolIdentity seals the statistical protocol and all immutable inputs.
type ProtocolIdentity struct {
	SchemaVersion           string  `json:"schema_version"`
	Campaign                string  `json:"campaign"`
	Amendment               string  `json:"amendment"`
	Phase                   string  `json:"phase"`
	ManifestSHA256          string  `json:"manifest_sha256"`
	ScheduleSHA256          string  `json:"schedule_sha256"`
	LegacyMeasurementSHA256 string  `json:"legacy_measurement_sha256"`
	BatchMeasurementSHA256  string  `json:"batch_measurement_sha256"`
	Resamples               int     `json:"resamples"`
	Confidence              float64 `json:"confidence"`
	LCBThreshold            float64 `json:"lcb_threshold"`
	GuardThreshold          float64 `json:"guard_threshold"`
	EnvironmentSHA256       string  `json:"environment_sha256"`
}

func (p ProtocolIdentity) Validate() error {
	if p.SchemaVersion != ProtocolIdentitySchemaVersion {
		return fmt.Errorf("protocol identity schema_version must be %q", ProtocolIdentitySchemaVersion)
	}
	if p.Campaign != CampaignID || p.Amendment != AmendmentVersion || (p.Phase != QualificationPhase && p.Phase != HeldOutPhase) {
		return errorsField("campaign, amendment, and phase")
	}
	for name, value := range map[string]string{"manifest_sha256": p.ManifestSHA256, "schedule_sha256": p.ScheduleSHA256, "legacy_measurement_sha256": p.LegacyMeasurementSHA256, "batch_measurement_sha256": p.BatchMeasurementSHA256, "environment_sha256": p.EnvironmentSHA256} {
		if !digestPattern.MatchString(value) || strings.Trim(value, "0") == "" {
			return fmt.Errorf("%s must be a non-zero lowercase SHA-256 digest", name)
		}
	}
	if p.Resamples != 100000 || p.Confidence != .95 || p.LCBThreshold != 5.0 || p.GuardThreshold != 5.10 {
		return errorsField("approved statistical constants")
	}
	return nil
}

type PublicationBinding struct {
	BinaryIdentitySHA256   string `json:"binary_identity_sha256"`
	ProtocolIdentitySHA256 string `json:"protocol_identity_sha256"`
}

func (b PublicationBinding) Validate(binary BinaryIdentity, protocol ProtocolIdentity) error {
	if err := binary.Validate(); err != nil {
		return fmt.Errorf("binary identity: %w", err)
	}
	if err := protocol.Validate(); err != nil {
		return fmt.Errorf("protocol identity: %w", err)
	}
	bd, _ := binary.Digest()
	pd, _ := protocol.Digest()
	if !digestPattern.MatchString(b.BinaryIdentitySHA256) || strings.Trim(b.BinaryIdentitySHA256, "0") == "" {
		return errorsField("binary_identity_sha256")
	}
	if !digestPattern.MatchString(b.ProtocolIdentitySHA256) || strings.Trim(b.ProtocolIdentitySHA256, "0") == "" {
		return errorsField("protocol_identity_sha256")
	}
	if b.BinaryIdentitySHA256 != bd {
		return errorsField("binary identity digest binding")
	}
	if b.ProtocolIdentitySHA256 != pd {
		return errorsField("protocol identity digest binding")
	}
	return nil
}

func ValidatePublicationBinding(binding PublicationBinding, binary BinaryIdentity, protocol ProtocolIdentity) error {
	return binding.Validate(binary, protocol)
}

func canonicalIdentity(v interface{ Validate() error }) ([]byte, error) {
	if err := v.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
func digestIdentity(v interface{ Validate() error }) (string, error) {
	b, err := canonicalIdentity(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
func (b BinaryIdentity) CanonicalJSON() ([]byte, error)   { return canonicalIdentity(b) }
func (p ProtocolIdentity) CanonicalJSON() ([]byte, error) { return canonicalIdentity(p) }
func (b BinaryIdentity) Digest() (string, error)          { return digestIdentity(b) }
func (p ProtocolIdentity) Digest() (string, error)        { return digestIdentity(p) }
func (b BinaryIdentity) Hash() (string, error)            { return b.Digest() }
func (p ProtocolIdentity) Hash() (string, error)          { return p.Digest() }

func strictUnmarshal(data []byte, target interface{}, validate func() error) error {
	if err := rejectDuplicateJSONFields(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if err := ensureJSONEOF(dec); err != nil {
		return err
	}
	return validate()
}
func (b *BinaryIdentity) UnmarshalJSON(data []byte) error {
	type plain BinaryIdentity
	var x plain
	if err := strictUnmarshal(data, &x, func() error { return BinaryIdentity(x).Validate() }); err != nil {
		return err
	}
	*b = BinaryIdentity(x)
	return nil
}
func (p *ProtocolIdentity) UnmarshalJSON(data []byte) error {
	type plain ProtocolIdentity
	var x plain
	if err := strictUnmarshal(data, &x, func() error { return ProtocolIdentity(x).Validate() }); err != nil {
		return err
	}
	*p = ProtocolIdentity(x)
	return nil
}
