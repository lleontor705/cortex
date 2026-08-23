package ast

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

var (
	// TS/JS Imports
	tsImportNamedRe    = regexp.MustCompile(`(?m)import\s+(?:type\s+)?\{([^}]+)\}\s+from\s+['"]([^'"]+)['"]`)
	tsImportDefaultRe  = regexp.MustCompile(`(?m)import\s+([a-zA-Z0-9_$]+)\s+from\s+['"]([^'"]+)['"]`)
	tsImportStarRe     = regexp.MustCompile(`(?m)import\s+\*\s+as\s+([a-zA-Z0-9_$]+)\s+from\s+['"]([^'"]+)['"]`)

	// TS/JS Classes & Interfaces
	tsClassRe     = regexp.MustCompile(`(?m)(?:export\s+)?(?:abstract\s+)?class\s+([a-zA-Z0-9_$]+)(?:\s+extends\s+([a-zA-Z0-9_$.]+))?(?:\s+implements\s+([a-zA-Z0-9_$,\s]+))?`)
	tsInterfaceRe = regexp.MustCompile(`(?m)(?:export\s+)?interface\s+([a-zA-Z0-9_$]+)(?:\s+extends\s+([a-zA-Z0-9_$,\s]+))?`)
	tsTypeRe      = regexp.MustCompile(`(?m)(?:export\s+)?type\s+([a-zA-Z0-9_$]+)(?:<[^>]+>)?\s*=`)
	tsEnumRe      = regexp.MustCompile(`(?m)(?:export\s+)?(?:const\s+)?enum\s+([a-zA-Z0-9_$]+)`)

	// TS/JS Functions & Methods
	tsFuncDeclRe  = regexp.MustCompile(`(?m)(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*([a-zA-Z0-9_$]*)\s*\(([^)]*)\)(?:\s*:\s*([^{]+))?`)
	tsArrowFnRe   = regexp.MustCompile(`(?m)(?:export\s+)?(?:const|let|var)\s+([a-zA-Z0-9_$]+)\s*(?::\s*[^=]+)?\s*=\s*(?:async\s*)?\(([^)]*)\)(?:\s*:\s*([a-zA-Z0-9_$<>[\]\s|&]+))?\s*=>`)
	tsMethodRe    = regexp.MustCompile(`(?m)^\s*(?:public|private|protected|static|async|override|\s)*(?:get|set\s+)?([a-zA-Z0-9_$]+)\s*\(([^)]*)\)(?:\s*:\s*([^{]+))?\s*[{;]`)

	// Call sites
	tsCallRe      = regexp.MustCompile(`\b([a-zA-Z0-9_$]+)\s*\(`)
)

var tsKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"return": true, "function": true, "export": true, "import": true, "const": true,
	"let": true, "var": true, "class": true, "type": true, "interface": true,
	"async": true, "await": true, "new": true, "super": true, "this": true,
	"typeof": true, "instanceof": true, "void": true, "delete": true, "throw": true,
	"require": true, "console": true, "Math": true, "JSON": true, "Object": true,
	"Array": true, "Promise": true, "String": true, "Number": true, "Boolean": true,
}

func extractTSJSFile(fullPath, relPath string) *ExtractionResult {
	res := &ExtractionResult{
		Entities:      make([]CodeEntity, 0),
		Relationships: make([]CodeRelationship, 0),
		Imports:       make([]ImportFact, 0),
		Exports:       make([]ExportFact, 0),
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return res
	}

	src := string(content)
	fileEntityID := fmt.Sprintf("module:%s", relPath)
	moduleName := strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))

	lines := strings.Split(src, "\n")
	res.Entities = append(res.Entities, CodeEntity{
		ID:         fileEntityID,
		Name:       filepath.Base(relPath),
		Kind:       code.KindModule,
		File:       relPath,
		Line:       1,
		EndLine:    len(lines),
		Package:    moduleName,
		Visibility: code.VisibilityPublic,
	})

	// Pass 1: Parse Imports
	extractTSImports(src, relPath, fileEntityID, res)

	// Pass 2: Parse Declarations line-by-line with JSDoc lookback and call scope tracking
	scanner := bufio.NewScanner(strings.NewReader(src))
	lineNum := 0
	var jsdocBuffer []string
	inJSDoc := false

	var currentClassID string
	var currentClassName string
	var currentScopeID = fileEntityID

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// JSDoc tracking
		if strings.HasPrefix(trimmed, "/**") {
			inJSDoc = true
			jsdocBuffer = []string{cleanJSDocLine(trimmed)}
			if strings.HasSuffix(trimmed, "*/") {
				inJSDoc = false
			}
			continue
		}
		if inJSDoc {
			jsdocBuffer = append(jsdocBuffer, cleanJSDocLine(trimmed))
			if strings.HasSuffix(trimmed, "*/") {
				inJSDoc = false
			}
			continue
		}

		// Skip line comments
		if strings.HasPrefix(trimmed, "//") {
			continue
		}

		docSummary := strings.TrimSpace(strings.Join(jsdocBuffer, " "))

		// 1. Classes
		if m := tsClassRe.FindStringSubmatch(line); len(m) > 1 {
			className := m[1]
			extendsClass := m[2]
			implementsIfaces := m[3]

			classID := fmt.Sprintf("class:%s:%s", relPath, className)
			currentClassID = classID
			currentClassName = className
			currentScopeID = classID

			visibility := code.VisibilityPrivate
			if strings.Contains(line, "export") {
				visibility = code.VisibilityPublic
				res.Exports = append(res.Exports, ExportFact{
					SourceFile:   relPath,
					ExportedName: className,
					SymbolID:     classID,
					Line:         lineNum,
				})
			}

			res.Entities = append(res.Entities, CodeEntity{
				ID:         classID,
				Name:       className,
				Kind:       code.KindClass,
				File:       relPath,
				Line:       lineNum,
				Package:    moduleName,
				ParentID:   fileEntityID,
				Visibility: visibility,
				DocSummary: docSummary,
				Signature:  strings.TrimSpace(line),
			})

			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   code.RelationContains,
				Confidence: code.ConfidenceExtracted,
			})

			if extendsClass != "" {
				res.Relationships = append(res.Relationships, CodeRelationship{
					Source:     classID,
					Target:     fmt.Sprintf("class:%s", extendsClass),
					Relation:   code.RelationExtends,
					Confidence: code.ConfidenceExtracted,
					Reasoning:  fmt.Sprintf("Extends base class %s", extendsClass),
				})
			}

			if implementsIfaces != "" {
				for _, iface := range strings.Split(implementsIfaces, ",") {
					ifaceName := strings.TrimSpace(iface)
					if ifaceName != "" {
						res.Relationships = append(res.Relationships, CodeRelationship{
							Source:     classID,
							Target:     fmt.Sprintf("interface:%s", ifaceName),
							Relation:   code.RelationImplements,
							Confidence: code.ConfidenceExtracted,
							Reasoning:  fmt.Sprintf("Implements interface %s", ifaceName),
						})
					}
				}
			}

			jsdocBuffer = nil
			continue
		}

		// 2. Interfaces
		if m := tsInterfaceRe.FindStringSubmatch(line); len(m) > 1 {
			ifaceName := m[1]
			extendsIface := m[2]
			ifaceID := fmt.Sprintf("interface:%s:%s", relPath, ifaceName)

			visibility := code.VisibilityPrivate
			if strings.Contains(line, "export") {
				visibility = code.VisibilityPublic
				res.Exports = append(res.Exports, ExportFact{
					SourceFile:   relPath,
					ExportedName: ifaceName,
					SymbolID:     ifaceID,
					Line:         lineNum,
				})
			}

			res.Entities = append(res.Entities, CodeEntity{
				ID:         ifaceID,
				Name:       ifaceName,
				Kind:       code.KindInterface,
				File:       relPath,
				Line:       lineNum,
				Package:    moduleName,
				ParentID:   fileEntityID,
				Visibility: visibility,
				DocSummary: docSummary,
				Signature:  strings.TrimSpace(line),
			})

			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     ifaceID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})

			if extendsIface != "" {
				res.Relationships = append(res.Relationships, CodeRelationship{
					Source:     ifaceID,
					Target:     fmt.Sprintf("interface:%s", strings.TrimSpace(extendsIface)),
					Relation:   code.RelationExtends,
					Confidence: code.ConfidenceExtracted,
				})
			}

			jsdocBuffer = nil
			continue
		}

		// 3. Type Aliases
		if m := tsTypeRe.FindStringSubmatch(line); len(m) > 1 {
			typeName := m[1]
			typeID := fmt.Sprintf("type:%s:%s", relPath, typeName)

			visibility := code.VisibilityPrivate
			if strings.Contains(line, "export") {
				visibility = code.VisibilityPublic
			}

			res.Entities = append(res.Entities, CodeEntity{
				ID:         typeID,
				Name:       typeName,
				Kind:       code.KindType,
				File:       relPath,
				Line:       lineNum,
				Package:    moduleName,
				ParentID:   fileEntityID,
				Visibility: visibility,
				DocSummary: docSummary,
				Signature:  strings.TrimSpace(line),
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     typeID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
			jsdocBuffer = nil
			continue
		}

		// 4. Enums
		if m := tsEnumRe.FindStringSubmatch(line); len(m) > 1 {
			enumName := m[1]
			enumID := fmt.Sprintf("enum:%s:%s", relPath, enumName)

			res.Entities = append(res.Entities, CodeEntity{
				ID:         enumID,
				Name:       enumName,
				Kind:       code.KindEnum,
				File:       relPath,
				Line:       lineNum,
				Package:    moduleName,
				ParentID:   fileEntityID,
				DocSummary: docSummary,
				Signature:  strings.TrimSpace(line),
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     enumID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
			jsdocBuffer = nil
			continue
		}

		// 5. Functions (Declaration)
		if m := tsFuncDeclRe.FindStringSubmatch(line); len(m) > 1 {
			fnName := m[1]
			if fnName == "" {
				fnName = "default"
			}
			rawParams := m[2]
			returnType := strings.TrimSpace(m[3])

			fnID := fmt.Sprintf("func:%s:%s", relPath, fnName)
			currentScopeID = fnID

			visibility := code.VisibilityPrivate
			if strings.Contains(line, "export") {
				visibility = code.VisibilityPublic
				res.Exports = append(res.Exports, ExportFact{
					SourceFile:   relPath,
					ExportedName: fnName,
					SymbolID:     fnID,
					Line:         lineNum,
				})
			}

			params := parseTSParams(rawParams)
			res.Entities = append(res.Entities, CodeEntity{
				ID:         fnID,
				Name:       fnName,
				Kind:       code.KindFunc,
				File:       relPath,
				Line:       lineNum,
				Package:    moduleName,
				ParentID:   fileEntityID,
				Visibility: visibility,
				DocSummary: docSummary,
				Parameters: params,
				ReturnType: returnType,
				Signature:  strings.TrimSpace(line),
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     fnID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
			jsdocBuffer = nil
			continue
		}

		// 6. Arrow Functions
		if m := tsArrowFnRe.FindStringSubmatch(line); len(m) > 1 {
			fnName := m[1]
			rawParams := m[2]
			returnType := strings.TrimSpace(m[3])

			fnID := fmt.Sprintf("func:%s:%s", relPath, fnName)
			currentScopeID = fnID

			visibility := code.VisibilityPrivate
			if strings.Contains(line, "export") {
				visibility = code.VisibilityPublic
				res.Exports = append(res.Exports, ExportFact{
					SourceFile:   relPath,
					ExportedName: fnName,
					SymbolID:     fnID,
					Line:         lineNum,
				})
			}

			params := parseTSParams(rawParams)
			res.Entities = append(res.Entities, CodeEntity{
				ID:         fnID,
				Name:       fnName,
				Kind:       code.KindFunc,
				File:       relPath,
				Line:       lineNum,
				Package:    moduleName,
				ParentID:   fileEntityID,
				Visibility: visibility,
				DocSummary: docSummary,
				Parameters: params,
				ReturnType: returnType,
				Signature:  strings.TrimSpace(line),
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     fnID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
			jsdocBuffer = nil
			continue
		}

		// 7. Class Methods (if inside a class)
		if currentClassID != "" && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")) {
			if m := tsMethodRe.FindStringSubmatch(line); len(m) > 1 {
				methodName := m[1]
				if methodName != "if" && methodName != "for" && methodName != "switch" && methodName != "catch" {
					rawParams := m[2]
					returnType := strings.TrimSpace(m[3])
					methodID := fmt.Sprintf("method:%s:%s.%s", relPath, currentClassName, methodName)
					currentScopeID = methodID

					visibility := code.VisibilityPublic
					if strings.Contains(line, "private") || strings.HasPrefix(methodName, "#") || strings.HasPrefix(methodName, "_") {
						visibility = code.VisibilityPrivate
					} else if strings.Contains(line, "protected") {
						visibility = code.VisibilityProtected
					}

					params := parseTSParams(rawParams)
					res.Entities = append(res.Entities, CodeEntity{
						ID:         methodID,
						Name:       methodName,
						Kind:       code.KindMethod,
						File:       relPath,
						Line:       lineNum,
						Package:    moduleName,
						ParentID:   currentClassID,
						Visibility: visibility,
						DocSummary: docSummary,
						Parameters: params,
						ReturnType: returnType,
						Signature:  strings.TrimSpace(line),
					})

					res.Relationships = append(res.Relationships, CodeRelationship{
						Source:     currentClassID,
						Target:     methodID,
						Relation:   code.RelationContains,
						Confidence: code.ConfidenceExtracted,
					})
					res.Relationships = append(res.Relationships, CodeRelationship{
						Source:     methodID,
						Target:     currentClassID,
						Relation:   code.RelationImplements,
						Confidence: code.ConfidenceExtracted,
					})

					jsdocBuffer = nil
					continue
				}
			}
		}

		// 8. Direct Calls inside current function/method scope
		for _, cm := range tsCallRe.FindAllStringSubmatch(line, -1) {
			fnName := cm[1]
			if !tsKeywords[fnName] && !strings.Contains(line, "function "+fnName) {
				targetID := fmt.Sprintf("func:%s", fnName)
				res.Relationships = append(res.Relationships, CodeRelationship{
					Source:     currentScopeID,
					Target:     targetID,
					Relation:   code.RelationCalls,
					Confidence: code.ConfidenceInferred,
					Reasoning:  fmt.Sprintf("Direct call to %s()", fnName),
				})
			}
		}

		// Reset class context on closing top-level brace
		if trimmed == "}" {
			currentClassID = ""
			currentClassName = ""
			currentScopeID = fileEntityID
		}

		// Clear unused JSDoc after blank lines
		if trimmed == "" {
			jsdocBuffer = nil
		}
	}

	return res
}

func extractTSImports(src, relPath, fileEntityID string, res *ExtractionResult) {
	// Named imports: import { a, b as c } from './x'
	for _, m := range tsImportNamedRe.FindAllStringSubmatch(src, -1) {
		importPath := m[2]
		rawImports := m[1]
		targetID := fmt.Sprintf("pkg:%s", importPath)

		res.Relationships = append(res.Relationships, CodeRelationship{
			Source:     fileEntityID,
			Target:     targetID,
			Relation:   code.RelationImports,
			Confidence: code.ConfidenceExtracted,
			Reasoning:  fmt.Sprintf("Import from %s", importPath),
		})

		for _, item := range strings.Split(rawImports, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			parts := strings.Split(item, " as ")
			importedName := strings.TrimSpace(parts[0])
			localName := importedName
			if len(parts) > 1 {
				localName = strings.TrimSpace(parts[1])
			}

			res.Imports = append(res.Imports, ImportFact{
				SourceFile:   relPath,
				ImportPath:   importPath,
				LocalName:    localName,
				ImportedName: importedName,
			})
		}
	}

	// Default imports: import Foo from './foo'
	for _, m := range tsImportDefaultRe.FindAllStringSubmatch(src, -1) {
		localName := m[1]
		importPath := m[2]
		if localName != "type" && localName != "*" {
			targetID := fmt.Sprintf("pkg:%s", importPath)
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   code.RelationImports,
				Confidence: code.ConfidenceExtracted,
			})
			res.Imports = append(res.Imports, ImportFact{
				SourceFile:   relPath,
				ImportPath:   importPath,
				LocalName:    localName,
				ImportedName: "default",
			})
		}
	}

	// Star imports: import * as Foo from './foo'
	for _, m := range tsImportStarRe.FindAllStringSubmatch(src, -1) {
		localName := m[1]
		importPath := m[2]
		targetID := fmt.Sprintf("pkg:%s", importPath)
		res.Relationships = append(res.Relationships, CodeRelationship{
			Source:     fileEntityID,
			Target:     targetID,
			Relation:   code.RelationImports,
			Confidence: code.ConfidenceExtracted,
		})
		res.Imports = append(res.Imports, ImportFact{
			SourceFile:   relPath,
			ImportPath:   importPath,
			LocalName:    localName,
			ImportedName: "*",
			IsWildcard:   true,
		})
	}
}

func parseTSParams(raw string) []code.Parameter {
	var params []code.Parameter
	if strings.TrimSpace(raw) == "" {
		return params
	}
	parts := strings.Split(raw, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		nameType := strings.Split(p, ":")
		name := strings.TrimSpace(nameType[0])
		name = strings.TrimPrefix(name, "...")
		name = strings.TrimSuffix(name, "?")
		pType := "any"
		if len(nameType) > 1 {
			pType = strings.TrimSpace(nameType[1])
		}
		params = append(params, code.Parameter{
			Name: name,
			Type: pType,
		})
	}
	return params
}

func cleanJSDocLine(raw string) string {
	raw = strings.TrimPrefix(raw, "/**")
	raw = strings.TrimSuffix(raw, "*/")
	raw = strings.TrimPrefix(raw, "*")
	return strings.TrimSpace(raw)
}
