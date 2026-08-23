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
	// Java Regexes
	javaPackageRe   = regexp.MustCompile(`(?m)^\s*package\s+([a-zA-Z0-9_.]+)\s*;`)
	javaImportRe    = regexp.MustCompile(`(?m)^\s*import\s+(?:static\s+)?([a-zA-Z0-9_.]+)\s*;`)
	javaClassRe     = regexp.MustCompile(`(?m)^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:public|protected|private|static|abstract|final|\s)+class\s+([a-zA-Z0-9_]+)`)
	javaInterfaceRe = regexp.MustCompile(`(?m)^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:public|protected|private|static|\s)+interface\s+([a-zA-Z0-9_]+)`)
	javaEnumRe      = regexp.MustCompile(`(?m)^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:public|protected|private|static|\s)+enum\s+([a-zA-Z0-9_]+)`)
	javaRecordRe    = regexp.MustCompile(`(?m)^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:public|protected|private|static|\s)+record\s+([a-zA-Z0-9_]+)`)
	javaMethodRe    = regexp.MustCompile(`(?m)^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:public|protected|private|static|final|synchronized|abstract|\s)+(?:void|[a-zA-Z0-9_<>?[\]]+)\s+([a-zA-Z0-9_]+)\s*\([^)]*\)\s*(?:throws\s+[a-zA-Z0-9_,\s]+)?\s*[{;]`)

	// Kotlin Regexes
	ktPackageRe   = regexp.MustCompile(`(?m)^\s*package\s+([a-zA-Z0-9_.]+)`)
	ktImportRe    = regexp.MustCompile(`(?m)^\s*import\s+([a-zA-Z0-9_.*]+)`)
	ktClassRe     = regexp.MustCompile(`(?m)^\s*(?:open|abstract|sealed|data|inner|value|\s)*class\s+([a-zA-Z0-9_]+)`)
	ktInterfaceRe = regexp.MustCompile(`(?m)^\s*interface\s+([a-zA-Z0-9_]+)`)
	ktObjectRe    = regexp.MustCompile(`(?m)^\s*(?:companion\s+)?object(?:\s+([a-zA-Z0-9_]+))?`)
	ktEnumRe      = regexp.MustCompile(`(?m)^\s*enum\s+class\s+([a-zA-Z0-9_]+)`)
	ktFunRe       = regexp.MustCompile(`(?m)^\s*(?:override|private|protected|public|internal|suspend|inline|\s)*fun\s+(?:<[^>]+>\s+)?([a-zA-Z0-9_]+)\s*\(`)
)

func extractJavaFile(fullPath, relPath string) *ExtractionResult {
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
	currentPkg := ""

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		if m := javaPackageRe.FindStringSubmatch(line); len(m) > 1 {
			currentPkg = m[1]
		}

		if m := javaImportRe.FindStringSubmatch(line); len(m) > 1 {
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

		if m := javaClassRe.FindStringSubmatch(line); len(m) > 1 {
			classID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         classID,
				Name:       m[1],
				Kind:       code.KindClass,
				File:       relPath,
				Line:       lineNum,
				Package:    currentPkg,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := javaInterfaceRe.FindStringSubmatch(line); len(m) > 1 {
			ifID := fmt.Sprintf("interface:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         ifID,
				Name:       m[1],
				Kind:       code.KindInterface,
				File:       relPath,
				Line:       lineNum,
				Package:    currentPkg,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     ifID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := javaEnumRe.FindStringSubmatch(line); len(m) > 1 {
			enumID := fmt.Sprintf("enum:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         enumID,
				Name:       m[1],
				Kind:       code.KindEnum,
				File:       relPath,
				Line:       lineNum,
				Package:    currentPkg,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     enumID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := javaRecordRe.FindStringSubmatch(line); len(m) > 1 {
			recID := fmt.Sprintf("record:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         recID,
				Name:       m[1],
				Kind:       "record",
				File:       relPath,
				Line:       lineNum,
				Package:    currentPkg,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     recID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := javaMethodRe.FindStringSubmatch(line); len(m) > 1 {
			methodName := m[1]
			if methodName != "if" && methodName != "for" && methodName != "while" && methodName != "switch" && methodName != "catch" {
				funcID := fmt.Sprintf("func:%s:%s", relPath, methodName)
				res.Entities = append(res.Entities, CodeEntity{
					ID:         funcID,
					Name:       methodName,
					Kind:       code.KindFunc,
					File:       relPath,
					Line:       lineNum,
					Package:    currentPkg,
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

func extractKotlinFile(fullPath, relPath string) *ExtractionResult {
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
	currentPkg := ""

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		if m := ktPackageRe.FindStringSubmatch(line); len(m) > 1 {
			currentPkg = m[1]
		}

		if m := ktImportRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("pkg:%s", m[1])
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   code.RelationImports,
				Confidence: code.ConfidenceExtracted,
			})
		}

		if m := ktClassRe.FindStringSubmatch(line); len(m) > 1 {
			classID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         classID,
				Name:       m[1],
				Kind:       code.KindClass,
				File:       relPath,
				Line:       lineNum,
				Package:    currentPkg,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := ktInterfaceRe.FindStringSubmatch(line); len(m) > 1 {
			ifID := fmt.Sprintf("interface:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         ifID,
				Name:       m[1],
				Kind:       code.KindInterface,
				File:       relPath,
				Line:       lineNum,
				Package:    currentPkg,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     ifID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := ktObjectRe.FindStringSubmatch(line); len(m) > 1 && m[1] != "" {
			objID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         objID,
				Name:       m[1],
				Kind:       code.KindClass,
				File:       relPath,
				Line:       lineNum,
				Package:    currentPkg,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     objID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := ktEnumRe.FindStringSubmatch(line); len(m) > 1 {
			enumID := fmt.Sprintf("enum:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         enumID,
				Name:       m[1],
				Kind:       code.KindEnum,
				File:       relPath,
				Line:       lineNum,
				Package:    currentPkg,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     enumID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := ktFunRe.FindStringSubmatch(line); len(m) > 1 {
			funcID := fmt.Sprintf("func:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         funcID,
				Name:       m[1],
				Kind:       code.KindFunc,
				File:       relPath,
				Line:       lineNum,
				Package:    currentPkg,
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
