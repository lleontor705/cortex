package ast

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

var (
	pyClassDeclRe  = regexp.MustCompile(`^class\s+([a-zA-Z0-9_]+)(?:\(([^)]*)\))?:`)
	pyFuncDeclRe   = regexp.MustCompile(`^(?:async\s+)?def\s+([a-zA-Z0-9_]+)\s*\(([^)]*)\)(?:\s*->\s*([^:]+))?:`)
	pyMethodDeclRe = regexp.MustCompile(`^\s+(?:async\s+)?def\s+([a-zA-Z0-9_]+)\s*\(([^)]*)\)(?:\s*->\s*([^:]+))?:`)
	pyDecoratorRe  = regexp.MustCompile(`^\s*@([a-zA-Z0-9_.]+(?:\([^)]*\))?)`)
	pyFromImportRe = regexp.MustCompile(`^from\s+([a-zA-Z0-9_.]+)\s+import\s+(.+)`)
	pyImportRe     = regexp.MustCompile(`^import\s+(.+)`)
	pyCallRe       = regexp.MustCompile(`\b([a-zA-Z0-9_]+)\s*\(`)
	pyMemberCallRe = regexp.MustCompile(`\b([a-zA-Z0-9_]+)\.([a-zA-Z0-9_]+)\s*\(`)
)

func extractPythonFile(fullPath, relPath string) *ExtractionResult {
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

	fileEntityID := fmt.Sprintf("module:%s", relPath)
	moduleName := strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))

	lines := strings.Split(string(content), "\n")
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

	var currentClassID string
	var currentClassName string
	var currentScopeID = fileEntityID
	var pendingDecorators []string

	for i := 0; i < len(lines); i++ {
		lineNum := i + 1
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}

		// 1. Decorators
		if m := pyDecoratorRe.FindStringSubmatch(trimmed); len(m) > 1 {
			pendingDecorators = append(pendingDecorators, m[1])
			continue
		}

		// 2. Imports
		if m := pyFromImportRe.FindStringSubmatch(trimmed); len(m) > 2 {
			pkgPath := m[1]
			importedSymbols := m[2]
			targetID := fmt.Sprintf("pkg:%s", pkgPath)

			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   code.RelationImports,
				Confidence: code.ConfidenceExtracted,
			})

			for _, sym := range strings.Split(importedSymbols, ",") {
				sym = strings.TrimSpace(sym)
				parts := strings.Split(sym, " as ")
				impName := strings.TrimSpace(parts[0])
				locName := impName
				if len(parts) > 1 {
					locName = strings.TrimSpace(parts[1])
				}
				res.Imports = append(res.Imports, ImportFact{
					SourceFile:   relPath,
					ImportPath:   pkgPath,
					LocalName:    locName,
					ImportedName: impName,
					Line:         lineNum,
				})
			}
			pendingDecorators = nil
			continue
		}

		if m := pyImportRe.FindStringSubmatch(trimmed); len(m) > 1 {
			rawImports := m[1]
			for _, item := range strings.Split(rawImports, ",") {
				item = strings.TrimSpace(item)
				parts := strings.Split(item, " as ")
				impName := strings.TrimSpace(parts[0])
				locName := impName
				if len(parts) > 1 {
					locName = strings.TrimSpace(parts[1])
				}
				targetID := fmt.Sprintf("pkg:%s", impName)
				res.Relationships = append(res.Relationships, CodeRelationship{
					Source:     fileEntityID,
					Target:     targetID,
					Relation:   code.RelationImports,
					Confidence: code.ConfidenceExtracted,
				})
				res.Imports = append(res.Imports, ImportFact{
					SourceFile:   relPath,
					ImportPath:   impName,
					LocalName:    locName,
					ImportedName: "*",
					Line:         lineNum,
				})
			}
			pendingDecorators = nil
			continue
		}

		// 3. Classes
		if m := pyClassDeclRe.FindStringSubmatch(trimmed); len(m) > 1 {
			className := m[1]
			baseClasses := m[2]
			classID := fmt.Sprintf("class:%s:%s", relPath, className)
			currentClassID = classID
			currentClassName = className
			currentScopeID = classID

			docSummary, nextI := scanPythonDocstring(lines, i+1)
			if nextI > i {
				i = nextI - 1
			}

			visibility := code.VisibilityPublic
			if strings.HasPrefix(className, "_") {
				visibility = code.VisibilityPrivate
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
				Signature:  trimmed,
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

			if baseClasses != "" {
				for _, base := range strings.Split(baseClasses, ",") {
					baseName := strings.TrimSpace(base)
					if baseName != "" && baseName != "object" {
						res.Relationships = append(res.Relationships, CodeRelationship{
							Source:     classID,
							Target:     fmt.Sprintf("class:%s", baseName),
							Relation:   code.RelationExtends,
							Confidence: code.ConfidenceExtracted,
							Reasoning:  fmt.Sprintf("Inherits from %s", baseName),
						})
					}
				}
			}

			res.Exports = append(res.Exports, ExportFact{
				SourceFile:   relPath,
				ExportedName: className,
				SymbolID:     classID,
				Line:         lineNum,
			})

			pendingDecorators = nil
			continue
		}

		// 4. Methods (inside class)
		if currentClassID != "" && (strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")) {
			if m := pyMethodDeclRe.FindStringSubmatch(line); len(m) > 1 {
				methodName := m[1]
				rawParams := m[2]
				returnType := strings.TrimSpace(m[3])
				methodID := fmt.Sprintf("method:%s:%s.%s", relPath, currentClassName, methodName)
				currentScopeID = methodID

				docSummary, nextI := scanPythonDocstring(lines, i+1)
				if nextI > i {
					i = nextI - 1
				}

				visibility := code.VisibilityPublic
				if strings.HasPrefix(methodName, "_") && !strings.HasPrefix(methodName, "__") {
					visibility = code.VisibilityPrivate
				}

				params := parsePyParams(rawParams)
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
					Signature:  trimmed,
					Metadata: map[string]any{
						"decorators": pendingDecorators,
					},
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

				pendingDecorators = nil
				continue
			}
		} else if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && trimmed != "" {
			// Out of class scope
			currentClassID = ""
			currentClassName = ""
			currentScopeID = fileEntityID
		}

		// 5. Top-level Functions
		if m := pyFuncDeclRe.FindStringSubmatch(trimmed); len(m) > 1 {
			fnName := m[1]
			rawParams := m[2]
			returnType := strings.TrimSpace(m[3])
			fnID := fmt.Sprintf("func:%s:%s", relPath, fnName)
			currentScopeID = fnID

			docSummary, nextI := scanPythonDocstring(lines, i+1)
			if nextI > i {
				i = nextI - 1
			}

			visibility := code.VisibilityPublic
			if strings.HasPrefix(fnName, "_") {
				visibility = code.VisibilityPrivate
			}

			params := parsePyParams(rawParams)
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
				Signature:  trimmed,
				Metadata: map[string]any{
					"decorators": pendingDecorators,
				},
			})

			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     fnID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})

			res.Exports = append(res.Exports, ExportFact{
				SourceFile:   relPath,
				ExportedName: fnName,
				SymbolID:     fnID,
				Line:         lineNum,
			})

			pendingDecorators = nil
			continue
		}

		// 6. Direct Calls & Member Calls inside current scope
		if m := pyMemberCallRe.FindStringSubmatch(trimmed); len(m) > 2 {
			receiver := m[1]
			method := m[2]
			if receiver != "self" && receiver != "cls" && receiver != "os" && receiver != "sys" && receiver != "math" {
				targetID := fmt.Sprintf("func:%s", method)
				res.Relationships = append(res.Relationships, CodeRelationship{
					Source:     currentScopeID,
					Target:     targetID,
					Relation:   code.RelationCalls,
					Confidence: code.ConfidenceInferred,
					Reasoning:  fmt.Sprintf("Call to %s.%s()", receiver, method),
				})
			}
		} else if m := pyCallRe.FindStringSubmatch(trimmed); len(m) > 1 {
			fnName := m[1]
			if fnName != "if" && fnName != "for" && fnName != "while" && fnName != "return" && fnName != "print" && fnName != "len" && fnName != "range" && fnName != "int" && fnName != "str" && fnName != "bool" && fnName != "list" && fnName != "dict" && fnName != "set" && fnName != "def" && fnName != "class" {
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
	}

	return res
}

func scanPythonDocstring(lines []string, startIdx int) (string, int) {
	if startIdx >= len(lines) {
		return "", startIdx
	}
	trimmed := strings.TrimSpace(lines[startIdx])
	if strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, `'''`) {
		delim := trimmed[:3]
		rest := trimmed[3:]
		if strings.HasSuffix(rest, delim) && len(rest) >= 3 {
			// Single-line docstring
			return strings.TrimSpace(strings.TrimSuffix(rest, delim)), startIdx + 1
		}
		// Multi-line docstring
		var buf []string
		if rest != "" {
			buf = append(buf, rest)
		}
		for j := startIdx + 1; j < len(lines); j++ {
			line := strings.TrimSpace(lines[j])
			if strings.HasSuffix(line, delim) {
				cleanLast := strings.TrimSpace(strings.TrimSuffix(line, delim))
				if cleanLast != "" {
					buf = append(buf, cleanLast)
				}
				return strings.Join(buf, " "), j + 1
			}
			buf = append(buf, line)
		}
		return strings.Join(buf, " "), len(lines)
	}
	return "", startIdx
}

func parsePyParams(raw string) []code.Parameter {
	var params []code.Parameter
	if strings.TrimSpace(raw) == "" {
		return params
	}
	parts := strings.Split(raw, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "self" || p == "cls" {
			continue
		}
		nameType := strings.Split(p, ":")
		name := strings.TrimSpace(nameType[0])
		name = strings.TrimPrefix(name, "*")
		name = strings.TrimPrefix(name, "**")
		nameVal := strings.Split(name, "=")
		name = strings.TrimSpace(nameVal[0])

		pType := "Any"
		if len(nameType) > 1 {
			typeVal := strings.Split(nameType[1], "=")
			pType = strings.TrimSpace(typeVal[0])
		}
		params = append(params, code.Parameter{
			Name: name,
			Type: pType,
		})
	}
	return params
}
