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
	// C / C++ Regexes
	cppIncludeRe   = regexp.MustCompile(`(?m)^\s*#include\s+[<"]([^>"]+)[>"]`)
	cppNamespaceRe = regexp.MustCompile(`(?m)^\s*namespace\s+([a-zA-Z0-9_]+)`)
	cppClassRe     = regexp.MustCompile(`(?m)^\s*class\s+([a-zA-Z0-9_]+)`)
	cppStructRe    = regexp.MustCompile(`(?m)^\s*struct\s+([a-zA-Z0-9_]+)`)
	cppFuncRe      = regexp.MustCompile(`(?m)^\s*(?:virtual|static|inline|explicit|\s)*(?:void|int|bool|char|double|float|[a-zA-Z0-9_:]+[\*&]?)\s+([a-zA-Z0-9_]+)\s*\([^)]*\)\s*(?:const)?\s*(?:override|final)?\s*[{;]`)

	// PHP Regexes
	phpNamespaceRe = regexp.MustCompile(`(?m)^\s*namespace\s+([a-zA-Z0-9_\\]+)\s*;`)
	phpUseRe       = regexp.MustCompile(`(?m)^\s*use\s+([a-zA-Z0-9_\\]+)(?:\s+as\s+[a-zA-Z0-9_]+)?\s*;`)
	phpClassRe     = regexp.MustCompile(`(?m)^\s*(?:abstract|final|\s)*class\s+([a-zA-Z0-9_]+)`)
	phpInterfaceRe = regexp.MustCompile(`(?m)^\s*interface\s+([a-zA-Z0-9_]+)`)
	phpTraitRe     = regexp.MustCompile(`(?m)^\s*trait\s+([a-zA-Z0-9_]+)`)
	phpEnumRe      = regexp.MustCompile(`(?m)^\s*enum\s+([a-zA-Z0-9_]+)`)
	phpFuncRe      = regexp.MustCompile(`(?m)^\s*(?:public|protected|private|static|\s)*function\s+([a-zA-Z0-9_]+)\s*\(`)

	// Ruby Regexes
	rbRequireRe = regexp.MustCompile(`(?m)^\s*(?:require|require_relative)\s+['"]([^'"]+)['"]`)
	rbModuleRe  = regexp.MustCompile(`(?m)^\s*module\s+([a-zA-Z0-9_:]+)`)
	rbClassRe   = regexp.MustCompile(`(?m)^\s*class\s+([a-zA-Z0-9_:]+)(?:\s*<\s*([a-zA-Z0-9_:]+))?`)
	rbDefRe     = regexp.MustCompile(`(?m)^\s*def\s+(?:self\.)?([a-zA-Z0-9_!?=]+)`)

	// Swift Regexes
	swiftImportRe    = regexp.MustCompile(`(?m)^\s*import\s+([a-zA-Z0-9_]+)`)
	swiftClassRe     = regexp.MustCompile(`(?m)^\s*(?:public|private|internal|open|final|\s)*class\s+([a-zA-Z0-9_]+)`)
	swiftStructRe    = regexp.MustCompile(`(?m)^\s*(?:public|private|internal|\s)*struct\s+([a-zA-Z0-9_]+)`)
	swiftProtocolRe  = regexp.MustCompile(`(?m)^\s*(?:public|private|internal|\s)*protocol\s+([a-zA-Z0-9_]+)`)
	swiftEnumRe      = regexp.MustCompile(`(?m)^\s*(?:public|private|internal|\s)*enum\s+([a-zA-Z0-9_]+)`)
	swiftFuncRe      = regexp.MustCompile(`(?m)^\s*(?:public|private|internal|open|override|static|class|mutating|\s)*func\s+([a-zA-Z0-9_]+)\s*(?:<[^>]+>)?\s*\(`)
)

func extractCppFile(fullPath, relPath string) *ExtractionResult {
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
	res.Entities = append(res.Entities, CodeEntity{
		ID:         fileEntityID,
		Name:       filepath.Base(relPath),
		Kind:       code.KindModule,
		File:       relPath,
		Line:       1,
		Visibility: code.VisibilityPublic,
	})

	scanner := bufio.NewScanner(file)
	lineNum := 0
	currentNs := ""

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		if m := cppNamespaceRe.FindStringSubmatch(line); len(m) > 1 {
			currentNs = m[1]
		}

		if m := cppIncludeRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("pkg:%s", m[1])
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   code.RelationImports,
				Confidence: code.ConfidenceExtracted,
			})
			res.Imports = append(res.Imports, ImportFact{
				SourceFile:   relPath,
				ImportPath:   m[1],
				LocalName:    filepath.Base(m[1]),
				ImportedName: "*",
				Line:         lineNum,
			})
		}

		if m := cppClassRe.FindStringSubmatch(line); len(m) > 1 {
			classID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         classID,
				Name:       m[1],
				Kind:       code.KindClass,
				File:       relPath,
				Line:       lineNum,
				Package:    currentNs,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := cppStructRe.FindStringSubmatch(line); len(m) > 1 {
			structID := fmt.Sprintf("struct:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         structID,
				Name:       m[1],
				Kind:       code.KindStruct,
				File:       relPath,
				Line:       lineNum,
				Package:    currentNs,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     structID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := cppFuncRe.FindStringSubmatch(line); len(m) > 1 {
			fnName := m[1]
			if fnName != "if" && fnName != "for" && fnName != "while" && fnName != "switch" && fnName != "catch" {
				funcID := fmt.Sprintf("func:%s:%s", relPath, fnName)
				res.Entities = append(res.Entities, CodeEntity{
					ID:         funcID,
					Name:       fnName,
					Kind:       code.KindFunc,
					File:       relPath,
					Line:       lineNum,
					Package:    currentNs,
					ParentID:   fileEntityID,
					Visibility: code.VisibilityPublic,
				})
				res.Relationships = append(res.Relationships, CodeRelationship{
					Source:     fileEntityID,
					Target:     funcID,
					Relation:   code.RelationDefines,
					Confidence: code.ConfidenceExtracted,
				})
			}
		}
	}

	return res
}

func extractPhpFile(fullPath, relPath string) *ExtractionResult {
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
	res.Entities = append(res.Entities, CodeEntity{
		ID:         fileEntityID,
		Name:       filepath.Base(relPath),
		Kind:       code.KindModule,
		File:       relPath,
		Line:       1,
		Visibility: code.VisibilityPublic,
	})

	scanner := bufio.NewScanner(file)
	lineNum := 0
	currentNs := ""

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		if m := phpNamespaceRe.FindStringSubmatch(line); len(m) > 1 {
			currentNs = m[1]
		}

		if m := phpUseRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("pkg:%s", m[1])
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   code.RelationImports,
				Confidence: code.ConfidenceExtracted,
			})
			res.Imports = append(res.Imports, ImportFact{
				SourceFile:   relPath,
				ImportPath:   m[1],
				LocalName:    filepath.Base(strings.ReplaceAll(m[1], `\`, "/")),
				ImportedName: "*",
				Line:         lineNum,
			})
		}

		if m := phpClassRe.FindStringSubmatch(line); len(m) > 1 {
			classID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         classID,
				Name:       m[1],
				Kind:       code.KindClass,
				File:       relPath,
				Line:       lineNum,
				Package:    currentNs,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := phpInterfaceRe.FindStringSubmatch(line); len(m) > 1 {
			ifID := fmt.Sprintf("interface:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         ifID,
				Name:       m[1],
				Kind:       code.KindInterface,
				File:       relPath,
				Line:       lineNum,
				Package:    currentNs,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     ifID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := phpTraitRe.FindStringSubmatch(line); len(m) > 1 {
			traitID := fmt.Sprintf("interface:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         traitID,
				Name:       m[1],
				Kind:       code.KindInterface,
				File:       relPath,
				Line:       lineNum,
				Package:    currentNs,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     traitID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := phpEnumRe.FindStringSubmatch(line); len(m) > 1 {
			enumID := fmt.Sprintf("enum:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         enumID,
				Name:       m[1],
				Kind:       code.KindEnum,
				File:       relPath,
				Line:       lineNum,
				Package:    currentNs,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     enumID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := phpFuncRe.FindStringSubmatch(line); len(m) > 1 {
			funcID := fmt.Sprintf("func:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         funcID,
				Name:       m[1],
				Kind:       code.KindFunc,
				File:       relPath,
				Line:       lineNum,
				Package:    currentNs,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     funcID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		}
	}

	return res
}

func extractRubyFile(fullPath, relPath string) *ExtractionResult {
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
	res.Entities = append(res.Entities, CodeEntity{
		ID:         fileEntityID,
		Name:       filepath.Base(relPath),
		Kind:       code.KindModule,
		File:       relPath,
		Line:       1,
		Visibility: code.VisibilityPublic,
	})

	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		if m := rbRequireRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("module:%s", m[1])
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   code.RelationImports,
				Confidence: code.ConfidenceExtracted,
			})
		}

		if m := rbModuleRe.FindStringSubmatch(line); len(m) > 1 {
			modID := fmt.Sprintf("module:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         modID,
				Name:       m[1],
				Kind:       code.KindModule,
				File:       relPath,
				Line:       lineNum,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     modID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := rbClassRe.FindStringSubmatch(line); len(m) > 1 {
			classID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         classID,
				Name:       m[1],
				Kind:       code.KindClass,
				File:       relPath,
				Line:       lineNum,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := rbDefRe.FindStringSubmatch(line); len(m) > 1 {
			funcID := fmt.Sprintf("func:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         funcID,
				Name:       m[1],
				Kind:       code.KindFunc,
				File:       relPath,
				Line:       lineNum,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     funcID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		}
	}

	return res
}

func extractSwiftFile(fullPath, relPath string) *ExtractionResult {
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
	res.Entities = append(res.Entities, CodeEntity{
		ID:         fileEntityID,
		Name:       filepath.Base(relPath),
		Kind:       code.KindModule,
		File:       relPath,
		Line:       1,
		Visibility: code.VisibilityPublic,
	})

	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		if m := swiftImportRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("pkg:%s", m[1])
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   code.RelationImports,
				Confidence: code.ConfidenceExtracted,
			})
		}

		if m := swiftClassRe.FindStringSubmatch(line); len(m) > 1 {
			classID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         classID,
				Name:       m[1],
				Kind:       code.KindClass,
				File:       relPath,
				Line:       lineNum,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := swiftStructRe.FindStringSubmatch(line); len(m) > 1 {
			structID := fmt.Sprintf("struct:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         structID,
				Name:       m[1],
				Kind:       code.KindStruct,
				File:       relPath,
				Line:       lineNum,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     structID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := swiftProtocolRe.FindStringSubmatch(line); len(m) > 1 {
			protoID := fmt.Sprintf("interface:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         protoID,
				Name:       m[1],
				Kind:       code.KindInterface,
				File:       relPath,
				Line:       lineNum,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     protoID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := swiftEnumRe.FindStringSubmatch(line); len(m) > 1 {
			enumID := fmt.Sprintf("enum:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         enumID,
				Name:       m[1],
				Kind:       code.KindEnum,
				File:       relPath,
				Line:       lineNum,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     enumID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := swiftFuncRe.FindStringSubmatch(line); len(m) > 1 {
			funcID := fmt.Sprintf("func:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         funcID,
				Name:       m[1],
				Kind:       code.KindFunc,
				File:       relPath,
				Line:       lineNum,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     funcID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		}
	}

	return res
}
