package ast

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrASTIRInvalid marks structurally invalid FileFacts (bad path, handle, or span).
	ErrASTIRInvalid = errors.New("AST_IR_INVALID")
	// ErrASTCapabilityOverclaim marks declared capabilities contradicted by emitted facts.
	ErrASTCapabilityOverclaim = errors.New("AST_CAPABILITY_OVERCLAIM")
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

var severityRank = map[Severity]int{SeverityError: 0, SeverityWarning: 1, SeverityInfo: 2}

func (s Severity) rank() int {
	if r, ok := severityRank[s]; ok {
		return r
	}
	return 3
}

func (s Severity) Valid() bool { return s.rank() < 3 }

// Diagnostic is one deterministic parse finding. It carries no IDs.
type Diagnostic struct {
	Severity Severity
	Line     int
	Column   int
	Code     string
	Message  string
}

// sortKey: NUL separator sorts below every printable byte; numeric fields are
// zero-padded so lexicographic order equals numeric order.
func (d Diagnostic) sortKey() string {
	return fmt.Sprintf("%d\x00%08d\x00%08d\x00%s\x00%s", d.Severity.rank(), d.Line, d.Column, d.Code, d.Message)
}

// SortDiagnostics orders canonically: severity, line, column, code, message.
func SortDiagnostics(diags []Diagnostic) {
	sort.SliceStable(diags, func(i, j int) bool { return diags[i].sortKey() < diags[j].sortKey() })
}
