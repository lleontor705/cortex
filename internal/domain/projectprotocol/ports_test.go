package projectprotocol_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/projectprotocol"
)

// stubStore proves the domain port is implementable without persistence
// coupling: every method is a compile-checked signature match.
type stubStore struct{}

func (stubStore) SaveArtifact(ctx context.Context, in projectprotocol.SaveArtifactInput) (projectprotocol.Artifact, error) {
	return projectprotocol.Artifact{}, nil
}

func (stubStore) SaveRevision(ctx context.Context, artifactID string, in projectprotocol.RevisionInput, pre projectprotocol.Preconditions) (projectprotocol.Revision, error) {
	return projectprotocol.Revision{}, nil
}

func (stubStore) GetArtifact(ctx context.Context, artifactID string) (projectprotocol.Artifact, error) {
	return projectprotocol.Artifact{}, nil
}

func (stubStore) ListArtifacts(ctx context.Context, filter projectprotocol.ArtifactFilter, page projectprotocol.PageRequest) (projectprotocol.ArtifactPage, error) {
	return projectprotocol.ArtifactPage{}, nil
}

func (stubStore) ListRevisions(ctx context.Context, artifactID string, page projectprotocol.PageRequest) (projectprotocol.RevisionPage, error) {
	return projectprotocol.RevisionPage{}, nil
}

func (stubStore) ListEvents(ctx context.Context, artifactID string, page projectprotocol.PageRequest) (projectprotocol.ArtifactEventPage, error) {
	return projectprotocol.ArtifactEventPage{}, nil
}

func (stubStore) Activate(ctx context.Context, in projectprotocol.ActivateInput) (projectprotocol.Activation, error) {
	return projectprotocol.Activation{}, nil
}

func (stubStore) Rollback(ctx context.Context, in projectprotocol.RollbackInput) (projectprotocol.Activation, error) {
	return projectprotocol.Activation{}, nil
}

func (stubStore) SoftDelete(ctx context.Context, in projectprotocol.SoftDeleteInput) (projectprotocol.Artifact, error) {
	return projectprotocol.Artifact{}, nil
}

func (stubStore) EffectiveProtocol(ctx context.Context, project string) (projectprotocol.Protocol, error) {
	return projectprotocol.Protocol{}, nil
}

// TestProjectProtocolStoreIsImplementable pins the port contract.
func TestProjectProtocolStoreIsImplementable(t *testing.T) {
	var _ domain.ProjectProtocolStore = stubStore{}
}

// TestProjectProtocolStoreDefinesNoDestructivePort enforces the v1 retention
// contract: the port surface exposes no hard-delete, purge, truncate or
// compaction method (REQ-RET-001).
func TestProjectProtocolStoreDefinesNoDestructivePort(t *testing.T) {
	portType := reflect.TypeOf((*domain.ProjectProtocolStore)(nil)).Elem()
	banned := []string{"Purge", "HardDelete", "Truncate", "Compact", "Compaction", "Drop", "DeleteArtifact", "DeleteRevision"}
	for i := 0; i < portType.NumMethod(); i++ {
		name := portType.Method(i).Name
		for _, b := range banned {
			if name == b {
				t.Fatalf("port exposes destructive method %s", name)
			}
		}
	}
	want := map[string]bool{
		"SaveArtifact": false, "SaveRevision": false, "GetArtifact": false,
		"ListArtifacts": false, "ListRevisions": false, "ListEvents": false,
		"Activate": false, "Rollback": false, "SoftDelete": false,
		"EffectiveProtocol": false,
	}
	for i := 0; i < portType.NumMethod(); i++ {
		if _, ok := want[portType.Method(i).Name]; ok {
			want[portType.Method(i).Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("port is missing required method %s", name)
		}
	}
}

// TestLimitsAreTheSingleSource verifies that the exported limit constants
// used by transport layers match the approved spec values.
func TestLimitsAreTheSingleSource(t *testing.T) {
	if projectprotocol.MaxArtifactContentBytes != 1048576 ||
		projectprotocol.MaxArtifactMetadataBytes != 65536 ||
		projectprotocol.MaxEffectiveArtifacts != 2000 ||
		projectprotocol.MaxProtocolBundleBytes != 4194304 {
		t.Fatal("approved limits drifted from spec")
	}
	if projectprotocol.OrdinaryRequestTransportBytes != 1048576 ||
		projectprotocol.LargeMutationTransportBytes != 8388608 ||
		projectprotocol.MCPAbsoluteRequestBytes != 8388608 ||
		projectprotocol.MCPProtocolResponseTargetBytes != 5242880 {
		t.Fatal("transport caps drifted from design constants")
	}
}
