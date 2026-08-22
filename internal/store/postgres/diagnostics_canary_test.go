package postgres

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestPackageDiagnosticsRedactProvenance is the package-wide AST canary for
// REQ-BPR-006, superseding the per-file line scanner that originally guarded
// only token_repository_integration_test.go. Verify-minted provenance is
// bearer-equivalent, so failure diagnostics in ANY test of this package must
// never format a whole TokenRecord/Principal value nor any digest, secret, or
// provenance credential. The canary parses every *_test.go in the package
// directory — including files behind the postgres_integration build tag — so
// reintroducing secret-bearing diagnostics fails the default suite without
// needing a database.
//
// Detection is origin-aware AST analysis, not type resolution: a value is
// forbidden when a diagnostic argument is (an alias of) an identifier naming
// a digest/secret/provenance credential or a record/principal value, selects
// a credential field or a whole credential-bearing struct field (a field
// named Record or Principal, or an identifier assigned from a
// constructor-style producer of a principal/token record), wraps a
// credential in a formatting call, or is a composite literal of a
// credential struct type. Bindings in the enclosing function are followed
// so renamed values stay classified: assignments and declarations, the
// result slots of multi-result credential producers (Verify/VerifyToken
// mint a principal, Issue/Rotate mint a token record — never their error
// companions), range value elements, and comma-ok lookups. Left-hand-side
// positions are preserved through non-identifier and blank slots, so an
// error companion after a selector or blank first slot is never misread as
// the producer's result 0. Selector left-hand sides (holder.p, err :=
// verifier.VerifyToken(...)) are tracked under their stable selector key,
// so a credential stored through a selector field stays classified when
// that selector is later formatted. Origins are ordered by position, and a
// diagnostic is judged only by origins bound before its use site, so a
// credential cannot be laundered by a later safe rebinding while a value
// that only becomes a credential after the diagnostic stays unflagged
// there. A proven-safe companion origin — a companion slot of a known
// multi-result producer or the ok slot of a comma-ok form — overrides
// credential-bearing name heuristics only for diagnostics after that
// binding and never suppresses an earlier unsafe origin; an arbitrary
// multi-result call's slots are never granted that precedence. Diagnostics
// through arbitrarily named *testing.T receivers — including subtest
// function literals — are inspected. Field-level diagnostics on
// non-credential fields (for example Record.Subject, GrantVersion,
// ExpiresAt, or paths through a record selector like issued.Record.ExpiresAt),
// static prose, sentinel errors, validators and other non-producing calls
// (validatePrincipal, recordCount), error and ok companions of multi-result
// calls, range keys, and safe derived scalars such as len(digest) or
// boolean comparisons remain explicitly allowed.
// TestDiagnosticsCanaryAdversarialFixtures pins these rules against evasion.
func TestPackageDiagnosticsRedactProvenance(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("diagnostics canary cannot enumerate package tests: %v", err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("diagnostics canary cannot parse %s: %v", name, err)
		}
		for _, violation := range scanTestFile(fset, file) {
			t.Errorf("%s:%d diagnostic argument %q violates redaction rule %q", name, violation.line, violation.expr, violation.rule)
		}
	}
}

// redactionViolation is one diagnostic argument that breached a redaction
// rule.
type redactionViolation struct {
	line int
	expr string
	rule string
}

// scanTestFile applies the redaction rules to every diagnostics call in one
// parsed test file and returns the violations found.
func scanTestFile(fset *token.FileSet, file *ast.File) []redactionViolation {
	var violations []redactionViolation
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		tNames := testingReceiverNames(fn)
		if len(tNames) == 0 {
			continue
		}
		origins := assignmentOrigins(fn)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isDiagnosticCall(call, tNames) {
				return true
			}
			for _, arg := range diagnosticValueArgs(call) {
				if rule := diagnosticRedactionRule(arg, origins); rule != "" {
					violations = append(violations, redactionViolation{
						line: fset.Position(arg.Pos()).Line,
						expr: types.ExprString(arg),
						rule: rule,
					})
				}
			}
			return true
		})
	}
	return violations
}

// diagnosticMethods are the testing.T reporting methods whose payloads reach
// the test output and therefore must stay provenance-safe.
var diagnosticMethods = map[string]bool{
	"Fatal": true, "Fatalf": true,
	"Error": true, "Errorf": true,
	"Log": true, "Logf": true,
	"Skip": true, "Skipf": true,
}

// isDiagnosticCall reports whether call is a reporting-method invocation on
// any identifier bound to a *testing.T in the enclosing function.
func isDiagnosticCall(call *ast.CallExpr, tNames map[string]bool) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	return ok && tNames[recv.Name] && diagnosticMethods[sel.Sel.Name]
}

// testingReceiverNames collects every identifier bound to a *testing.T
// anywhere in the function — its own parameters plus the parameters of
// nested function literals (subtests) — so diagnostics through arbitrarily
// named testing receivers are inspected.
func testingReceiverNames(fn *ast.FuncDecl) map[string]bool {
	names := map[string]bool{}
	collect := func(ft *ast.FuncType) {
		if ft.Params == nil {
			return
		}
		for _, param := range ft.Params.List {
			if !isTestingT(param.Type) {
				continue
			}
			for _, name := range param.Names {
				names[name.Name] = true
			}
		}
	}
	collect(fn.Type)
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			collect(lit.Type)
		}
		return true
	})
	return names
}

func isTestingT(typ ast.Expr) bool {
	switch v := typ.(type) {
	case *ast.StarExpr:
		return isTestingT(v.X)
	case *ast.SelectorExpr:
		pkg, ok := v.X.(*ast.Ident)
		return ok && pkg.Name == "testing" && v.Sel.Name == "T"
	}
	return false
}

// identOrigin is one recorded origin of an identifier, positioned where the
// binding occurred so diagnostics can consider only bindings that precede
// the use site. expr is the expression that fed the identifier; resultIndex
// is the zero-based result slot of expr that fed it when expr is a
// multi-result producer call or the ok verdict of a comma-ok form, and -1
// for single-value bindings. A resultIndex >= 1 origin is a proven-safe
// companion verdict — the err/ok slot of a known multi-result producer or
// the boolean ok of a comma-ok form — that can override credential-bearing
// name heuristics at later diagnostics.
type identOrigin struct {
	pos         token.Pos
	expr        ast.Expr
	resultIndex int
}

// assignmentOrigins records, for every identifier declaration, assignment,
// range value binding, or comma-ok binding in the function, the ordered
// origins that fed it. Alias rebinding (d := storedDigest), credential-field
// extraction (s := rec.Digest), whole-struct construction
// (p := domain.Principal{...}), multi-result producer slots
// (p, err := verifier.VerifyToken(...)), range elements
// (for _, r := range records), and comma-ok lookups
// (d, ok := digestByToken[id]) all stay classifiable through their origin.
// Original left-hand-side positions are preserved through non-identifier
// (holder.p, err := verifier.VerifyToken(...)) and blank (_, err := ...)
// slots, so a name is always classified by the result slot it actually
// received — an err companion after a selector or blank first slot is never
// misread as the producer's result 0. Selector left-hand sides are
// additionally recorded under their stable selector key, so a diagnostic
// formatting the same selector resolves the origin that fed it. The ok
// companion of a comma-ok form is recorded as a companion origin. Multiple
// origins of one name are all retained in position order: a diagnostic is
// judged by every origin recorded before its use site, so a credential can
// never be laundered by a later safe rebinding and a value that only became
// a credential after the diagnostic stays unflagged there. Range keys are
// deliberately not recorded: a range key is an index or a map key whose
// credential-ness the operand name cannot establish.
func assignmentOrigins(fn *ast.FuncDecl) map[string][]identOrigin {
	origins := map[string][]identOrigin{}
	record := func(name string, pos token.Pos, expr ast.Expr, resultIndex int) {
		if name == "" || name == "_" {
			return
		}
		origins[name] = append(origins[name], identOrigin{pos: pos, expr: expr, resultIndex: resultIndex})
	}
	// recordMultiName binds one or more names — or stable selector keys for
	// selector left-hand sides such as holder.p — to a single right-hand
	// expression, preserving the original left-hand-side positions: a
	// non-identifier or blank left-hand side still occupies its slot, so the
	// name at left-hand position i is always fed from result slot i of a
	// multi-result call and never from a compacted enumeration of the
	// identifier names. A call feeds name i from result slot i, so a
	// selector left-hand side receiving result 0 of a credential producer
	// keeps that classification when the same selector is later formatted.
	// Any other single-value expression (map index, type assertion) is a
	// comma-ok form whose first name receives the looked-up value, while the
	// second name, when present, receives the boolean verdict — recorded as
	// a companion origin so a credential-bearing name bound to that slot is
	// recognized as a misnomer rather than classified by name.
	recordMultiName := func(lhs []ast.Expr, pos token.Pos, expr ast.Expr) {
		keys := make([]string, len(lhs))
		for i, e := range lhs {
			switch v := e.(type) {
			case *ast.Ident:
				keys[i] = v.Name
			case *ast.SelectorExpr:
				keys[i] = selectorOriginKey(v)
			}
		}
		if call, ok := expr.(*ast.CallExpr); ok {
			for i, key := range keys {
				if key != "" {
					record(key, pos, call, i)
				}
			}
			return
		}
		if len(keys) > 0 && keys[0] != "" {
			record(keys[0], pos, expr, -1)
		}
		if len(keys) > 1 && keys[1] != "" {
			record(keys[1], pos, expr, 1)
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			switch {
			case len(stmt.Rhs) == 1 && len(stmt.Lhs) > 1:
				recordMultiName(stmt.Lhs, stmt.Pos(), stmt.Rhs[0])
			case len(stmt.Lhs) == len(stmt.Rhs):
				for i, lhs := range stmt.Lhs {
					if name, ok := lhs.(*ast.Ident); ok {
						record(name.Name, lhs.Pos(), stmt.Rhs[i], -1)
						continue
					}
					if key := selectorOriginKey(lhs); key != "" {
						record(key, lhs.Pos(), stmt.Rhs[i], -1)
					}
				}
			}
		case *ast.DeclStmt:
			gen, ok := stmt.Decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				return true
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Values) == 0 {
					continue
				}
				if len(vs.Values) == 1 {
					lhs := make([]ast.Expr, len(vs.Names))
					for i, name := range vs.Names {
						lhs[i] = name
					}
					recordMultiName(lhs, vs.Pos(), vs.Values[0])
					continue
				}
				if len(vs.Names) == len(vs.Values) {
					for i, name := range vs.Names {
						record(name.Name, name.Pos(), vs.Values[i], -1)
					}
				}
			}
		case *ast.RangeStmt:
			if value, ok := stmt.Value.(*ast.Ident); ok {
				record(value.Name, value.Pos(), stmt.X, -1)
			}
		}
		return true
	})
	return origins
}

// selectorOriginKey returns the stable origins-map key for a selector
// left-hand side such as holder.p: the rendered selector spine. Selector
// keys are always dotted, so they can never collide with identifier keys.
// Selectors whose base is not a plain identifier/selector chain (m[k].p)
// have no stable key and return "" — they stay untracked, exactly like an
// identifier with no recorded origin.
func selectorOriginKey(expr ast.Expr) string {
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if base := exprText(sel.X); base != "" {
			return base + "." + sel.Sel.Name
		}
	}
	return ""
}

// diagnosticValueArgs returns the value arguments of a diagnostic call. The
// leading format-string literal of the formatted variants is static prose and
// is excluded; every remaining argument is inspected.
func diagnosticValueArgs(call *ast.CallExpr) []ast.Expr {
	sel := call.Fun.(*ast.SelectorExpr)
	formatted := strings.HasSuffix(sel.Sel.Name, "f")
	args := call.Args
	if formatted && len(args) > 0 {
		if _, ok := args[0].(*ast.BasicLit); ok {
			args = args[1:]
		}
	}
	return args
}

// credentialFields are struct fields whose values are never printable in a
// diagnostic, regardless of the owning value.
var credentialFields = map[string]bool{
	"Digest":      true,
	"GrantDigest": true,
	"Secret":      true,
}

// maxAliasDepth bounds identifier-origin chasing so re-binding cycles can
// never loop the classifier.
const maxAliasDepth = 8

// diagnosticRedactionRule returns the violated rule name for a diagnostic
// value argument, or the empty string when the argument is safe to format.
// Only binding origins recorded before the argument's use site are
// considered, so rebinding order is respected.
func diagnosticRedactionRule(arg ast.Expr, origins map[string][]identOrigin) string {
	return exprRedactionRule(arg, arg.Pos(), origins, maxAliasDepth)
}

// exprRedactionRule classifies one expression recursively; depth bounds both
// alias chasing and nesting through wrapped calls. usePos is the position of
// the diagnostic argument being classified: identifier origins recorded at
// or after that position belong to later rebinding and are ignored.
func exprRedactionRule(arg ast.Expr, usePos token.Pos, origins map[string][]identOrigin, depth int) string {
	if arg == nil || depth <= 0 {
		return ""
	}
	switch v := arg.(type) {
	case *ast.BasicLit:
		return "" // static prose
	case *ast.BinaryExpr:
		return "" // comparisons/arithmetic yield scalars, not credentials
	case *ast.Ident:
		// Every origin bound before the use site is judged first, and any
		// unsafe origin returns immediately: a credential cannot be
		// laundered by a later safe rebinding, and rebinding to a
		// credential only flags later diagnostics.
		provenSafeCompanion := false
		for _, origin := range origins[v.Name] {
			if origin.pos >= usePos {
				continue
			}
			if origin.resultIndex >= 0 {
				if call, ok := origin.expr.(*ast.CallExpr); ok {
					if rule := producerResultRule(call.Fun, origin.resultIndex); rule != "" {
						return rule
					}
					// A companion slot (result slot >= 1) of a KNOWN
					// multi-result producer is a semantically proven err/ok
					// verdict binding at this origin. From this binding
					// onward it overrides the credential-bearing name
					// heuristic, because such a name bound to a proven
					// verdict slot is a misnomer, not a credential. Unsafe
					// origins above have already returned, so the override
					// can never suppress an earlier unsafe origin, and it
					// only applies to diagnostics after this origin's
					// position. Arbitrary multi-result calls are excluded:
					// their non-zero slots carry no proven verdict
					// semantics, so a credential-bearing name bound to one
					// stays classified by name.
					if origin.resultIndex >= 1 && knownMultiResultProducer(call.Fun) {
						provenSafeCompanion = true
					}
				} else if origin.resultIndex == 1 {
					// The ok slot of a comma-ok form (map index, type
					// assertion) is intrinsically boolean, so a
					// credential-bearing name bound to it is a misnomer.
					provenSafeCompanion = true
				}
				continue
			}
			if rule := exprRedactionRule(origin.expr, usePos, origins, depth-1); rule != "" {
				return rule
			}
		}
		if rule := identRedactionRule(v.Name); rule != "" && !provenSafeCompanion {
			return rule
		}
		return ""
	case *ast.ParenExpr:
		return exprRedactionRule(v.X, usePos, origins, depth-1)
	case *ast.StarExpr:
		return exprRedactionRule(v.X, usePos, origins, depth-1)
	case *ast.UnaryExpr:
		if v.Op == token.AND || v.Op == token.MUL {
			return exprRedactionRule(v.X, usePos, origins, depth-1)
		}
		return ""
	case *ast.SliceExpr:
		return exprRedactionRule(v.X, usePos, origins, depth-1)
	case *ast.IndexExpr:
		return exprRedactionRule(v.X, usePos, origins, depth-1)
	case *ast.CompositeLit:
		return typeSpineRedactionRule(exprText(v.Type))
	case *ast.SelectorExpr:
		if credentialFields[v.Sel.Name] {
			return "credential-field"
		}
		// A field literally named Record or Principal carries a whole
		// credential-bearing struct (issued.Record is a TokenRecord,
		// result.Principal a domain/identity principal); formatting it
		// prints every field at once. Deeper non-credential field paths
		// (issued.Record.ExpiresAt) are classified by their own final
		// field and stay safe.
		switch v.Sel.Name {
		case "Record":
			return "whole-record"
		case "Principal":
			return "whole-principal"
		}
		// A selector that was itself a left-hand side (holder.p, err :=
		// verifier.VerifyToken(...)) is classified by the origins bound to
		// its stable selector key, exactly like an identifier: result 0 of a
		// known producer keeps the whole-value classification under the
		// selector. Only origins recorded before the use site are judged,
		// each unsafe one returning immediately, so a later rebinding can
		// never launder an earlier unsafe origin, and the structural checks
		// above still fire before any origin. Companion verdicts earn no
		// name-override here: the selector spine heuristic below is not an
		// identifier name rule and stays active (fail-closed).
		if key := selectorOriginKey(v); key != "" {
			for _, origin := range origins[key] {
				if origin.pos >= usePos {
					continue
				}
				if origin.resultIndex >= 0 {
					if call, ok := origin.expr.(*ast.CallExpr); ok {
						if rule := producerResultRule(call.Fun, origin.resultIndex); rule != "" {
							return rule
						}
					}
					continue
				}
				if rule := exprRedactionRule(origin.expr, usePos, origins, depth-1); rule != "" {
					return rule
				}
			}
		}
		if spine := exprText(v); spine != "" && credentialText(spine) {
			return "credential-value"
		}
		return ""
	case *ast.CallExpr:
		// len exposes only a length: explicitly safe even for credentials.
		if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "len" {
			return ""
		}
		if fn := exprText(v.Fun); fn != "" && credentialText(fn) {
			return "credential-value"
		}
		// A constructor-style producer of a whole principal/token record
		// classifies its result: an assignment fed by it keeps the
		// whole-value classification under any fresh name.
		if rule := producerReturnRule(v.Fun); rule != "" {
			return rule
		}
		// A credential becomes printable output only through calls that
		// render their arguments (wrapped formatting, encoders, byte
		// conversions). Arbitrary calls — validators, verifiers, producers —
		// return fresh values: a credential-typed input does not classify
		// their result, so recursion stops there.
		if !rendersArguments(v.Fun) {
			return ""
		}
		for _, nested := range v.Args {
			if rule := exprRedactionRule(nested, usePos, origins, depth-1); rule != "" {
				return rule
			}
		}
		return ""
	}
	if spine := exprText(arg); spine != "" && credentialText(spine) {
		return "credential-value"
	}
	return ""
}

// rendersArguments reports whether a call renders its argument values into
// its result: formatting helpers, text quoting, binary-to-text encoders, and
// basic byte/rune conversions. Only for these does a credential argument
// become printable output.
func rendersArguments(fun ast.Expr) bool {
	switch v := fun.(type) {
	case *ast.Ident:
		switch v.Name {
		case "string", "[]byte", "rune":
			return true
		}
		return false
	case *ast.SelectorExpr:
		spine := exprText(v)
		switch spine {
		case "fmt.Sprint", "fmt.Sprintf", "fmt.Sprintln", "fmt.Errorf",
			"strconv.Quote", "strconv.QuoteToASCII", "strconv.QuoteToGraphic":
			return true
		}
		return strings.HasSuffix(spine, ".EncodeToString")
	}
	return false
}

// producerVerbs are the constructor-style call prefixes whose result IS a
// fresh whole value of the named kind. Only these classify their result:
// validators (validatePrincipal) and other non-producing helpers
// (recordCount) return a judgment or a scalar, not the credential-bearing
// struct itself, so their results stay safe to print.
var producerVerbs = []string{"make", "new", "build", "clone", "copy", "assemble", "mint", "create", "construct"}

// multiResultProducers names the repository credential producers that
// return a credential-bearing value alongside error companions, mapped to
// the classification of their result slot 0: Verify and VerifyToken mint a
// whole principal, Issue and Rotate a whole token record. Matching is on
// the exact lowercased final callee segment, so near names such as
// issuedSQL stay unclassified, and only result slot 0 is ever classified —
// the error or boolean companion of a multi-result call is never a
// credential and stays safe to print.
var multiResultProducers = map[string]string{
	"verify":      "whole-principal",
	"verifytoken": "whole-principal",
	"issue":       "whole-record",
	"rotate":      "whole-record",
}

// producerResultRule classifies result slot index of a call by its callee.
// Non-zero slots — the err and ok companions of multi-result calls — are
// never credentials and return "" immediately. Slot 0 is classified when
// the callee is an exact multi-result producer (Verify/VerifyToken minting
// a principal, Issue/Rotate minting a token record) or a constructor-style
// verb producer whose name ends in Principal or Record (makePrincipal,
// newTokenRecord, admin.NewPrincipal, assembleRotatedRecord). An assignment
// fed by such a slot then keeps the whole-value classification through
// ordered alias tracking, so a produced credential-bearing struct can never
// be printed under a fresh name. Spine qualifiers are ignored: only the
// final segment is matched.
func producerResultRule(fun ast.Expr, index int) string {
	if index != 0 {
		return ""
	}
	seg := exprText(fun)
	if seg == "" {
		return ""
	}
	if idx := strings.LastIndex(seg, "."); idx >= 0 {
		seg = seg[idx+1:]
	}
	lower := strings.ToLower(seg)
	if rule, ok := multiResultProducers[lower]; ok {
		return rule
	}
	for _, verb := range producerVerbs {
		if strings.HasPrefix(lower, verb) {
			if strings.HasSuffix(lower, "principal") {
				return "whole-principal"
			}
			if strings.HasSuffix(lower, "record") {
				return "whole-record"
			}
			return ""
		}
	}
	return ""
}

// producerReturnRule classifies the (single-result) value of a call used
// directly as a diagnostic argument: only a whole-principal or whole-record
// producer result is unprintable.
func producerReturnRule(fun ast.Expr) string {
	return producerResultRule(fun, 0)
}

// knownMultiResultProducer reports whether fun is one of the exact
// multi-result credential producers in multiResultProducers. Only the
// companion slots of these known producers — and the boolean ok slot of a
// comma-ok form — are semantically proven err/ok verdicts. An arbitrary
// multi-result call need not return a verdict in any slot, so its slots
// never earn safe-companion precedence and a credential-bearing name bound
// to one stays classified by name.
func knownMultiResultProducer(fun ast.Expr) bool {
	seg := exprText(fun)
	if seg == "" {
		return false
	}
	if idx := strings.LastIndex(seg, "."); idx >= 0 {
		seg = seg[idx+1:]
	}
	_, ok := multiResultProducers[strings.ToLower(seg)]
	return ok
}

func identRedactionRule(name string) string {
	// Err-prefixed identifiers are sentinel error values by convention, not
	// principal records; printing errors is always allowed.
	if strings.HasPrefix(name, "Err") {
		return ""
	}
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "provenance") || strings.Contains(lower, "tampered") || strings.Contains(lower, "secret") || strings.Contains(lower, "digest"):
		return "credential-value"
	case strings.Contains(lower, "principal"):
		return "whole-principal"
	case strings.Contains(lower, "record") || name == "rec" || name == "rotated":
		return "whole-record"
	}
	return ""
}

// typeSpineRedactionRule classifies a whole-struct value by its rendered
// type name: principals and token records are never printable in a
// diagnostic, whatever the variable is called.
func typeSpineRedactionRule(spine string) string {
	if spine == "" {
		return ""
	}
	if credentialText(spine) {
		return "credential-value"
	}
	lower := strings.ToLower(spine)
	switch {
	case strings.Contains(lower, "principal"):
		return "whole-principal"
	case strings.Contains(lower, "tokenrecord"):
		return "whole-record"
	}
	return ""
}

// credentialText reports whether a rendered expression names bearer
// material: verify-minted provenance, a tampered proof attempt, or a
// digest/secret value.
func credentialText(text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	return strings.Contains(lower, "provenance") || strings.Contains(lower, "tampered") || strings.Contains(lower, "digest") || strings.Contains(lower, "secret")
}

// exprText renders the identifier/selector spine of an expression so rules
// can match on what a value is named after. Shapes without a renderable
// spine (indexing, calls, composite literals) return "".
func exprText(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		if base := exprText(v.X); base != "" {
			return base + "." + v.Sel.Name
		}
		return v.Sel.Name
	case *ast.ParenExpr:
		return exprText(v.X)
	case *ast.StarExpr:
		return exprText(v.X)
	}
	return ""
}

// adversarialFixtures are evasion attempts the canary must flag. Each
// fixture is parsed as a synthetic source file, never executed, so the
// package-wide scan does not see it as live code.
var adversarialFixtures = []struct {
	name string
	src  string
	want []string
}{
	{
		name: "digest identifier through non-t receiver",
		src:  `func TestDigestIdent(tt *testing.T) { tt.Fatalf("stored=%q", storedDigest) }`,
		want: []string{"storedDigest:credential-value"},
	},
	{
		name: "aliased digest identifier",
		src:  `func TestAliasDigest(t *testing.T) { d := storedDigest; t.Fatalf("stored=%s", d) }`,
		want: []string{"d:credential-value"},
	},
	{
		name: "alias of credential field",
		src:  `func TestAliasField(t *testing.T) { s := rec.Digest; t.Errorf("field=%s", s) }`,
		want: []string{"s:credential-field"},
	},
	{
		name: "alias chain to whole record",
		src:  `func TestAliasRecord(t *testing.T) { a := rotated; b := a; t.Logf("%+v", b) }`,
		want: []string{"b:whole-record"},
	},
	{
		name: "var declaration alias",
		src:  `func TestVarAlias(t *testing.T) { var wrapped = grantDigest; t.Fatalf("%s", wrapped) }`,
		want: []string{"wrapped:credential-value"},
	},
	{
		name: "hex wrapped digest",
		src:  `func TestHexWrap(t *testing.T) { t.Fatalf("hex=%s", hex.EncodeToString(tokenDigest)) }`,
		want: []string{`hex.EncodeToString(tokenDigest):credential-value`},
	},
	{
		name: "sprintf wrapped digest",
		src:  `func TestSprintfWrap(t *testing.T) { t.Errorf("v=%v", fmt.Sprintf("%x", digest)) }`,
		want: []string{`fmt.Sprintf("%x", digest):credential-value`},
	},
	{
		name: "hmac derived credential",
		src:  `func TestDerivedMac(t *testing.T) { mac := digestMac.Sum(nil); t.Fatalf("%x", mac) }`,
		want: []string{"mac:credential-value"},
	},
	{
		name: "byte conversion wrap",
		src:  `func TestConversionWrap(t *testing.T) { t.Fatalf("raw=%s", string(storedDigest)) }`,
		want: []string{"string(storedDigest):credential-value"},
	},
	{
		name: "provenance selector spine",
		src:  `func TestSelectorSpine(t *testing.T) { t.Fatalf("%s", token.Provenance) }`,
		want: []string{"token.Provenance:credential-value"},
	},
	{
		name: "whole principal via arbitrary receiver",
		src:  `func TestWholePrincipal(qa *testing.T) { qa.Fatalf("p=%+v", principal) }`,
		want: []string{"principal:whole-principal"},
	},
	{
		name: "whole record via subtest receiver",
		src:  `func TestSubtest(t *testing.T) { t.Run("inner", func(inner *testing.T) { inner.Errorf("r=%+v", rec) }) }`,
		want: []string{"rec:whole-record"},
	},
	{
		name: "whole principal struct composite",
		src:  `func TestCompositePrincipal(t *testing.T) { p := domain.Principal{Subject: "s"}; t.Fatalf("%+v", p) }`,
		want: []string{"p:whole-principal"},
	},
	{
		name: "whole token record struct composite",
		src:  `func TestCompositeRecord(t *testing.T) { tr := postgres.TokenRecord{ID: 1}; t.Logf("%v", tr) }`,
		want: []string{"tr:whole-record"},
	},
	{
		name: "whole record via field selector",
		src:  `func TestRecordField(t *testing.T) { t.Fatalf("record=%+v", issued.Record) }`,
		want: []string{"issued.Record:whole-record"},
	},
	{
		name: "whole principal via field selector",
		src:  `func TestPrincipalField(t *testing.T) { t.Errorf("principal=%+v", result.Principal) }`,
		want: []string{"result.Principal:whole-principal"},
	},
	{
		name: "aliased whole-record selector",
		src:  `func TestSelectorAliasRecord(t *testing.T) { rec := issued.Record; t.Fatalf("%+v", rec) }`,
		want: []string{"rec:whole-record"},
	},
	{
		name: "producer-return principal assignment",
		src:  `func TestProducerPrincipal(t *testing.T) { p := makePrincipal(); t.Fatalf("%+v", p) }`,
		want: []string{"p:whole-principal"},
	},
	{
		name: "producer-return record assignment",
		src:  `func TestProducerRecord(t *testing.T) { tr := newTokenRecord(); t.Logf("%v", tr) }`,
		want: []string{"tr:whole-record"},
	},
	{
		name: "alias of producer return",
		src:  `func TestProducerAlias(t *testing.T) { p := clonePrincipal(); q := p; t.Fatalf("%+v", q) }`,
		want: []string{"q:whole-principal"},
	},
	{
		name: "method producer on selector spine",
		src:  `func TestMethodProducer(t *testing.T) { p := admin.NewPrincipal(); t.Fatalf("%+v", p) }`,
		want: []string{"p:whole-principal"},
	},
	{
		name: "wrapped formatting of whole-principal selector",
		src:  `func TestWrapRecordSelector(t *testing.T) { t.Fatalf("v=%s", fmt.Sprintf("%+v", result.Principal)) }`,
		want: []string{`fmt.Sprintf("%+v", result.Principal):whole-principal`},
	},
	{
		name: "verify-token result 0 under innocuous name",
		src:  `func TestVerifyTokenInnocuous(t *testing.T) { p, err := verifier.VerifyToken(ctx, secret, "read"); if err != nil { t.Fatalf("verify failed: %v", err) }; t.Fatalf("p=%+v", p) }`,
		want: []string{"p:whole-principal"},
	},
	{
		name: "alias of verify-token result",
		src:  `func TestVerifyTokenAlias(t *testing.T) { p, err := verifier.VerifyToken(ctx, secret, ""); q := p; t.Fatalf("%+v", q) }`,
		want: []string{"q:whole-principal"},
	},
	{
		name: "issue result 0 under innocuous name",
		src:  `func TestIssueInnocuous(t *testing.T) { issued, err := admin.tokens().Issue(ctx, identity.TokenIssue{Subject: "s"}); if err != nil { t.Fatal(err) }; t.Fatalf("issued=%+v", issued) }`,
		want: []string{"issued:whole-record"},
	},
	{
		name: "rotate result 0 under innocuous name",
		src:  `func TestRotateInnocuous(t *testing.T) { next, err := admin.tokens().Rotate(ctx, issued.Record.ID); if err != nil { t.Fatal(err) }; t.Logf("next=%+v", next) }`,
		want: []string{"next:whole-record"},
	},
	{
		name: "range value bound to credential operand",
		src:  `func TestRangeRecord(t *testing.T) { for _, r := range records { t.Errorf("r=%+v", r) } }`,
		want: []string{"r:whole-record"},
	},
	{
		name: "comma-ok lookup bound to credential map",
		src:  `func TestCommaOKDigest(t *testing.T) { d, ok := digestByToken[tokenID]; if !ok { t.Fatal("missing") }; t.Fatalf("d=%s", d) }`,
		want: []string{"d:credential-value"},
	},
	{
		name: "sensitive origin before safe rebinding stays flagged",
		src:  `func TestRebindSensitiveThenSafe(t *testing.T) { d := storedDigest; t.Fatalf("at=%s", d); d = "redacted"; t.Fatalf("after=%s", d) }`,
		want: []string{"d:credential-value", "d:credential-value"},
	},
	{
		name: "safe origin before sensitive rebinding stays safe at earlier diagnostic",
		src:  `func TestRebindSafeThenSensitive(t *testing.T) { d := "redacted"; t.Fatalf("before=%s", d); d = storedDigest; t.Fatalf("after=%s", d) }`,
		want: []string{"d:credential-value"},
	},
	{
		name: "prior unsafe origin still flags after later proven-safe companion",
		src:  `func TestSafeCompanionNoLaunder(t *testing.T) { d := storedDigest; t.Fatalf("at=%s", d); _, d = digestByToken[tokenID]; t.Fatalf("after=%s", d) }`,
		want: []string{"d:credential-value", "d:credential-value"},
	},
	{
		name: "selector LHS result 0 of known verify producer",
		src:  `func TestSelectorLHSResult0(t *testing.T) { holder.p, err = verifier.VerifyToken(ctx, secret, "read"); if err != nil { t.Fatal(err) }; t.Fatalf("principal=%+v", holder.p) }`,
		want: []string{"holder.p:whole-principal"},
	},
	{
		name: "arbitrary multi-result companion is not proven safe",
		src:  `func TestArbitraryPairNotSafe(t *testing.T) { other, principal := pairPrincipals(); t.Fatalf("principal=%+v", principal) }`,
		want: []string{"principal:whole-principal"},
	},
	{
		name: "selector prior unsafe origin not laundered by comma-ok rebinding",
		src:  `func TestSelectorNoLaunder(t *testing.T) { holder.p = storedDigest; t.Fatalf("at=%s", holder.p); holder.p, _ = digestByToken[tokenID]; t.Fatalf("after=%s", holder.p) }`,
		want: []string{"holder.p:credential-value", "holder.p:credential-value"},
	},
}

// safeFixtures must stay unflagged: safe length and boolean diagnostics,
// non-credential fields, sentinel errors, and static prose.
var safeFixtures = []struct {
	name string
	src  string
}{
	{
		name: "credential length diagnostics",
		src:  `func TestSafeLen(t *testing.T) { if len(storedDigest) != 64 { t.Fatalf("digest length=%d", len(storedDigest)) } }`,
	},
	{
		name: "credential boolean diagnostics",
		src:  `func TestSafeBool(t *testing.T) { provided := storedDigest != ""; t.Fatalf("provided=%t", provided); t.Errorf("empty=%v", storedDigest == "") }`,
	},
	{
		name: "non-credential fields and versions",
		src:  `func TestSafeFields(t *testing.T) { t.Fatalf("subject=%s version=%d expires=%v", rec.Subject, principal.GrantVersion, issued.Record.ExpiresAt) }`,
	},
	{
		name: "sentinel errors and static prose",
		src:  `func TestSafeErrors(t *testing.T) { t.Fatalf("err=%v", ErrGrantDigestRequired); t.Error("static text") }`,
	},
	{
		name: "length of wrapped credential",
		src:  `func TestSafeWrappedLen(t *testing.T) { t.Fatalf("len=%d", len(hex.EncodeToString(digest))) }`,
	},
	{
		name: "producer call argument does not classify its result",
		src:  `func TestSafeProducerArg(t *testing.T) { err := validatePrincipal(principal); t.Fatalf("rejected: %v", err) }`,
	},
	{
		name: "unknown identifiers stay unclassified",
		src:  `func TestSafeUnknown(t *testing.T) { t.Fatalf("value=%v", mystery) }`,
	},
	{
		name: "record fields reached through the record selector",
		src:  `func TestSafeRecordFieldPath(t *testing.T) { t.Fatalf("expires=%v subject=%s type=%s", issued.Record.ExpiresAt, rotated.Record.Subject, rotated.Record.PrincipalType) }`,
	},
	{
		name: "non-producing helpers with credential-ish names stay safe",
		src:  `func TestSafeNonProducer(t *testing.T) { n := recordCount(); t.Fatalf("count=%d", n) }`,
	},
	{
		name: "producer result inspected field-wise",
		src:  `func TestSafeProducerField(t *testing.T) { p := makePrincipal(); t.Fatalf("subject=%s", p.Subject) }`,
	},
	{
		name: "producer result never printed",
		src:  `func TestSafeProducerUnprinted(t *testing.T) { p := makePrincipal(); _ = p; t.Fatal("static text") }`,
	},
	{
		name: "multi-result error companions stay safe",
		src:  `func TestMultiResultErrSafe(t *testing.T) { _, err := verifier.VerifyToken(ctx, secret, "read"); if err != nil { t.Fatalf("verify failed: %v", err) } }`,
	},
	{
		name: "multi-result value inspected field-wise",
		src:  `func TestMultiResultFieldSafe(t *testing.T) { p, err := verifier.VerifyToken(ctx, secret, ""); if err != nil { t.Fatal(err) }; t.Fatalf("subject=%s", p.Subject) }`,
	},
	{
		name: "comma-ok boolean companion stays safe",
		src:  `func TestCommaOKBoolSafe(t *testing.T) { _, ok := digestByToken[tokenID]; t.Fatalf("found=%t", ok) }`,
	},
	{
		name: "non-identifier and blank first LHS keep companion result index",
		src:  `func TestSelectorLHSCompanionSafe(t *testing.T) { holder.p, err := verifier.VerifyToken(ctx, secret, "read"); if err != nil { t.Fatal(err) }; _, second := verifier.VerifyToken(ctx, secret, ""); if second != nil { t.Fatalf("second failed: %v", second) } }`,
	},
	{
		name: "proven-safe multi-result companion overrides credential name",
		src:  `func TestPrincipalErrNameSafe(t *testing.T) { _, principalErr := verifier.VerifyToken(ctx, secret, "read"); if principalErr != nil { t.Fatalf("verify failed: %v", principalErr) } }`,
	},
	{
		name: "proven-safe comma-ok companion overrides credential name",
		src:  `func TestDigestOKNameSafe(t *testing.T) { _, digestOK := digestByToken[tokenID]; t.Fatalf("found=%t", digestOK) }`,
	},
	{
		name: "range over innocuous operand stays safe",
		src:  `func TestRangeSafe(t *testing.T) { for _, tc := range cases { t.Fatalf("case=%v", tc) } }`,
	},
	{
		name: "key-only range over credential-named operand stays safe",
		src:  `func TestRangeKeySafe(t *testing.T) { for i := range digests { t.Fatalf("i=%d", i) } }`,
	},
}

// TestDiagnosticsCanaryAdversarialFixtures pins the canary rules against the
// known evasion vectors (digest identifiers, aliases, wrapped formatting,
// arbitrary testing receivers, whole credential structs) and against false
// positives on safe length/boolean diagnostics.
func TestDiagnosticsCanaryAdversarialFixtures(t *testing.T) {
	for _, fx := range adversarialFixtures {
		got := runCanaryFixture(t, fx.name, fx.src)
		gotEntries := make([]string, 0, len(got))
		for _, v := range got {
			gotEntries = append(gotEntries, v.expr+":"+v.rule)
		}
		sort.Strings(gotEntries)
		wantEntries := append([]string(nil), fx.want...)
		sort.Strings(wantEntries)
		if strings.Join(gotEntries, ";") != strings.Join(wantEntries, ";") {
			t.Errorf("fixture %q: got violations %v, want %v", fx.name, gotEntries, wantEntries)
		}
	}
	for _, fx := range safeFixtures {
		if got := runCanaryFixture(t, fx.name, fx.src); len(got) != 0 {
			t.Errorf("safe fixture %q flagged false positives: %v", fx.name, got)
		}
	}
}

// runCanaryFixture parses one synthetic source fragment as a test file and
// scans it with the same rules as the package-wide canary.
func runCanaryFixture(t *testing.T, name, src string) []redactionViolation {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture_"+name+".go", "package postgres\n\nimport \"testing\"\n\n"+src+"\n", 0)
	if err != nil {
		t.Fatalf("fixture %q does not parse: %v", name, err)
	}
	return scanTestFile(fset, file)
}
