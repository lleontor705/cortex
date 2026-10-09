package config

import "testing"

func TestProviderRPMFromEnv(t *testing.T) {
	t.Run("unset defaults to unlimited", func(t *testing.T) {
		t.Setenv("CORTEX_PROVIDER_RPM", "")
		rpm, err := ProviderRPMFromEnv()
		if err != nil || rpm != 0 {
			t.Fatalf("ProviderRPMFromEnv() = %d, %v; want 0, nil", rpm, err)
		}
	})
	t.Run("positive budget parses", func(t *testing.T) {
		t.Setenv("CORTEX_PROVIDER_RPM", "60")
		rpm, err := ProviderRPMFromEnv()
		if err != nil || rpm != 60 {
			t.Fatalf("ProviderRPMFromEnv() = %d, %v; want 60, nil", rpm, err)
		}
	})
	t.Run("malformed fails closed", func(t *testing.T) {
		t.Setenv("CORTEX_PROVIDER_RPM", "sixty")
		if _, err := ProviderRPMFromEnv(); err == nil {
			t.Fatal("malformed CORTEX_PROVIDER_RPM must fail closed")
		}
	})
	t.Run("negative fails closed", func(t *testing.T) {
		t.Setenv("CORTEX_PROVIDER_RPM", "-1")
		if _, err := ProviderRPMFromEnv(); err == nil {
			t.Fatal("negative CORTEX_PROVIDER_RPM must fail closed")
		}
	})
}
