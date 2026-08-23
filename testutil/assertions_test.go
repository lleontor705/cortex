package testutil

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestAssertObservationEqual(t *testing.T) {
	f := NewFixtures()

	t.Run("equal", func(t *testing.T) {
		obs1 := f.Observation()
		obs2 := f.Observation()

		// Should not fail
		AssertObservationEqual(t, obs1, obs2)
	})

	t.Run("nil_both", func(t *testing.T) {
		AssertObservationEqual(t, nil, nil)
	})

	t.Run("different_id", func(t *testing.T) {
		innerT := &testing.T{}
		obs1 := f.Observation()
		obs2 := f.Observation(func(o *domain.Observation) { o.ID = 999 })

		AssertObservationEqual(innerT, obs1, obs2)

		if !innerT.Failed() {
			t.Error("expected test to fail for different IDs")
		}
	})

	t.Run("different_title", func(t *testing.T) {
		innerT := &testing.T{}
		obs1 := f.Observation()
		obs2 := f.Observation(func(o *domain.Observation) { o.Title = "Different" })

		AssertObservationEqual(innerT, obs1, obs2)

		if !innerT.Failed() {
			t.Error("expected test to fail for different titles")
		}
	})
}

func TestAssertSessionEqual(t *testing.T) {
	f := NewFixtures()

	t.Run("equal", func(t *testing.T) {
		sess1 := f.Session()
		sess2 := f.Session()

		AssertSessionEqual(t, sess1, sess2)
	})

	t.Run("nil_ended", func(t *testing.T) {
		innerT := &testing.T{}
		sess1 := f.Session()
		sess2 := f.SessionEnded()

		AssertSessionEqual(innerT, sess1, sess2)

		if !innerT.Failed() {
			t.Error("expected test to fail when one session has nil EndedAt")
		}
	})
}

func TestAssertEdgeEqual(t *testing.T) {
	f := NewFixtures()

	t.Run("equal", func(t *testing.T) {
		edge1 := f.Edge(1, 2)
		edge2 := f.Edge(1, 2)

		AssertEdgeEqual(t, edge1, edge2)
	})

	t.Run("different_weight", func(t *testing.T) {
		innerT := &testing.T{}
		edge1 := f.Edge(1, 2)
		edge2 := f.Edge(1, 2, func(e *domain.Edge) { e.Weight = 0.5 })

		AssertEdgeEqual(innerT, edge1, edge2)

		if !innerT.Failed() {
			t.Error("expected test to fail for different weights")
		}
	})
}

func TestAssertPromptEqual(t *testing.T) {
	f := NewFixtures()

	t.Run("equal", func(t *testing.T) {
		p1 := f.Prompt()
		p2 := f.Prompt()

		AssertPromptEqual(t, p1, p2)
	})

	t.Run("different_content", func(t *testing.T) {
		innerT := &testing.T{}
		p1 := f.Prompt()
		p2 := f.Prompt(func(p *domain.Prompt) { p.Content = "Different" })

		AssertPromptEqual(innerT, p1, p2)

		if !innerT.Failed() {
			t.Error("expected test to fail for different content")
		}
	})
}

func TestAssertErrorIs(t *testing.T) {
	t.Run("matching", func(t *testing.T) {
		err := fmt.Errorf("wrapped: %w", domain.ErrNotFound)
		AssertErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("not_matching", func(t *testing.T) {
		innerT := &testing.T{}
		err := errors.New("some error")

		AssertErrorIs(innerT, err, domain.ErrNotFound)

		if !innerT.Failed() {
			t.Error("expected test to fail for non-matching error")
		}
	})
}

func TestAssertErrorAs(t *testing.T) {
	t.Run("matching", func(t *testing.T) {
		err := &domain.NotFoundError{Type: "observation", ID: 123}
		var target *domain.NotFoundError
		AssertErrorAs(t, err, &target)
	})

	t.Run("not_matching", func(t *testing.T) {
		innerT := &testing.T{}
		err := errors.New("some error")
		var target *domain.NotFoundError

		AssertErrorAs(innerT, err, &target)

		if !innerT.Failed() {
			t.Error("expected test to fail for non-matching error type")
		}
	})
}

func TestAssertNoError(t *testing.T) {
	t.Run("no_error", func(t *testing.T) {
		AssertNoError(t, nil)
	})

	t.Run("with_error", func(t *testing.T) {
		innerT := &testing.T{}
		AssertNoError(innerT, errors.New("test error"))

		if !innerT.Failed() {
			t.Error("expected test to fail with error")
		}
	})
}

func TestRequireNoError(t *testing.T) {
	t.Run("no_error", func(t *testing.T) {
		RequireNoError(t, nil)
	})

	// Note: Testing the failure case of RequireNoError is not possible
	// with inner tests because it uses Fatalf which calls FailNow().
	// The behavior is verified by manual testing or integration tests.
}

func TestAssertError(t *testing.T) {
	t.Run("with_error", func(t *testing.T) {
		AssertError(t, errors.New("test error"))
	})

	t.Run("no_error", func(t *testing.T) {
		innerT := &testing.T{}
		AssertError(innerT, nil)

		if !innerT.Failed() {
			t.Error("expected test to fail with nil error")
		}
	})
}

func TestAssertWithinDuration(t *testing.T) {
	now := time.Now()

	t.Run("within_range", func(t *testing.T) {
		AssertWithinDuration(t, now, now.Add(500*time.Millisecond), time.Second)
	})

	t.Run("outside_range", func(t *testing.T) {
		innerT := &testing.T{}
		AssertWithinDuration(innerT, now, now.Add(2*time.Second), time.Second)

		if !innerT.Failed() {
			t.Error("expected test to fail for times outside range")
		}
	})
}

func TestAssertEqual(t *testing.T) {
	t.Run("equal", func(t *testing.T) {
		AssertEqual(t, "test", "test")
	})

	t.Run("not_equal", func(t *testing.T) {
		innerT := &testing.T{}
		AssertEqual(innerT, "test", "other")

		if !innerT.Failed() {
			t.Error("expected test to fail for non-equal values")
		}
	})
}

func TestAssertNotEqual(t *testing.T) {
	t.Run("not_equal", func(t *testing.T) {
		AssertNotEqual(t, "test", "other")
	})

	t.Run("equal", func(t *testing.T) {
		innerT := &testing.T{}
		AssertNotEqual(innerT, "test", "test")

		if !innerT.Failed() {
			t.Error("expected test to fail for equal values")
		}
	})
}

func TestAssertTrueFalse(t *testing.T) {
	t.Run("true_success", func(t *testing.T) {
		AssertTrue(t, true)
	})

	t.Run("true_failure", func(t *testing.T) {
		innerT := &testing.T{}
		AssertTrue(innerT, false)

		if !innerT.Failed() {
			t.Error("expected test to fail for false condition")
		}
	})

	t.Run("false_success", func(t *testing.T) {
		AssertFalse(t, false)
	})

	t.Run("false_failure", func(t *testing.T) {
		innerT := &testing.T{}
		AssertFalse(innerT, true)

		if !innerT.Failed() {
			t.Error("expected test to fail for true condition")
		}
	})
}

func TestAssertNilNotNil(t *testing.T) {
	t.Run("nil_success", func(t *testing.T) {
		AssertNil(t, nil)
	})

	t.Run("nil_failure", func(t *testing.T) {
		innerT := &testing.T{}
		AssertNil(innerT, "not nil")

		if !innerT.Failed() {
			t.Error("expected test to fail for non-nil value")
		}
	})

	t.Run("notnil_success", func(t *testing.T) {
		AssertNotNil(t, "not nil")
	})

	t.Run("notnil_failure", func(t *testing.T) {
		innerT := &testing.T{}
		AssertNotNil(innerT, nil)

		if !innerT.Failed() {
			t.Error("expected test to fail for nil value")
		}
	})
}

func TestAssertLen(t *testing.T) {
	t.Run("slice", func(t *testing.T) {
		AssertLen(t, []int{1, 2, 3}, 3)
	})

	t.Run("string", func(t *testing.T) {
		AssertLen(t, "hello", 5)
	})

	t.Run("map", func(t *testing.T) {
		AssertLen(t, map[string]int{"a": 1}, 1)
	})

	t.Run("wrong_length", func(t *testing.T) {
		innerT := &testing.T{}
		AssertLen(innerT, []int{1, 2, 3}, 5)

		if !innerT.Failed() {
			t.Error("expected test to fail for wrong length")
		}
	})
}

func TestAssertContains(t *testing.T) {
	t.Run("contains", func(t *testing.T) {
		AssertContains(t, []string{"a", "b", "c"}, "b")
	})

	t.Run("not_contains", func(t *testing.T) {
		innerT := &testing.T{}
		AssertContains(innerT, []string{"a", "b", "c"}, "d")

		if !innerT.Failed() {
			t.Error("expected test to fail for missing element")
		}
	})
}

func TestAssertNotContains(t *testing.T) {
	t.Run("not_contains", func(t *testing.T) {
		AssertNotContains(t, []string{"a", "b", "c"}, "d")
	})

	t.Run("contains", func(t *testing.T) {
		innerT := &testing.T{}
		AssertNotContains(innerT, []string{"a", "b", "c"}, "b")

		if !innerT.Failed() {
			t.Error("expected test to fail for present element")
		}
	})
}

func TestAssertPanics(t *testing.T) {
	t.Run("panics", func(t *testing.T) {
		AssertPanics(t, func() {
			panic("test panic")
		})
	})

	t.Run("no_panic", func(t *testing.T) {
		innerT := &testing.T{}
		AssertPanics(innerT, func() {})

		if !innerT.Failed() {
			t.Error("expected test to fail for non-panicking function")
		}
	})
}

func TestPanicRecovered(t *testing.T) {
	// Note: Testing the panic recovery with inner tests is complex because
	// the t.Error call in the defer happens after fn() returns.
	// The success case is tested manually.

	t.Run("no_panic", func(t *testing.T) {
		innerT := &testing.T{}
		recovered := PanicRecovered(innerT, func() {})

		if !innerT.Failed() {
			t.Error("expected test to fail for non-panicking function")
		}
		if recovered != nil {
			t.Errorf("expected nil recovery, got %v", recovered)
		}
	})
}

func TestAssertNotFoundError(t *testing.T) {
	t.Run("matching", func(t *testing.T) {
		err := &domain.NotFoundError{Type: "observation", ID: int64(123)}
		AssertNotFoundError(t, err, "observation", int64(123))
	})

	t.Run("wrong_type", func(t *testing.T) {
		innerT := &testing.T{}
		err := &domain.NotFoundError{Type: "observation", ID: int64(123)}
		AssertNotFoundError(innerT, err, "session", int64(123))

		if !innerT.Failed() {
			t.Error("expected test to fail for wrong type")
		}
	})
}

func TestAssertValidationError(t *testing.T) {
	t.Run("matching", func(t *testing.T) {
		err := &domain.ValidationError{Field: "title", Message: "cannot be empty"}
		AssertValidationError(t, err, "title")
	})

	t.Run("wrong_field", func(t *testing.T) {
		innerT := &testing.T{}
		err := &domain.ValidationError{Field: "title", Message: "cannot be empty"}
		AssertValidationError(innerT, err, "content")

		if !innerT.Failed() {
			t.Error("expected test to fail for wrong field")
		}
	})
}

func TestAssertConflictError(t *testing.T) {
	t.Run("conflict_error", func(t *testing.T) {
		err := &domain.ConflictError{Entity: "session", Reason: "already ended"}
		AssertConflictError(t, err)
	})

	t.Run("wrong_error_type", func(t *testing.T) {
		innerT := &testing.T{}
		err := errors.New("some error")
		AssertConflictError(innerT, err)

		if !innerT.Failed() {
			t.Error("expected test to fail for wrong error type")
		}
	})
}

func TestAssertListEqualHelpers(t *testing.T) {
	f := NewFixtures()

	t.Run("observation_list", func(t *testing.T) {
		list1 := f.ObservationList(3)
		list2 := f.ObservationList(3)
		AssertObservationListEqual(t, list1, list2)
	})

	t.Run("observation_list_different_lengths", func(t *testing.T) {
		innerT := &testing.T{}
		list1 := f.ObservationList(3)
		list2 := f.ObservationList(2)
		AssertObservationListEqual(innerT, list1, list2)

		if !innerT.Failed() {
			t.Error("expected test to fail for different lengths")
		}
	})

	t.Run("edge_list", func(t *testing.T) {
		list1 := f.EdgeList(3)
		list2 := f.EdgeList(3)
		AssertEdgeListEqual(t, list1, list2)
	})

	t.Run("prompt_list", func(t *testing.T) {
		p1 := []*domain.Prompt{f.Prompt(), f.Prompt(func(p *domain.Prompt) { p.ID = 2 })}
		p2 := []*domain.Prompt{f.Prompt(), f.Prompt(func(p *domain.Prompt) { p.ID = 2 })}
		AssertPromptListEqual(t, p1, p2)
	})
}

func TestAssertIDs(t *testing.T) {
	f := NewFixtures()

	t.Run("observation_ids", func(t *testing.T) {
		obs := f.ObservationList(3)
		AssertObservationIDs(t, obs, 1, 2, 3)
	})

	t.Run("observation_ids_wrong", func(t *testing.T) {
		innerT := &testing.T{}
		obs := f.ObservationList(3)
		AssertObservationIDs(innerT, obs, 1, 2, 4) // Last ID is wrong

		if !innerT.Failed() {
			t.Error("expected test to fail for wrong IDs")
		}
	})

	t.Run("session_ids", func(t *testing.T) {
		sessions := []*domain.Session{
			f.Session(func(s *domain.Session) { s.ID = "a" }),
			f.Session(func(s *domain.Session) { s.ID = "b" }),
		}
		AssertSessionIDs(t, sessions, "a", "b")
	})

	t.Run("observation_types", func(t *testing.T) {
		obs := []*domain.Observation{
			f.ObservationDecision(),
			f.ObservationBugfix(),
		}
		AssertObservationTypes(t, obs, domain.TypeDecision, domain.TypeBugfix)
	})
}

func TestAssertImportanceScoreEqual(t *testing.T) {
	f := NewFixtures()

	t.Run("equal", func(t *testing.T) {
		s1 := f.ImportanceScore(1)
		s2 := f.ImportanceScore(1)
		AssertImportanceScoreEqual(t, s1, s2)
	})

	t.Run("nil_both", func(t *testing.T) {
		AssertImportanceScoreEqual(t, nil, nil)
	})

	t.Run("different_observation_id", func(t *testing.T) {
		innerT := &testing.T{}
		s1 := f.ImportanceScore(1)
		s2 := f.ImportanceScore(2)
		AssertImportanceScoreEqual(innerT, s1, s2)

		if !innerT.Failed() {
			t.Error("expected test to fail for different observation IDs")
		}
	})
}
