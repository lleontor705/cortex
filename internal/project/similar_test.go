package project

import "testing"

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "abcd", 1},
		{"kitten", "sitting", 3},
		{"saturday", "sunday", 3},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			got := levenshtein(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestFindSimilar_CaseInsensitive(t *testing.T) {
	matches := FindSimilar("Cortex", []string{"cortex", "other"}, 3)
	if len(matches) == 0 {
		t.Fatal("expected case-insensitive match")
	}
	if matches[0].Name != "cortex" || matches[0].MatchType != "case-insensitive" {
		t.Errorf("got %+v, want case-insensitive match for 'cortex'", matches[0])
	}
}

func TestFindSimilar_Substring(t *testing.T) {
	matches := FindSimilar("cortex", []string{"cortex-memory", "other"}, 3)
	if len(matches) == 0 {
		t.Fatal("expected substring match")
	}
	if matches[0].MatchType != "substring" {
		t.Errorf("got %+v, want substring match", matches[0])
	}
}

func TestFindSimilar_Levenshtein(t *testing.T) {
	matches := FindSimilar("cortex", []string{"cortax", "other"}, 3)
	if len(matches) == 0 {
		t.Fatal("expected levenshtein match")
	}
	if matches[0].MatchType != "levenshtein" || matches[0].Distance != 1 {
		t.Errorf("got %+v, want levenshtein match with distance 1", matches[0])
	}
}

func TestFindSimilar_ExcludesExact(t *testing.T) {
	matches := FindSimilar("cortex", []string{"cortex"}, 3)
	if len(matches) != 0 {
		t.Errorf("exact match should be excluded, got %+v", matches)
	}
}

func TestFindSimilar_ShortName(t *testing.T) {
	// Short names should have scaled maxDistance
	matches := FindSimilar("ab", []string{"xy", "abc"}, 3)
	// "ab" has effectiveMax = min(3, 2/2) = 1
	// "xy" has distance 2 > 1, should not match
	// "abc" has distance 1 <= 1, should match
	found := false
	for _, m := range matches {
		if m.Name == "xy" {
			t.Error("short name 'ab' should not match 'xy' with scaled distance")
		}
		if m.Name == "abc" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'abc' to match 'ab'")
	}
}

func TestFindSimilar_Empty(t *testing.T) {
	matches := FindSimilar("test", []string{}, 3)
	if len(matches) != 0 {
		t.Errorf("expected empty result for empty candidates")
	}
}

func TestFindSimilar_SubstringSkippedForShort(t *testing.T) {
	// Names shorter than 3 chars should not trigger substring matching
	matches := FindSimilar("go", []string{"golang-tools"}, 3)
	for _, m := range matches {
		if m.MatchType == "substring" {
			t.Error("substring match should be skipped for 2-char names")
		}
	}
}
