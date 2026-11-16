package config

import "testing"

func TestResolveRemoteTokenPrecedence(t *testing.T) {
	t.Setenv("CORTEX_TEST_REMOTE_TOKEN", "  from-env  ")
	t.Setenv("CORTEX_TEST_EMPTY_TOKEN", "")

	cases := []struct {
		name, tokenEnv, fallback, want string
	}{
		{"env wins over fallback", "CORTEX_TEST_REMOTE_TOKEN", "stored", "from-env"},
		{"empty env uses fallback", "CORTEX_TEST_EMPTY_TOKEN", " stored ", "stored"},
		{"literal ctx token", "ctx_abc", "stored", "ctx_abc"},
		{"literal jwt", "eyJhbGciOi", "", "eyJhbGciOi"},
		{"literal bootstrap token", "cortex_admin_xyz", "", "cortex_admin_xyz"},
		{"blank token env uses fallback", "  ", "stored", "stored"},
		{"nothing configured", "CORTEX_TEST_EMPTY_TOKEN", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveRemoteToken(tc.tokenEnv, tc.fallback); got != tc.want {
				t.Fatalf("ResolveRemoteToken(%q, %q) = %q, want %q", tc.tokenEnv, tc.fallback, got, tc.want)
			}
		})
	}
}
