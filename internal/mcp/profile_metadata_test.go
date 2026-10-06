package mcp

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSupportedProfilesExcludeRetired(t *testing.T) {
	want := []string{"agent", "coder", "dev", "minimal"}
	got := SupportedProfiles()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SupportedProfiles() = %v, want %v", got, want)
	}
	for _, name := range got {
		if RetiredProfiles[name] {
			t.Errorf("SupportedProfiles() advertises retired profile %q", name)
		}
	}
}

func TestStatusToolAdvertisesOnlySupportedProfiles(t *testing.T) {
	stores := setupTestStores(t)
	result := callTool(t, handleGetStatus(stores), map[string]interface{}{})

	var payload struct {
		Profiles []string `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(resultText(result)), &payload); err != nil {
		t.Fatalf("parse status output: %v", err)
	}
	if !reflect.DeepEqual(payload.Profiles, SupportedProfiles()) {
		t.Errorf("status profiles = %v, want %v", payload.Profiles, SupportedProfiles())
	}
	for _, name := range payload.Profiles {
		if RetiredProfiles[name] {
			t.Errorf("status tool advertises retired profile %q", name)
		}
	}
}

func TestCoderAliasResolvesToDevProfile(t *testing.T) {
	if !reflect.DeepEqual(ResolveTools("coder"), ProfileDev) {
		t.Error(`ResolveTools("coder") must equal ProfileDev`)
	}
}
