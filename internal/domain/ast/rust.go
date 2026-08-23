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
	rsUseRe        = regexp.MustCompile(`^\s*(?:pub(?:\([^)]+\))?\s+)?use\s+([a-zA-Z0-9_:]+)`)
	rsModRe        = regexp.MustCompile(`^\s*(?:pub(?:\([^)]+\))?\s+)?mod\s+([a-zA-Z0-9_]+)\s*;`)
	rsStructRe     = regexp.MustCompile(`^\s*(?:pub(?:\([^)]+\))?\s+)?struct\s+([a-zA-Z0-9_]+)`)
	rsEnumRe       = regexp.MustCompile(`^\s*(?:pub(?:\([^)]+\))?\s+)?enum\s+([a-zA-Z0-9_]+)`)
	rsTraitRe      = regexp.MustCompile(`^\s*(?:pub(?:\([^)]+\))?\s+)?trait\s+([a-zA-Z0-9_]+)`)
	rsImplTraitRe  = regexp.MustCompile(`^\s*impl(?:<[^>]+>)?\s+([a-zA-Z0-9_:]+)\s+for\s+([a-zA-Z0-9_:]+)`)
	rsImplStructRe = regexp.MustCompile(`^\s*impl(?:<[^>]+>)?\s+([a-zA-Z0-9_:]+)`)
	rsFnRe         = regexp.MustCompile(`^\s*(?:pub(?:\([^)]+\))?\s+)?(?:async\s+)?(?:unsafe\s+)?(?:extern\s+(?:"[^"]+"\s+)?)?fn\s+([a-zA-Z0-9_]+)\s*(?:<[^>]+>)?\s*\(([^)]*)\)(?:\s*->\s*([^{]+))?`)
)

func extractRustFile(fullPath, relPath string) *ExtractionResult {
	res := &ExtractionResult{
		Entities:      make([]CodeEntity, 0),
		Relationships: make([]CodeRelationship, 0),
		Imports:       make([]ImportFact, 0),
		Exports:       make([]ExportFact, 0),
	}

	file, err := os.Open(fullPath)
	if err != nil {
		return res
	}
	defer func() { _ = file.Close() }()

	fileEntityID := fmt.Sprintf("module:%s", relPath)
	moduleName := strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))

	res.Entities = append(res.Entities, CodeEntity{
		ID:         fileEntityID,
		Name:       filepath.Base(relPath),
		Kind:       code.KindModule,
		File:       relPath,
		Line:       1,
		Package:    moduleName,
		Visibility: code.VisibilityPublic,
	})

	scanner := bufio.NewScanner(file)
	lineNum := 0

	var docComments []string
	var currentParentID string
	var currentParentName string

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Rust doc comment `///`
		if strings.HasPrefix(trimmed, "///") {
			docComments = append(docComments, strings.TrimSpace(strings.TrimPrefix(trimmed, "///")))
			continue
		}

		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		docSummary := strings.TrimSpace(strings.Join(docComments, " "))

		// 1. Use / Imports
		if m := rsUseRe.FindStringSubmatch(trimmed); len(m) > 1 {
			importPath := m[1]
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
				LocalName:    filepath.Base(strings.ReplaceAll(importPath, "::", "/")),
				ImportedName: "*",
				Line:         lineNum,
			})
			docComments = nil
			continue
		}

		// 2. Mod declaration
		if m := rsModRe.FindStringSubmatch(trimmed); len(m) > 1 {
			modName := m[1]
			modID := fmt.Sprintf("module:%s:%s", relPath, modName)
			res.Entities = append(res.Entities, CodeEntity{
				ID:         modID,
				Name:       modName,
				Kind:       code.KindModule,
				File:       relPath,
				Line:       lineNum,
				Package:    moduleName,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
				DocSummary: docSummary,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     modID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
			docComments = nil
			continue
		}

		// 3. Struct
		if m := rsStructRe.FindStringSubmatch(trimmed); len(m) > 1 {
			structName := m[1]
			structID := fmt.Sprintf("type:%s:%s", relPath, structName)
			visibility := code.VisibilityPrivate
			if strings.Contains(trimmed, "pub") {
				visibility = code.VisibilityPublic
				res.Exports = append(res.Exports, ExportFact{
					SourceFile:   relPath,
					ExportedName: structName,
					SymbolID:     structID,
					Line:         lineNum,
				})
			}

			res.Entities = append(res.Entities, CodeEntity{
				ID:         structID,
				Name:       structName,
				Kind:       code.KindStruct,
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
				Target:     structID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
			docComments = nil
			continue
		}

		// 4. Enum
		if m := rsEnumRe.FindStringSubmatch(trimmed); len(m) > 1 {
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
				Signature:  trimmed,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     enumID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
			docComments = nil
			continue
		}

		// 5. Trait
		if m := rsTraitRe.FindStringSubmatch(trimmed); len(m) > 1 {
			traitName := m[1]
			traitID := fmt.Sprintf("interface:%s:%s", relPath, traitName)
			res.Entities = append(res.Entities, CodeEntity{
				ID:         traitID,
				Name:       traitName,
				Kind:       code.KindInterface,
				File:       relPath,
				Line:       lineNum,
				Package:    moduleName,
				ParentID:   fileEntityID,
				DocSummary: docSummary,
				Signature:  trimmed,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     traitID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
			docComments = nil
			continue
		}

		// 6. Impl Trait for Struct
		if m := rsImplTraitRe.FindStringSubmatch(trimmed); len(m) > 2 {
			traitName := cleanRsType(m[1])
			structName := cleanRsType(m[2])
			structID := fmt.Sprintf("type:%s:%s", relPath, structName)
			traitID := fmt.Sprintf("interface:%s", traitName)

			currentParentID = structID
			currentParentName = structName

			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     structID,
				Target:     traitID,
				Relation:   code.RelationImplements,
				Confidence: code.ConfidenceExtracted,
				Reasoning:  fmt.Sprintf("impl %s for %s", traitName, structName),
			})
			docComments = nil
			continue
		} else if m := rsImplStructRe.FindStringSubmatch(trimmed); len(m) > 1 && !strings.Contains(trimmed, " for ") {
			structName := cleanRsType(m[1])
			structID := fmt.Sprintf("type:%s:%s", relPath, structName)
			currentParentID = structID
			currentParentName = structName
			docComments = nil
			continue
		}

		// 7. Functions & Methods
		if m := rsFnRe.FindStringSubmatch(trimmed); len(m) > 1 {
			fnName := m[1]
			rawParams := m[2]
			returnType := strings.TrimSpace(m[3])

			parentID := fileEntityID
			kind := code.KindFunc
			fnID := fmt.Sprintf("func:%s:%s", relPath, fnName)

			if currentParentID != "" {
				kind = code.KindMethod
				parentID = currentParentID
				fnID = fmt.Sprintf("method:%s:%s.%s", relPath, currentParentName, fnName)
			}

			visibility := code.VisibilityPrivate
			if strings.Contains(trimmed, "pub") {
				visibility = code.VisibilityPublic
				if currentParentID == "" {
					res.Exports = append(res.Exports, ExportFact{
						SourceFile:   relPath,
						ExportedName: fnName,
						SymbolID:     fnID,
						Line:         lineNum,
					})
				}
			}

			params := parseRsParams(rawParams)
			res.Entities = append(res.Entities, CodeEntity{
				ID:         fnID,
				Name:       fnName,
				Kind:       kind,
				File:       relPath,
				Line:       lineNum,
				Package:    moduleName,
				ParentID:   parentID,
				Visibility: visibility,
				DocSummary: docSummary,
				Parameters: params,
				ReturnType: returnType,
				Signature:  trimmed,
			})

			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     parentID,
				Target:     fnID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})

			if currentParentID != "" {
				res.Relationships = append(res.Relationships, CodeRelationship{
					Source:     currentParentID,
					Target:     fnID,
					Relation:   code.RelationContains,
					Confidence: code.ConfidenceExtracted,
				})
			}

			docComments = nil
			continue
		}

		if trimmed == "}" {
			currentParentID = ""
			currentParentName = ""
		}

		if trimmed == "" {
			docComments = nil
		}
	}

	return res
}

func parseRsParams(raw string) []code.Parameter {
	var params []code.Parameter
	if strings.TrimSpace(raw) == "" {
		return params
	}
	parts := strings.Split(raw, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "&self" || p == "&mut self" || p == "self" {
			continue
		}
		nameType := strings.Split(p, ":")
		name := strings.TrimSpace(nameType[0])
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

func cleanRsType(raw string) string {
	raw = strings.TrimSpace(raw)
	parts := strings.Split(raw, "::")
	return parts[len(parts)-1]
}
