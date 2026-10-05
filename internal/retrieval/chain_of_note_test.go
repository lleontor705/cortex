package retrieval

// chain_of_note_test.go covers REQ-LME-001 acceptance criterion 4: the
// Chain-of-Note reading stage produces per-passage notes and fuses the
// note-ranked list into final ranking without breaking the REQ-RET-002
// fusion contract (position-only RRF inputs, decay as final multiplier).

import (
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func noteCandidate(id int64, title, topicKey, content string) *domain.SearchResult {
	return &domain.SearchResult{Observation: domain.Observation{
		ID:       id,
		Title:    title,
		TopicKey: topicKey,
		Content:  content,
	}}
}

// TestChainOfNoteStage_NotesAndDeterministicOrder pins the reading stage:
// every non-empty candidate gets a non-empty note taken from its own text,
// AnswerBearing tracks the relevance threshold, order is relevance DESC then
// ID ASC, and an empty query returns (nil, candidates) untouched.
func TestChainOfNoteStage_NotesAndDeterministicOrder(t *testing.T) {
	candidates := []*domain.SearchResult{
		noteCandidate(1, "Dragon project update", "proj/dragon",
			"The dragon project uses Go. The build pipeline failed yesterday."),
		noteCandidate(2, "Weather note", "weather/storm",
			"A thunderstorm flooded the basement. Nothing relevant about trains here."),
	}
	query := "dragon project build"

	notes, ranked := ChainOfNoteStage(query, candidates)
	if len(notes) != 2 || len(ranked) != 2 {
		t.Fatalf("expected 2 notes and 2 ranked candidates, got %d/%d", len(notes), len(ranked))
	}
	byID := map[int64]PassageNote{}
	for _, n := range notes {
		if strings.TrimSpace(n.Note) == "" {
			t.Errorf("note for obs %d must not be empty", n.ObservationID)
		}
		byID[n.ObservationID] = n
	}
	// The note must be a sentence of the candidate's own passage.
	passage := candidates[0].Title + " " + candidates[0].TopicKey + " " + candidates[0].Content
	if !strings.Contains(passage, byID[1].Note) {
		t.Errorf("note %q must come from its passage", byID[1].Note)
	}
	if !byID[1].AnswerBearing || byID[2].AnswerBearing {
		t.Errorf("AnswerBearing: obs1=%v obs2=%v, want true/false", byID[1].AnswerBearing, byID[2].AnswerBearing)
	}
	if ranked[0].ID != 1 {
		t.Errorf("highest-relevance candidate must rank first, got ID %d", ranked[0].ID)
	}
	for i := 1; i < len(ranked); i++ {
		prev, cur := byID[ranked[i-1].ID].Relevance, byID[ranked[i].ID].Relevance
		if prev < cur || (prev == cur && ranked[i-1].ID > ranked[i].ID) {
			t.Errorf("order violated at %d: relevance [%v id %d] before [%v id %d]",
				i, prev, ranked[i-1].ID, cur, ranked[i].ID)
		}
	}

	// Determinism: a second run over the same inputs yields the same order.
	_, rankedAgain := ChainOfNoteStage(query, candidates)
	for i := range ranked {
		if ranked[i].ID != rankedAgain[i].ID {
			t.Fatalf("ordering must be deterministic, run1=%v run2=%v", noteIDs(ranked), noteIDs(rankedAgain))
		}
	}

	emptyNotes, untouched := ChainOfNoteStage("", candidates)
	if emptyNotes != nil {
		t.Errorf("empty query must produce no notes, got %d", len(emptyNotes))
	}
	if len(untouched) != 2 {
		t.Errorf("empty query must return candidates untouched, got %d", len(untouched))
	}
}

// TestFuseResultsWithNotes_OverlapAccumulatesRRF pins that the note-ranked
// list is a third accumulate input: an ID in both FTS (pos 1) and notes
// (pos 1) scores 1/61 + 1/61 and beats a vector-only ID at 1/61.
// Overwrite or dropped-notes mutations flip the head of the list.
func TestFuseResultsWithNotes_OverlapAccumulatesRRF(t *testing.T) {
	fts := []*domain.SearchResult{noteCandidate(5, "overlap", "", "overlap content")}
	vec := []*domain.VectorSearchResult{
		{Observation: domain.Observation{ID: 6}, Similarity: 0.7},
	}
	notes := []*domain.SearchResult{noteCandidate(5, "overlap", "", "overlap content")}

	results := FuseResultsWithNotes(fts, vec, notes, FuseOptions{Limit: 10})
	if len(results) != 2 {
		t.Fatalf("expected 2 fused results, got %d", len(results))
	}
	if results[0].ID != 5 {
		t.Errorf("accumulated ID 5 (1/61+1/61) must beat vector-only ID 6 (1/61), got [%d, %d]",
			results[0].ID, results[1].ID)
	}
	if results[1].ID != 6 {
		t.Errorf("vector-only ID 6 must stay second, got %d", results[1].ID)
	}
}

// TestFuseResultsWithNotes_RelevanceIsNeverRankInput pins the score-as-rank
// contract: two runs whose notes have DIFFERENT relevance magnitudes but the
// SAME positional order must produce identical fused output — only position
// contributes to RRF.
func TestFuseResultsWithNotes_RelevanceIsNeverRankInput(t *testing.T) {
	fts := []*domain.SearchResult{
		noteCandidate(10, "match", "", "alpha beta filler"),
		noteCandidate(20, "miss", "", "unrelated content here"),
	}
	vec := []*domain.VectorSearchResult{}

	notesA, rankedA := ChainOfNoteStage("alpha beta", fts)
	notesB, rankedB := ChainOfNoteStage("alpha zebra", fts)
	if len(notesA) != 2 || len(notesB) != 2 {
		t.Fatalf("expected notes from both runs")
	}
	if notesA[0].Relevance == notesB[0].Relevance {
		t.Fatalf("premise: relevance magnitudes must differ (both %f)", notesA[0].Relevance)
	}
	if rankedA[0].ID != rankedB[0].ID || rankedA[1].ID != rankedB[1].ID {
		t.Fatalf("premise: positional order must be identical")
	}

	outA := FuseResultsWithNotes(fts, vec, rankedA, FuseOptions{Limit: 10})
	outB := FuseResultsWithNotes(fts, vec, rankedB, FuseOptions{Limit: 10})
	if len(outA) != len(outB) {
		t.Fatalf("fused lengths differ: %d vs %d", len(outA), len(outB))
	}
	for i := range outA {
		if outA[i].ID != outB[i].ID {
			t.Fatalf("note relevance must never change ranking: [%v] vs [%v]", noteIDs(outA), noteIDs(outB))
		}
	}
}

// TestFuseResultsWithNotes_DecayIsFinalMultiplier pins that temporal decay
// remains a FINAL multiplicative pass on the notes path: without decay the
// accumulated overlap (2/61) beats a vector-only 1/61; with a 10-day-old
// candidate and a 1-day half-life the decayed 2/61*2^-10 must fall below
// the undecayed 1/61, flipping the order.
func TestFuseResultsWithNotes_DecayIsFinalMultiplier(t *testing.T) {
	ref := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	overlap := noteCandidate(1, "overlap", "", "overlap content")
	overlap.UpdatedAt = ref.AddDate(0, 0, -10)
	fts := []*domain.SearchResult{overlap}
	notes := []*domain.SearchResult{overlap}
	vec := []*domain.VectorSearchResult{{Observation: domain.Observation{ID: 9}, Similarity: 0.5}}

	plain := FuseResultsWithNotes(fts, vec, notes, FuseOptions{Limit: 10})
	if len(plain) != 2 || plain[0].ID != 1 {
		t.Fatalf("without decay accumulated ID 1 must lead, got %v", noteIDs(plain))
	}

	decayed := FuseResultsWithNotes(fts, vec, notes, FuseOptions{
		Limit:             10,
		DecayHalfLifeDays: 1,
		ReferenceTime:     ref,
	})
	if len(decayed) != 2 || decayed[0].ID != 9 {
		t.Fatalf("decay must be the final multiplier over accumulated note credit: got %v", noteIDs(decayed))
	}
}

func noteIDs(rs []*domain.SearchResult) []int64 {
	out := make([]int64, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}
