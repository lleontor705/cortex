package tui

import (
	"strings"
	"testing"
)

func navTestLabel(s Screen) string {
	switch s {
	case ScreenDashboard:
		return "Dashboard"
	case ScreenSearch:
		return "Search"
	case ScreenSearchResults:
		return "Search Results"
	case ScreenObservationDetail:
		return "Detail"
	case ScreenTimeline:
		return "Timeline"
	default:
		return "Screen"
	}
}

// seedNavFrom positions m on screen with origin as the level beneath it on the
// live stack, mirroring how a handler builds history before a screen is
// displayed. The Dashboard floor is implicit and therefore never pushed.
func seedNavFrom(m *Model, screen, origin Screen) {
	m.Nav = NavStack{}
	if origin != navFloor {
		m.Nav.Push(origin)
	}
	if screen != navFloor {
		m.Nav.Push(screen)
	}
	m.Screen = screen
}

func TestNavStackZeroValueAtFloor(t *testing.T) {
	var n NavStack
	if got := n.Current(); got != ScreenDashboard {
		t.Fatalf("Current() = %v, want Dashboard", got)
	}
	if got := n.Depth(); got != 1 {
		t.Fatalf("Depth() = %d, want 1", got)
	}
	if got := n.Breadcrumb(navTestLabel); got != "Dashboard" {
		t.Fatalf("Breadcrumb() = %q, want %q", got, "Dashboard")
	}
}

func TestNavStackPushPopRoundTrip(t *testing.T) {
	path := []Screen{ScreenSearch, ScreenSearchResults, ScreenObservationDetail, ScreenTimeline}
	var n NavStack
	for _, s := range path {
		n.Push(s)
	}
	if got, want := n.Current(), ScreenTimeline; got != want {
		t.Fatalf("Current() after pushes = %v, want %v", got, want)
	}
	// Pop returns screens in reverse order, bottoming out at the floor.
	want := []Screen{ScreenObservationDetail, ScreenSearchResults, ScreenSearch, ScreenDashboard}
	for i, w := range want {
		if got := n.Pop(); got != w {
			t.Fatalf("Pop #%d = %v, want %v", i+1, got, w)
		}
	}
}

func TestNavStackPopAtFloorIsNoop(t *testing.T) {
	cases := []struct {
		name string
		path []Screen
	}{
		{"fresh", nil},
		{"exhausted", []Screen{ScreenSearch, ScreenSearchResults}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n NavStack
			for _, s := range tc.path {
				n.Push(s)
			}
			for range tc.path {
				n.Pop()
			}
			if got := n.Pop(); got != ScreenDashboard {
				t.Fatalf("Pop at floor = %v, want Dashboard", got)
			}
			if got := n.Current(); got != ScreenDashboard {
				t.Fatalf("Current() at floor = %v, want Dashboard", got)
			}
			if got := n.Depth(); got != 1 {
				t.Fatalf("Depth() at floor = %d, want 1", got)
			}
			if len(n.stack) != 0 {
				t.Fatalf("stack at floor = %v, want empty", n.stack)
			}
		})
	}
}

func TestNavStackResetToDashboard(t *testing.T) {
	cases := []struct {
		name string
		path []Screen
	}{
		{"fromFloor", nil},
		{"fromDepth2", []Screen{ScreenSearch}},
		{"fromDepth5", []Screen{ScreenSearch, ScreenSearchResults, ScreenObservationDetail, ScreenTimeline}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n NavStack
			for _, s := range tc.path {
				n.Push(s)
			}
			n.ResetTo(ScreenDashboard)
			if got := n.Current(); got != ScreenDashboard {
				t.Fatalf("Current() after ResetTo = %v, want Dashboard", got)
			}
			if len(n.stack) != 0 {
				t.Fatalf("stack after ResetTo = %v, want empty", n.stack)
			}
			if got := n.Depth(); got != 1 {
				t.Fatalf("Depth() after ResetTo = %d, want 1", got)
			}
			if got := n.Pop(); got != ScreenDashboard {
				t.Fatalf("Pop after ResetTo = %v, want Dashboard", got)
			}
		})
	}
}

func TestNavStackResetToNonFloorTarget(t *testing.T) {
	var n NavStack
	n.Push(ScreenSearch)
	n.Push(ScreenSearchResults)
	n.ResetTo(ScreenSearch)
	if got := n.Current(); got != ScreenSearch {
		t.Fatalf("Current() after ResetTo(Search) = %v, want Search", got)
	}
	if got := n.Depth(); got != 2 {
		t.Fatalf("Depth() after ResetTo(Search) = %d, want 2", got)
	}
	if got := n.Pop(); got != ScreenDashboard {
		t.Fatalf("Pop after ResetTo(Search) = %v, want Dashboard", got)
	}
}

func TestNavStackBreadcrumbAndDepth(t *testing.T) {
	cases := []struct {
		name      string
		path      []Screen
		wantCrumb string
		wantDepth int
	}{
		{"floor", nil, "Dashboard", 1},
		{"one", []Screen{ScreenSearch}, "Dashboard / Search", 2},
		{"three", []Screen{ScreenSearch, ScreenSearchResults, ScreenObservationDetail}, "Dashboard / Search / Search Results / Detail", 4},
		{"five", []Screen{ScreenSearch, ScreenSearchResults, ScreenObservationDetail, ScreenTimeline}, "Dashboard / Search / Search Results / Detail / Timeline", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n NavStack
			for _, s := range tc.path {
				n.Push(s)
			}
			if got := n.Breadcrumb(navTestLabel); got != tc.wantCrumb {
				t.Fatalf("Breadcrumb() = %q, want %q", got, tc.wantCrumb)
			}
			if got := n.Depth(); got != tc.wantDepth {
				t.Fatalf("Depth() = %d, want %d", got, tc.wantDepth)
			}
		})
	}
}

// ─── Live Model navigation stack (tui-redesign-022/025b) ────────────────────
//
// Model.NavPush/NavPop/NavReset are the seam every screen handler and global
// binding now drives (REQ-NAV-003). NavPop is the esc equivalent and NavReset
// the q equivalent, so these oracles prove the live stack supports multi-level
// history end to end at the Model boundary.

// TestModelNavEscWalkBackThroughFourLevels proves a four-level-deep path walks
// back one level per esc and bottoms out at the Dashboard floor.
func TestModelNavEscWalkBackThroughFourLevels(t *testing.T) {
	m := New(&Deps{Version: "test"})
	path := []Screen{ScreenSearch, ScreenSearchResults, ScreenObservationDetail, ScreenTimeline}
	for _, s := range path {
		m.NavPush(s)
	}
	if got := m.NavDepth(); got != 5 {
		t.Fatalf("NavDepth() = %d, want 5", got)
	}
	const wantCrumb = "Dashboard / Search / Search Results / Detail / Timeline"
	if got := m.NavBreadcrumb(); got != wantCrumb {
		t.Fatalf("NavBreadcrumb() = %q, want %q", got, wantCrumb)
	}

	back := []Screen{ScreenObservationDetail, ScreenSearchResults, ScreenSearch, ScreenDashboard}
	for i, w := range back {
		if got := m.NavPop(); got != w {
			t.Fatalf("esc #%d = %v, want %v", i+1, got, w)
		}
		if m.Screen != w {
			t.Fatalf("Screen after esc #%d = %v, want %v", i+1, m.Screen, w)
		}
	}

	// Floor: esc at Dashboard is a no-op.
	if got := m.NavPop(); got != ScreenDashboard {
		t.Fatalf("esc at floor = %v, want Dashboard", got)
	}
	if got := m.NavDepth(); got != 1 {
		t.Fatalf("NavDepth() at floor = %d, want 1", got)
	}
	if got := m.NavBreadcrumb(); got != "Dashboard" {
		t.Fatalf("NavBreadcrumb() at floor = %q, want Dashboard", got)
	}
}

// TestModelNavResetOnQ pins q semantics: clear history to the Dashboard floor.
func TestModelNavResetOnQ(t *testing.T) {
	m := New(&Deps{Version: "test"})
	for _, s := range []Screen{ScreenSearch, ScreenSearchResults, ScreenObservationDetail, ScreenTimeline} {
		m.NavPush(s)
	}
	if got := m.NavReset(); got != ScreenDashboard {
		t.Fatalf("NavReset() = %v, want Dashboard", got)
	}
	if m.Screen != ScreenDashboard {
		t.Fatalf("Screen after reset = %v, want Dashboard", m.Screen)
	}
	if got := m.NavDepth(); got != 1 {
		t.Fatalf("NavDepth() after reset = %d, want 1", got)
	}
	if got := m.NavPop(); got != ScreenDashboard {
		t.Fatalf("esc after reset = %v, want Dashboard", got)
	}
}

// TestModelNavPushSameScreenIsNoop guards against spurious duplicate levels.
func TestModelNavPushSameScreenIsNoop(t *testing.T) {
	m := New(&Deps{Version: "test"})
	m.NavPush(ScreenSearch)
	m.NavPush(ScreenSearch)
	if got := m.NavDepth(); got != 2 {
		t.Fatalf("NavDepth() = %d, want 2", got)
	}
}

// TestModelNavParentPeek proves the stack-parent accessor that the Help origin
// and the header chrome fallback read from (REQ-NAV-003).
func TestModelNavParentPeek(t *testing.T) {
	m := New(&Deps{Version: "test"})
	if got := m.NavParent(); got != ScreenDashboard {
		t.Fatalf("NavParent() at floor = %v, want Dashboard", got)
	}
	m.NavPush(ScreenSearch)
	m.NavPush(ScreenSearchResults)
	if got := m.NavParent(); got != ScreenSearch {
		t.Fatalf("NavParent() = %v, want Search", got)
	}
	m.NavPop()
	if got := m.NavParent(); got != ScreenDashboard {
		t.Fatalf("NavParent() after pop = %v, want Dashboard", got)
	}
}

// TestModelHeaderBreadcrumbRendersLiveStack proves the header chrome renders
// the full live path, not just the current screen, on every non-dashboard view.
func TestModelHeaderBreadcrumbRendersLiveStack(t *testing.T) {
	m := NewWithScreen(&Deps{Version: "test"}, ScreenSearch)
	m.Width, m.Height = 120, 40
	m.NavPush(ScreenSearchResults)
	m.NavPush(ScreenObservationDetail)

	out := m.View()
	if !strings.Contains(out, "Dashboard / Search / Search Results / Detail") {
		t.Fatalf("View() missing live breadcrumb path, got:\n%s", out)
	}
}
