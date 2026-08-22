package ast

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

func extractCSharpFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
	var entities []CodeEntity
	var rels []CodeRelationship

	file, err := os.Open(fullPath)
	if err != nil {
		return entities, rels
	}
	defer func() { _ = file.Close() }()

	fileEntityID := fmt.Sprintf("module:%s", relPath)
	entities = append(entities, CodeEntity{
		ID:   fileEntityID,
		Name: filepath.Base(relPath),
		Kind: "module",
		File: relPath,
		Line: 1,
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
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   "imports",
				Confidence: 1.0,
			})
		}

		if m := csClassRe.FindStringSubmatch(line); len(m) > 1 {
			classID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      classID,
				Name:    m[1],
				Kind:    "class",
				File:    relPath,
				Line:    lineNum,
				Package: currentPkg,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := csInterfaceRe.FindStringSubmatch(line); len(m) > 1 {
			ifID := fmt.Sprintf("interface:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      ifID,
				Name:    m[1],
				Kind:    "interface",
				File:    relPath,
				Line:    lineNum,
				Package: currentPkg,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     ifID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := csStructRe.FindStringSubmatch(line); len(m) > 1 {
			structID := fmt.Sprintf("struct:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      structID,
				Name:    m[1],
				Kind:    "struct",
				File:    relPath,
				Line:    lineNum,
				Package: currentPkg,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     structID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := csRecordRe.FindStringSubmatch(line); len(m) > 1 {
			recID := fmt.Sprintf("record:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      recID,
				Name:    m[1],
				Kind:    "record",
				File:    relPath,
				Line:    lineNum,
				Package: currentPkg,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     recID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := csEnumRe.FindStringSubmatch(line); len(m) > 1 {
			enumID := fmt.Sprintf("enum:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      enumID,
				Name:    m[1],
				Kind:    "enum",
				File:    relPath,
				Line:    lineNum,
				Package: currentPkg,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     enumID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := csMethodRe.FindStringSubmatch(line); len(m) > 1 {
			methodName := m[1]
			if methodName != "if" && methodName != "for" && methodName != "while" && methodName != "switch" && methodName != "catch" {
				funcID := fmt.Sprintf("func:%s:%s", relPath, methodName)
				entities = append(entities, CodeEntity{
					ID:      funcID,
					Name:    methodName,
					Kind:    "func",
					File:    relPath,
					Line:    lineNum,
					Package: currentPkg,
				})
				rels = append(rels, CodeRelationship{
					Source:     fileEntityID,
					Target:     funcID,
					Relation:   "defines",
					Confidence: 1.0,
				})
			}
		}
	}

	return entities, rels
}

func extractFSharpFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
	var entities []CodeEntity
	var rels []CodeRelationship

	file, err := os.Open(fullPath)
	if err != nil {
		return entities, rels
	}
	defer func() { _ = file.Close() }()

	fileEntityID := fmt.Sprintf("module:%s", relPath)
	entities = append(entities, CodeEntity{
		ID:   fileEntityID,
		Name: filepath.Base(relPath),
		Kind: "module",
		File: relPath,
		Line: 1,
	})

	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		if m := fsOpenRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("pkg:%s", m[1])
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   "imports",
				Confidence: 1.0,
			})
		}

		if m := fsModuleRe.FindStringSubmatch(line); len(m) > 1 {
			modID := fmt.Sprintf("module:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:   modID,
				Name: m[1],
				Kind: "module",
				File: relPath,
				Line: lineNum,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     modID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := fsTypeRe.FindStringSubmatch(line); len(m) > 1 {
			typeID := fmt.Sprintf("type:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:   typeID,
				Name: m[1],
				Kind: "struct",
				File: relPath,
				Line: lineNum,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     typeID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := fsLetRe.FindStringSubmatch(line); len(m) > 1 {
			funcID := fmt.Sprintf("func:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:   funcID,
				Name: m[1],
				Kind: "func",
				File: relPath,
				Line: lineNum,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     funcID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		}
	}

	return entities, rels
}

func extractVBDotNetFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
	var entities []CodeEntity
	var rels []CodeRelationship

	file, err := os.Open(fullPath)
	if err != nil {
		return entities, rels
	}
	defer func() { _ = file.Close() }()

	fileEntityID := fmt.Sprintf("module:%s", relPath)
	entities = append(entities, CodeEntity{
		ID:   fileEntityID,
		Name: filepath.Base(relPath),
		Kind: "module",
		File: relPath,
		Line: 1,
	})

	scanner := bufio.NewScanner(file)
	lineNum := 0
	currentPkg := ""

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		if m := vbNamespaceRe.FindStringSubmatch(line); len(m) > 1 {
			currentPkg = m[1]
		}

		if m := vbImportsRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("pkg:%s", m[1])
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   "imports",
				Confidence: 1.0,
			})
		}

		if m := vbClassRe.FindStringSubmatch(line); len(m) > 1 {
			classID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      classID,
				Name:    m[1],
				Kind:    "class",
				File:    relPath,
				Line:    lineNum,
				Package: currentPkg,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := vbInterfaceRe.FindStringSubmatch(line); len(m) > 1 {
			ifID := fmt.Sprintf("interface:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      ifID,
				Name:    m[1],
				Kind:    "interface",
				File:    relPath,
				Line:    lineNum,
				Package: currentPkg,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     ifID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := vbStructRe.FindStringSubmatch(line); len(m) > 1 {
			structID := fmt.Sprintf("struct:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      structID,
				Name:    m[1],
				Kind:    "struct",
				File:    relPath,
				Line:    lineNum,
				Package: currentPkg,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     structID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := vbModuleRe.FindStringSubmatch(line); len(m) > 1 {
			modID := fmt.Sprintf("module:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      modID,
				Name:    m[1],
				Kind:    "module",
				File:    relPath,
				Line:    lineNum,
				Package: currentPkg,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     modID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := vbMethodRe.FindStringSubmatch(line); len(m) > 1 {
			funcID := fmt.Sprintf("func:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      funcID,
				Name:    m[1],
				Kind:    "func",
				File:    relPath,
				Line:    lineNum,
				Package: currentPkg,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     funcID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		}
	}

	return entities, rels
}
