package postgres

import (
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

func FuzzPrincipalValidationNeverPanics(f *testing.F) {
	f.Add("subject", "00000000-0000-0000-0000-000000000001")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, subject, org string) {
		if len(subject) > 256 || len(org) > 256 {
			t.Skip()
		}
		_ = validatePrincipal(domain.Principal{Subject: subject, OrgID: org})
	})
}
