package ast

import (
	"fmt"
	"sort"
	"strings"
)

type FactKind string

const (
	KindFunc   FactKind = "func"
	KindMethod FactKind = "method"
	KindStruct FactKind = "struct"
	KindClass  FactKind = "class"
)

// DeclFact is one declared symbol with its deterministic span; no global IDs.
type DeclFact struct {
	Kind     FactKind
	Name     string
	Receiver string
	Line     int
	EndLine  int
	Col      int
	EndCol   int
}

// RefFact is one symbol reference; Target is a language-native handle. No IDs.
type RefFact struct {
	Target string
	Line   int
	Col    int
}

// FileFacts is the deterministic per-file IR emitted by v2 adapters: they MUST
// NOT assign global IDs nor invent ordering; Normalize owns it.
type FileFacts struct {
	Path         string
	Language     string
	Imports      []ImportFact
	Decls        []DeclFact
	Refs         []RefFact
	Capabilities Capabilities
	Diagnostics  []Diagnostic
}

// sortKey: NUL separator sorts below every printable byte; numeric fields are
// zero-padded so lexicographic order equals numeric order.
func (d DeclFact) sortKey() string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%08d", d.Kind, d.Name, d.Receiver, d.Line)
}
func (r RefFact) sortKey() string { return fmt.Sprintf("%s\x00%08d\x00%08d", r.Target, r.Line, r.Col) }
func (im ImportFact) sortKey() string {
	return fmt.Sprintf("%s\x00%s\x00%08d", im.ImportPath, im.LocalName, im.Line)
}

// validPath enforces repo-relative slash paths.
func validPath(p string) bool {
	if p == "" || strings.Contains(p, "\\") || strings.HasPrefix(p, "/") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == ".." || seg == "." {
			return false
		}
	}
	return true
}

func invalid(what, detail string) error {
	return fmt.Errorf("%w: %s: %s", ErrASTIRInvalid, what, detail)
}

// Validate reports the first defect in canonical field order; overclaims
// surface as ErrASTCapabilityOverclaim.
func (f FileFacts) Validate() error {
	if !validPath(f.Path) {
		return invalid("path", f.Path)
	}
	if f.Language == "" {
		return invalid("language", f.Language)
	}
	for i := range f.Imports {
		if im := &f.Imports[i]; im.ImportPath == "" || im.Line < 1 {
			return invalid("import", im.ImportPath)
		}
	}
	for i := range f.Decls {
		d := &f.Decls[i]
		if d.Kind == "" || d.Name == "" || strings.ContainsAny(d.Name, " \t") {
			return invalid("decl handle", d.Name)
		}
		if d.Line < 1 || (d.EndLine > 0 && d.EndLine < d.Line) ||
			(d.EndLine == d.Line && d.Col > 0 && d.EndCol > 0 && d.EndCol < d.Col) {
			return invalid("decl span", d.Name)
		}
	}
	for i := range f.Refs {
		if r := &f.Refs[i]; r.Target == "" || strings.ContainsAny(r.Target, " \t") || r.Line < 1 || r.Col < 0 {
			return invalid("ref", r.Target)
		}
	}
	if err := f.Capabilities.Validate(); err != nil {
		return err
	}
	if err := f.Capabilities.CheckEmitted(len(f.Decls), len(f.Refs)); err != nil {
		return err
	}
	for i := range f.Diagnostics {
		if d := &f.Diagnostics[i]; !d.Severity.Valid() || d.Code == "" || d.Message == "" || d.Line < 1 {
			return invalid("diagnostic", d.Code)
		}
	}
	return nil
}

// Normalize applies the canonical order: decls (kind, name, receiver, line),
// refs (target, line, col), imports (path, local, line), diagnostics canonically.
func (f FileFacts) Normalize() {
	sort.SliceStable(f.Imports, func(i, j int) bool { return f.Imports[i].sortKey() < f.Imports[j].sortKey() })
	sort.SliceStable(f.Decls, func(i, j int) bool { return f.Decls[i].sortKey() < f.Decls[j].sortKey() })
	sort.SliceStable(f.Refs, func(i, j int) bool { return f.Refs[i].sortKey() < f.Refs[j].sortKey() })
	SortDiagnostics(f.Diagnostics)
}
