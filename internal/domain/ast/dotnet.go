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
	// C# Regexes
	csUsingRe     = regexp.MustCompile(`(?m)^\s*using\s+(?:static\s+)?(?:[a-zA-Z0-9_]+\s*=\s*)?([a-zA-Z0-9_.]+)\s*;`)
	csNamespaceRe = regexp.MustCompile(`(?m)^\s*namespace\s+([a-zA-Z0-9_.]+)`)
	csClassRe     = regexp.MustCompile(`(?m)^\s*(?:\[[^\]]+\]\s*)*(?:public|private|protected|internal|static|abstract|sealed|partial|\s)+class\s+([a-zA-Z0-9_]+)`)
	csInterfaceRe = regexp.MustCompile(`(?m)^\s*(?:\[[^\]]+\]\s*)*(?:public|private|protected|internal|abstract|partial|\s)+interface\s+([a-zA-Z0-9_]+)`)
	csStructRe    = regexp.MustCompile(`(?m)^\s*(?:\[[^\]]+\]\s*)*(?:public|private|protected|internal|readonly|ref|partial|\s)+struct\s+([a-zA-Z0-9_]+)`)
	csRecordRe    = regexp.MustCompile(`(?m)^\s*(?:\[[^\]]+\]\s*)*(?:public|private|protected|internal|readonly|partial|\s)+record\s+(?:class\s+|struct\s+)?([a-zA-Z0-9_]+)`)
	csEnumRe      = regexp.MustCompile(`(?m)^\s*(?:\[[^\]]+\]\s*)*(?:public|private|protected|internal|\s)+enum\s+([a-zA-Z0-9_]+)`)
	csMethodRe    = regexp.MustCompile(`(?m)^\s*(?:\[[^\]]+\]\s*)*(?:public|private|protected|internal|static|async|virtual|override|abstract|sealed|\s)+(?:void|[a-zA-Z0-9_<>?[\]]+)\s+([a-zA-Z0-9_]+)\s*\([^)]*\)\s*(?:where\s+.*)?\s*[{;]`)

	// F# Regexes
	fsOpenRe   = regexp.MustCompile(`(?m)^\s*open\s+([a-zA-Z0-9_.]+)`)
	fsModuleRe = regexp.MustCompile(`(?m)^\s*module\s+(?:rec\s+)?([a-zA-Z0-9_.]+)`)
	fsTypeRe   = regexp.MustCompile(`(?m)^\s*type\s+([a-zA-Z0-9_]+)`)
	fsLetRe    = regexp.MustCompile(`(?m)^\s*let\s+(?:rec\s+)?(?:private\s+|public\s+|internal\s+)?([a-zA-Z0-9_]+)`)

	// VB.NET Regexes
	vbImportsRe   = regexp.MustCompile(`(?mi)^\s*Imports\s+([a-zA-Z0-9_.]+)`)
	vbNamespaceRe = regexp.MustCompile(`(?mi)^\s*Namespace\s+([a-zA-Z0-9_.]+)`)
	vbClassRe     = regexp.MustCompile(`(?mi)^\s*(?:Public|Private|Protected|Friend|Partial|\s)*Class\s+([a-zA-Z0-9_]+)`)
	vbInterfaceRe = regexp.MustCompile(`(?mi)^\s*(?:Public|Private|Protected|Friend|\s)*Interface\s+([a-zA-Z0-9_]+)`)
	vbStructRe    = regexp.MustCompile(`(?mi)^\s*(?:Public|Private|Protected|Friend|\s)*Structure\s+([a-zA-Z0-9_]+)`)
	vbModuleRe    = regexp.MustCompile(`(?mi)^\s*(?:Public|Private|Protected|Friend|\s)*Module\s+([a-zA-Z0-9_]+)`)
	vbMethodRe    = regexp.MustCompile(`(?mi)^\s*(?:Public|Private|Protected|Friend|Shared|Overridable|Overrides|Async|\s)*(?:Sub|Function)\s+([a-zA-Z0-9_]+)\s*\(`)
)

func extractCSharpFile(fullPath, relPath string) *ExtractionResult {
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

		if m := csNamespaceRe.FindStringSubmatch(line); len(m) > 1 {
			currentPkg = strings.TrimSuffix(m[1], ";")
		}

		if m := csUsingRe.FindStringSubmatch(line); len(m) > 1 {
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

		if m := csClassRe.FindStringSubmatch(line); len(m) > 1 {
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
		} else if m := csInterfaceRe.FindStringSubmatch(line); len(m) > 1 {
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
		} else if m := csStructRe.FindStringSubmatch(line); len(m) > 1 {
			structID := fmt.Sprintf("struct:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:         structID,
				Name:       m[1],
				Kind:       code.KindStruct,
				File:       relPath,
				Line:       lineNum,
				Package:    currentPkg,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     structID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := csRecordRe.FindStringSubmatch(line); len(m) > 1 {
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
		} else if m := csEnumRe.FindStringSubmatch(line); len(m) > 1 {
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
		} else if m := csMethodRe.FindStringSubmatch(line); len(m) > 1 {
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

func extractFSharpFile(fullPath, relPath string) *ExtractionResult {
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

		if m := fsOpenRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("pkg:%s", m[1])
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   code.RelationImports,
				Confidence: code.ConfidenceExtracted,
			})
		}

		if m := fsModuleRe.FindStringSubmatch(line); len(m) > 1 {
			modID := fmt.Sprintf("module:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:       modID,
				Name:     m[1],
				Kind:     code.KindModule,
				File:     relPath,
				Line:     lineNum,
				ParentID: fileEntityID,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     modID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := fsTypeRe.FindStringSubmatch(line); len(m) > 1 {
			typeID := fmt.Sprintf("type:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:       typeID,
				Name:     m[1],
				Kind:     code.KindStruct,
				File:     relPath,
				Line:     lineNum,
				ParentID: fileEntityID,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     typeID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := fsLetRe.FindStringSubmatch(line); len(m) > 1 {
			funcID := fmt.Sprintf("func:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:       funcID,
				Name:     m[1],
				Kind:     code.KindFunc,
				File:     relPath,
				Line:     lineNum,
				ParentID: fileEntityID,
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

func extractVBDotNetFile(fullPath, relPath string) *ExtractionResult {
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

		if m := vbNamespaceRe.FindStringSubmatch(line); len(m) > 1 {
			currentNs = m[1]
		}

		if m := vbImportsRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("pkg:%s", m[1])
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   code.RelationImports,
				Confidence: code.ConfidenceExtracted,
			})
		}

		if m := vbClassRe.FindStringSubmatch(line); len(m) > 1 {
			classID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:       classID,
				Name:     m[1],
				Kind:     code.KindClass,
				File:     relPath,
				Line:     lineNum,
				Package:  currentNs,
				ParentID: fileEntityID,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := vbInterfaceRe.FindStringSubmatch(line); len(m) > 1 {
			ifID := fmt.Sprintf("interface:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:       ifID,
				Name:     m[1],
				Kind:     code.KindInterface,
				File:     relPath,
				Line:     lineNum,
				Package:  currentNs,
				ParentID: fileEntityID,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     ifID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := vbStructRe.FindStringSubmatch(line); len(m) > 1 {
			structID := fmt.Sprintf("struct:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:       structID,
				Name:     m[1],
				Kind:     code.KindStruct,
				File:     relPath,
				Line:     lineNum,
				Package:  currentNs,
				ParentID: fileEntityID,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     structID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := vbModuleRe.FindStringSubmatch(line); len(m) > 1 {
			modID := fmt.Sprintf("module:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:       modID,
				Name:     m[1],
				Kind:     code.KindModule,
				File:     relPath,
				Line:     lineNum,
				Package:  currentNs,
				ParentID: fileEntityID,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     modID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
		} else if m := vbMethodRe.FindStringSubmatch(line); len(m) > 1 {
			funcID := fmt.Sprintf("func:%s:%s", relPath, m[1])
			res.Entities = append(res.Entities, CodeEntity{
				ID:       funcID,
				Name:     m[1],
				Kind:     code.KindFunc,
				File:     relPath,
				Line:     lineNum,
				Package:  currentNs,
				ParentID: fileEntityID,
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
