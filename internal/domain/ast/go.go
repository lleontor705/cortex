package ast

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

// extractGoFile uses go/parser and go/ast for high-fidelity Go AST extraction.
func extractGoFile(fullPath, relPath string) *ExtractionResult {
	res := &ExtractionResult{
		Entities:      make([]CodeEntity, 0),
		Relationships: make([]CodeRelationship, 0),
		Imports:       make([]ImportFact, 0),
		Exports:       make([]ExportFact, 0),
	}

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, fullPath, nil, parser.ParseComments)
	if err != nil {
		return res
	}

	pkgName := node.Name.Name
	fileEntityID := fmt.Sprintf("module:%s", relPath)

	// File / Module entity
	res.Entities = append(res.Entities, CodeEntity{
		ID:         fileEntityID,
		Name:       filepath.Base(relPath),
		Kind:       code.KindModule,
		File:       relPath,
		Line:       1,
		EndLine:    fset.Position(node.End()).Line,
		Package:    pkgName,
		DocSummary: cleanDoc(node.Doc.Text()),
		Visibility: code.VisibilityPublic,
	})

	// Extract imports
	for _, imp := range node.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		targetID := fmt.Sprintf("pkg:%s", importPath)
		impLine := fset.Position(imp.Pos()).Line

		localName := filepath.Base(importPath)
		isWildcard := false
		if imp.Name != nil {
			localName = imp.Name.Name
			if localName == "." {
				isWildcard = true
			}
		}

		res.Relationships = append(res.Relationships, CodeRelationship{
			Source:     fileEntityID,
			Target:     targetID,
			Relation:   code.RelationImports,
			Confidence: code.ConfidenceExtracted,
			Reasoning:  "Direct Go import statement",
		})

		res.Imports = append(res.Imports, ImportFact{
			SourceFile:   relPath,
			ImportPath:   importPath,
			LocalName:    localName,
			ImportedName: "*",
			IsWildcard:   isWildcard,
			Line:         impLine,
		})
	}

	// Extract declarations
	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			extractGoGenDecl(fset, d, relPath, pkgName, fileEntityID, res)
		case *ast.FuncDecl:
			extractGoFuncDecl(fset, d, relPath, pkgName, fileEntityID, res)
		}
	}

	return res
}

func extractGoGenDecl(fset *token.FileSet, d *ast.GenDecl, relPath, pkgName, fileEntityID string, res *ExtractionResult) {
	docText := cleanDoc(d.Doc.Text())

	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			pos := fset.Position(s.Pos())
			endPos := fset.Position(s.End())
			typeName := s.Name.Name
			typeID := fmt.Sprintf("type:%s.%s", pkgName, typeName)

			kind := code.KindType
			metadata := make(map[string]any)

			// Determine if Struct or Interface
			switch t := s.Type.(type) {
			case *ast.StructType:
				kind = code.KindStruct
				if t.Fields != nil {
					fields := make([]map[string]string, 0, len(t.Fields.List))
					for _, f := range t.Fields.List {
						fieldType := typeString(f.Type)
						fieldTag := ""
						if f.Tag != nil {
							fieldTag = strings.Trim(f.Tag.Value, "`")
						}
						if len(f.Names) == 0 {
							// Embedded struct
							fields = append(fields, map[string]string{
								"name":     fieldType,
								"type":     fieldType,
								"tag":      fieldTag,
								"embedded": "true",
							})
						} else {
							for _, name := range f.Names {
								fields = append(fields, map[string]string{
									"name": name.Name,
									"type": fieldType,
									"tag":  fieldTag,
								})
							}
						}
					}
					metadata["fields"] = fields
				}
			case *ast.InterfaceType:
				kind = code.KindInterface
				if t.Methods != nil {
					methods := make([]string, 0, len(t.Methods.List))
					for _, m := range t.Methods.List {
						for _, name := range m.Names {
							methods = append(methods, name.Name)
						}
					}
					metadata["methods"] = methods
				}
			}

			docSummary := docText
			if docSummary == "" && s.Doc != nil {
				docSummary = cleanDoc(s.Doc.Text())
			}

			visibility := goVisibility(typeName)

			res.Entities = append(res.Entities, CodeEntity{
				ID:         typeID,
				Name:       typeName,
				Kind:       kind,
				File:       relPath,
				Line:       pos.Line,
				EndLine:    endPos.Line,
				StartCol:   pos.Column,
				EndCol:     endPos.Column,
				Package:    pkgName,
				ParentID:   fileEntityID,
				Visibility: visibility,
				DocSummary: docSummary,
				Signature:  fmt.Sprintf("type %s %s", typeName, kind),
				Metadata:   metadata,
			})

			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     typeID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})

			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     typeID,
				Relation:   code.RelationContains,
				Confidence: code.ConfidenceExtracted,
			})

			if visibility == code.VisibilityPublic {
				res.Exports = append(res.Exports, ExportFact{
					SourceFile:   relPath,
					ExportedName: typeName,
					SymbolID:     typeID,
					Line:         pos.Line,
				})
			}
		}
	}
}

func extractGoFuncDecl(fset *token.FileSet, d *ast.FuncDecl, relPath, pkgName, fileEntityID string, res *ExtractionResult) {
	pos := fset.Position(d.Pos())
	endPos := fset.Position(d.End())
	funcName := d.Name.Name
	kind := code.KindFunc
	receiver := ""
	receiverType := ""
	parentID := fileEntityID

	if d.Recv != nil && len(d.Recv.List) > 0 {
		kind = code.KindMethod
		receiverType = typeString(d.Recv.List[0].Type)
		receiver = strings.TrimPrefix(receiverType, "*")
		parentID = fmt.Sprintf("type:%s.%s", pkgName, receiver)
	}

	funcID := fmt.Sprintf("func:%s.%s", pkgName, funcName)
	if receiver != "" {
		funcID = fmt.Sprintf("method:%s.%s.%s", pkgName, receiver, funcName)
	}

	// Extract typed parameters
	var params []code.Parameter
	if d.Type.Params != nil {
		for _, p := range d.Type.Params.List {
			pType := typeString(p.Type)
			if len(p.Names) == 0 {
				params = append(params, code.Parameter{Name: "_", Type: pType})
			} else {
				for _, name := range p.Names {
					params = append(params, code.Parameter{Name: name.Name, Type: pType})
				}
			}
		}
	}

	// Extract return types
	var returnTypes []string
	if d.Type.Results != nil {
		for _, r := range d.Type.Results.List {
			returnTypes = append(returnTypes, typeString(r.Type))
		}
	}
	returnType := strings.Join(returnTypes, ", ")
	if len(returnTypes) > 1 {
		returnType = "(" + returnType + ")"
	}

	// Calculate cyclomatic complexity
	complexity := computeGoComplexity(d.Body)

	// Full signature
	var paramStrs []string
	for _, p := range params {
		paramStrs = append(paramStrs, fmt.Sprintf("%s %s", p.Name, p.Type))
	}
	sig := fmt.Sprintf("func %s(%s)", funcName, strings.Join(paramStrs, ", "))
	if receiver != "" {
		sig = fmt.Sprintf("func (%s) %s(%s)", receiverType, funcName, strings.Join(paramStrs, ", "))
	}
	if returnType != "" {
		sig += " " + returnType
	}

	visibility := goVisibility(funcName)
	docText := cleanDoc(d.Doc.Text())

	res.Entities = append(res.Entities, CodeEntity{
		ID:         funcID,
		Name:       funcName,
		Kind:       kind,
		File:       relPath,
		Line:       pos.Line,
		EndLine:    endPos.Line,
		StartCol:   pos.Column,
		EndCol:     endPos.Column,
		Package:    pkgName,
		ParentID:   parentID,
		Visibility: visibility,
		Signature:  sig,
		DocSummary: docText,
		Parameters: params,
		ReturnType: returnType,
		Complexity: complexity,
		Metadata: map[string]any{
			"receiver": receiver,
		},
	})

	res.Relationships = append(res.Relationships, CodeRelationship{
		Source:     fileEntityID,
		Target:     funcID,
		Relation:   code.RelationDefines,
		Confidence: code.ConfidenceExtracted,
	})

	if receiver != "" {
		res.Relationships = append(res.Relationships, CodeRelationship{
			Source:     parentID,
			Target:     funcID,
			Relation:   code.RelationContains,
			Confidence: code.ConfidenceExtracted,
		})
		res.Relationships = append(res.Relationships, CodeRelationship{
			Source:     funcID,
			Target:     parentID,
			Relation:   code.RelationImplements,
			Confidence: code.ConfidenceExtracted,
		})
	}

	if visibility == code.VisibilityPublic {
		res.Exports = append(res.Exports, ExportFact{
			SourceFile:   relPath,
			ExportedName: funcName,
			SymbolID:     funcID,
			Line:         pos.Line,
		})
	}

	// Inspect calls and instantiation in function body
	if d.Body != nil {
		ast.Inspect(d.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					// Direct function call in same package or built-in
					targetID := fmt.Sprintf("func:%s.%s", pkgName, fun.Name)
					res.Relationships = append(res.Relationships, CodeRelationship{
						Source:     funcID,
						Target:     targetID,
						Relation:   code.RelationCalls,
						Confidence: code.ConfidenceExtracted,
						Reasoning:  fmt.Sprintf("Direct call to %s()", fun.Name),
					})
				case *ast.SelectorExpr:
					// Selector call: pkg.Func() or obj.Method()
					if pkgIdent, ok := fun.X.(*ast.Ident); ok {
						targetID := fmt.Sprintf("func:%s.%s", pkgIdent.Name, fun.Sel.Name)
						res.Relationships = append(res.Relationships, CodeRelationship{
							Source:     funcID,
							Target:     targetID,
							Relation:   code.RelationCalls,
							Confidence: code.ConfidenceInferred,
							Reasoning:  fmt.Sprintf("Package selector call %s.%s()", pkgIdent.Name, fun.Sel.Name),
						})
					}
				}
			} else if comp, ok := n.(*ast.CompositeLit); ok {
				// Instantiation: &Service{} or Service{}
				typeStr := typeString(comp.Type)
				if typeStr != "" {
					cleanType := strings.TrimPrefix(typeStr, "*")
					targetID := fmt.Sprintf("type:%s.%s", pkgName, cleanType)
					res.Relationships = append(res.Relationships, CodeRelationship{
						Source:     funcID,
						Target:     targetID,
						Relation:   code.RelationInstantiates,
						Confidence: code.ConfidenceExtracted,
						Reasoning:  fmt.Sprintf("Instantiates struct %s", cleanType),
					})
				}
			}
			return true
		})
	}
}

// computeGoComplexity estimates cyclomatic complexity by counting control flow branches.
func computeGoComplexity(body *ast.BlockStmt) int {
	if body == nil {
		return 1
	}
	complexity := 1
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.IfStmt:
			complexity++
		case *ast.ForStmt, *ast.RangeStmt:
			complexity++
		case *ast.CaseClause:
			if len(node.List) > 0 { // non-default case
				complexity += len(node.List)
			}
		case *ast.CommClause:
			if node.Comm != nil {
				complexity++
			}
		case *ast.BinaryExpr:
			if node.Op == token.LAND || node.Op == token.LOR {
				complexity++
			}
		}
		return true
	})
	return complexity
}

func goVisibility(name string) string {
	if len(name) == 0 {
		return code.VisibilityPrivate
	}
	firstRune := []rune(name)[0]
	if unicode.IsUpper(firstRune) {
		return code.VisibilityPublic
	}
	return code.VisibilityPrivate
}

func cleanDoc(raw string) string {
	lines := strings.Split(raw, "\n")
	var cleaned []string
	for _, l := range lines {
		t := strings.TrimSpace(strings.TrimPrefix(l, "//"))
		if t != "" {
			cleaned = append(cleaned, t)
		}
	}
	return strings.Join(cleaned, " ")
}

func typeString(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + typeString(t.Elt)
		}
		return "[...]" + typeString(t.Elt)
	case *ast.MapType:
		return fmt.Sprintf("map[%s]%s", typeString(t.Key), typeString(t.Value))
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.Ellipsis:
		return "..." + typeString(t.Elt)
	case *ast.FuncType:
		return "func(...)"
	default:
		return "any"
	}
}
