package ast

import (
	"errors"
	"fmt"
	"testing"
)

func validFacts() FileFacts {
	return FileFacts{
		Path:         "internal/domain/ast/facts.go",
		Language:     "go",
		Imports:      []ImportFact{{ImportPath: "fmt", LocalName: "fmt", Line: 3}},
		Decls:        []DeclFact{{Kind: KindFunc, Name: "Compute", Line: 10, EndLine: 20, Col: 6, EndCol: 1}, {Kind: KindStruct, Name: "Service", Line: 5, EndLine: 8}},
		Refs:         []RefFact{{Target: "Service", Line: 12, Col: 2}},
		Capabilities: Capabilities{Language: "go", Declarations: CapabilityL1, References: CapabilityL1},
		Diagnostics:  []Diagnostic{{Severity: SeverityInfo, Line: 1, Column: 1, Code: "AST_PARSE_NOTE", Message: "note"}},
	}
}

func TestFileFacts_Validate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*FileFacts)
		want   error
	}{
		{"happy", func(f *FileFacts) {}, nil},
		{"empty path", func(f *FileFacts) { f.Path = "" }, ErrASTIRInvalid},
		{"backslash path", func(f *FileFacts) { f.Path = "internal\\facts.go" }, ErrASTIRInvalid},
		{"parent traversal", func(f *FileFacts) { f.Path = "../facts.go" }, ErrASTIRInvalid},
		{"import empty path", func(f *FileFacts) { f.Imports[0].ImportPath = "" }, ErrASTIRInvalid},
		{"decl empty name", func(f *FileFacts) { f.Decls[0].Name = "" }, ErrASTIRInvalid},
		{"decl zero line", func(f *FileFacts) { f.Decls[0].Line = 0 }, ErrASTIRInvalid},
		{"decl endline before line", func(f *FileFacts) { f.Decls[0].EndLine = 9 }, ErrASTIRInvalid},
		{"ref empty target", func(f *FileFacts) { f.Refs[0].Target = "" }, ErrASTIRInvalid},
		{"ref target with space", func(f *FileFacts) { f.Refs[0].Target = "a b" }, ErrASTIRInvalid},
		{"capability empty language", func(f *FileFacts) { f.Capabilities.Language = "" }, ErrASTIRInvalid},
		{"capability bad level", func(f *FileFacts) { f.Capabilities.Declarations = CapabilityLevel(7) }, ErrASTIRInvalid},
		{"diag unknown severity", func(f *FileFacts) { f.Diagnostics[0].Severity = "fatal" }, ErrASTIRInvalid},
		{"overclaim refs", func(f *FileFacts) { f.Capabilities.References = CapabilityL0 }, ErrASTCapabilityOverclaim},
		{"overclaim decls", func(f *FileFacts) { f.Capabilities.Declarations = CapabilityL0 }, ErrASTCapabilityOverclaim},
	}
	for _, tc := range cases {
		f := validFacts()
		tc.mutate(&f)
		if err := f.Validate(); !errors.Is(err, tc.want) {
			t.Errorf("%s: expected %v, got %v", tc.name, tc.want, err)
		}
	}
	broken := validFacts()
	broken.Path, broken.Language, broken.Decls[0].Name, broken.Refs[0].Target = "bad\\path", "", "", ""
	first := broken.Validate().Error()
	if broken.Validate().Error() != first {
		t.Fatal("expected deterministic first defect")
	}
}

func TestFileFacts_DeterministicOrdering(t *testing.T) {
	f := validFacts()
	f.Decls = []DeclFact{{Kind: KindStruct, Name: "Zeta", Line: 1}, {Kind: KindFunc, Name: "beta", Line: 9}, {Kind: KindFunc, Name: "beta", Receiver: "T", Line: 3}, {Kind: KindFunc, Name: "alpha", Line: 2}}
	f.Refs = []RefFact{{Target: "b.T", Line: 2, Col: 1}, {Target: "a.X", Line: 9, Col: 2}, {Target: "a.X", Line: 3, Col: 5}}
	f.Diagnostics = []Diagnostic{{Severity: SeverityInfo, Line: 1, Column: 1, Code: "N", Message: "m"}, {Severity: SeverityError, Line: 5, Column: 1, Code: "E", Message: "m"}, {Severity: SeverityError, Line: 1, Column: 1, Code: "E", Message: "m"}}
	f.Normalize()
	key := func(i int) string {
		return fmt.Sprintf("%s:%s:%s", f.Decls[i].Kind, f.Decls[i].Name, f.Decls[i].Receiver)
	}
	if got := key(0) + "," + key(1) + "," + key(2) + "," + key(3); got != "func:alpha:,func:beta:,func:beta:T,struct:Zeta:" {
		t.Fatalf("decl order: got %q", got)
	}
	if f.Refs[0].Target != "a.X" || f.Refs[0].Line != 3 || f.Refs[1].Target != "a.X" || f.Refs[1].Line != 9 || f.Refs[2].Target != "b.T" {
		t.Fatalf("ref order: got %v", f.Refs)
	}
	if f.Diagnostics[0].Severity != SeverityError || f.Diagnostics[0].Line != 1 || f.Diagnostics[1].Line != 5 || f.Diagnostics[2].Severity != SeverityInfo {
		t.Fatalf("diag order: got %v", f.Diagnostics)
	}
	f.Normalize()
	if f.Decls[0].Name != "alpha" || f.Refs[2].Target != "b.T" || f.Diagnostics[2].Severity != SeverityInfo || f.Validate() != nil {
		t.Fatal("Normalize not idempotent or normalized facts invalid")
	}
}

func TestCapability_Contract(t *testing.T) {
	if CapabilityL0.String() != "L0" || CapabilityL3.String() != "L3" || CapabilityLevel(9).String() != "L?" {
		t.Errorf("canonical tokens broken: %s %s %s", CapabilityL0, CapabilityL3, CapabilityLevel(9))
	}
	if got, err := ParseCapabilityLevel("L2"); err != nil || got != CapabilityL2 {
		t.Errorf("ParseCapabilityLevel(L2) = %v, %v", got, err)
	}
	if _, err := ParseCapabilityLevel("L4"); !errors.Is(err, ErrASTIRInvalid) {
		t.Errorf("expected AST_IR_INVALID for L4, got %v", err)
	}
	if err := (Capabilities{Language: "go", Declarations: CapabilityL1, References: CapabilityL3}).Validate(); err != nil {
		t.Errorf("expected valid capabilities, got %v", err)
	}
	zero := Capabilities{Language: "go", Declarations: CapabilityL0, References: CapabilityL0}
	if err := zero.CheckEmitted(0, 0); err != nil {
		t.Errorf("L0 with zero emissions must pass, got %v", err)
	}
	if err := zero.CheckEmitted(1, 0); !errors.Is(err, ErrASTCapabilityOverclaim) {
		t.Errorf("expected overclaim for decls at L0, got %v", err)
	}
	if err := zero.CheckEmitted(0, 1); !errors.Is(err, ErrASTCapabilityOverclaim) {
		t.Errorf("expected overclaim for refs at L0, got %v", err)
	}
}

func TestDiagnostics_CanonicalOrder(t *testing.T) {
	diags := []Diagnostic{{Severity: SeverityWarning, Line: 1, Column: 1, Code: "W", Message: "m"}, {Severity: SeverityError, Line: 1, Column: 1, Code: "A", Message: "m2"}, {Severity: SeverityInfo, Line: 1, Column: 1, Code: "N", Message: "m"}, {Severity: SeverityError, Line: 2, Column: 1, Code: "A", Message: "m"}, {Severity: SeverityError, Line: 1, Column: 1, Code: "A", Message: "m1"}}
	SortDiagnostics(diags)
	want := []string{"error:1:A:m1", "error:1:A:m2", "error:2:A:m", "warning:1:W:m", "info:1:N:m"}
	for i, d := range diags {
		key := fmt.Sprintf("%s:%d:%s:%s", d.Severity, d.Line, d.Code, d.Message)
		if key != want[i] {
			t.Fatalf("diagnostic order: expected %v, got %s at %d", want, key, i)
		}
	}
}
