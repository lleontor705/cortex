//go:build postgres_integration

package postgres

import "testing"

func TestAppRoleGrantSQLQuotesLoginIdentifier(t *testing.T) {
	for _, role := range []string{"cortex_test", `login"with-quote`} {
		t.Run(role, func(t *testing.T) {
			got := appRoleGrantSQL(role)
			if got == "GRANT cortex_app TO "+role {
				t.Fatalf("role identifier was not quoted: %s", got)
			}
			if got == "" {
				t.Fatal("grant statement is empty")
			}
		})
	}
}
