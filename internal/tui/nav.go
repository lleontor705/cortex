package tui

import "strings"

// navFloor is the root of the navigation stack. The stack can never pop below
// it: esc on Dashboard is a no-op and ResetTo returns here (q semantics).
const navFloor = ScreenDashboard

// NavStack models forward navigation as a stack of screens. The zero value is
// a valid stack positioned at the Dashboard floor, so callers never need a
// constructor.
//
// The floor is implicit rather than stored: an empty stack means "at
// Dashboard", which keeps reset semantics simple while guaranteeing Current
// always returns a usable screen.
type NavStack struct {
	stack []Screen
}

// Push records forward navigation onto the stack.
func (n *NavStack) Push(s Screen) {
	n.stack = append(n.stack, s)
}

// Pop moves back one screen and returns the new current screen. At the
// Dashboard floor it is a no-op that returns the floor.
func (n *NavStack) Pop() Screen {
	if len(n.stack) == 0 {
		return navFloor
	}
	n.stack = n.stack[:len(n.stack)-1]
	return n.Current()
}

// ResetTo clears the navigation history and lands on s. Callers pass navFloor
// (the q binding); a non-floor target starts a fresh path directly above the
// floor.
func (n *NavStack) ResetTo(s Screen) {
	n.stack = nil
	if s != navFloor {
		n.stack = append(n.stack, s)
	}
}

// Current returns the screen at the top of the stack, or the Dashboard floor
// when the stack is empty.
func (n *NavStack) Current() Screen {
	if len(n.stack) == 0 {
		return navFloor
	}
	return n.stack[len(n.stack)-1]
}

// Depth returns the number of breadcrumb segments, including the implicit
// Dashboard floor. A fresh stack at Dashboard therefore has depth 1.
func (n *NavStack) Depth() int {
	return len(n.stack) + 1
}

// Breadcrumb renders the full navigation path, e.g.
// "Dashboard / Search / Search Results". label maps a screen to its display
// name; production passes Model.screenName so the breadcrumb stays in sync
// with the header vocabulary.
func (n *NavStack) Breadcrumb(label func(Screen) string) string {
	segments := make([]string, 0, len(n.stack)+1)
	segments = append(segments, label(navFloor))
	for _, s := range n.stack {
		segments = append(segments, label(s))
	}
	return strings.Join(segments, " / ")
}
