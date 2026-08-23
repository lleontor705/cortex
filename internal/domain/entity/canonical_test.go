package entity

import (
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestNormalizeUnicodeEquivalentAndCollisionSafe(t *testing.T) {
	composed := Normalize(domain.EntityConcept, "Café")
	decomposed := Normalize(domain.EntityConcept, "Cafe\u0301")
	if composed != decomposed {
		t.Fatalf("canonicalization must normalize Unicode: %q != %q", composed, decomposed)
	}
	if Normalize(domain.EntityFile, "a-b") == Normalize(domain.EntityFile, "ab") {
		t.Fatal("canonicalization must preserve separators to avoid collisions")
	}
}
