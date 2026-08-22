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
	// Rust Regexes
	rsUseRe    = regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]+\))?\s+)?use\s+([a-zA-Z0-9_:]+)`)
	rsModRe    = regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]+\))?\s+)?mod\s+([a-zA-Z0-9_]+)\s*;`)
	rsStructRe = regexp.MustCompile(`(?m)^\s*(?:#\[[^\]]+\]\s*)*(?:pub(?:\([^)]+\))?\s+)?struct\s+([a-zA-Z0-9_]+)`)
	rsEnumRe   = regexp.MustCompile(`(?m)^\s*(?:#\[[^\]]+\]\s*)*(?:pub(?:\([^)]+\))?\s+)?enum\s+([a-zA-Z0-9_]+)`)
	rsTraitRe  = regexp.MustCompile(`(?m)^\s*(?:#\[[^\]]+\]\s*)*(?:pub(?:\([^)]+\))?\s+)?trait\s+([a-zA-Z0-9_]+)`)
	rsFnRe     = regexp.MustCompile(`(?m)^\s*(?:#\[[^\]]+\]\s*)*(?:pub(?:\([^)]+\))?\s+)?(?:async\s+)?(?:unsafe\s+)?(?:extern\s+(?:"[^"]+"\s+)?)?fn\s+([a-zA-Z0-9_]+)\s*(?:<[^>]+>)?\s*\(`)

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
	swiftExtensionRe = regexp.MustCompile(`(?m)^\s*extension\s+([a-zA-Z0-9_]+)`)
	swiftFuncRe      = regexp.MustCompile(`(?m)^\s*(?:public|private|internal|open|override|static|class|mutating|\s)*func\s+([a-zA-Z0-9_]+)\s*(?:<[^>]+>)?\s*\(`)
)

func extractRustFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
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
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		if m := rsUseRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("pkg:%s", m[1])
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   "imports",
				Confidence: 1.0,
			})
		} else if m := rsModRe.FindStringSubmatch(line); len(m) > 1 {
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
		}

		if m := rsStructRe.FindStringSubmatch(line); len(m) > 1 {
			structID := fmt.Sprintf("struct:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:   structID,
				Name: m[1],
				Kind: "struct",
				File: relPath,
				Line: lineNum,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     structID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := rsEnumRe.FindStringSubmatch(line); len(m) > 1 {
			enumID := fmt.Sprintf("enum:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:   enumID,
				Name: m[1],
				Kind: "enum",
				File: relPath,
				Line: lineNum,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     enumID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := rsTraitRe.FindStringSubmatch(line); len(m) > 1 {
			traitID := fmt.Sprintf("interface:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:   traitID,
				Name: m[1],
				Kind: "interface",
				File: relPath,
				Line: lineNum,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     traitID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := rsFnRe.FindStringSubmatch(line); len(m) > 1 {
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

func extractCppFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
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
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   "imports",
				Confidence: 1.0,
			})
		}

		if m := cppClassRe.FindStringSubmatch(line); len(m) > 1 {
			classID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      classID,
				Name:    m[1],
				Kind:    "class",
				File:    relPath,
				Line:    lineNum,
				Package: currentNs,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := cppStructRe.FindStringSubmatch(line); len(m) > 1 {
			structID := fmt.Sprintf("struct:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      structID,
				Name:    m[1],
				Kind:    "struct",
				File:    relPath,
				Line:    lineNum,
				Package: currentNs,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     structID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := cppFuncRe.FindStringSubmatch(line); len(m) > 1 {
			fnName := m[1]
			if fnName != "if" && fnName != "for" && fnName != "while" && fnName != "switch" && fnName != "catch" {
				funcID := fmt.Sprintf("func:%s:%s", relPath, fnName)
				entities = append(entities, CodeEntity{
					ID:      funcID,
					Name:    fnName,
					Kind:    "func",
					File:    relPath,
					Line:    lineNum,
					Package: currentNs,
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

func extractPhpFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
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
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   "imports",
				Confidence: 1.0,
			})
		}

		if m := phpClassRe.FindStringSubmatch(line); len(m) > 1 {
			classID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      classID,
				Name:    m[1],
				Kind:    "class",
				File:    relPath,
				Line:    lineNum,
				Package: currentNs,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := phpInterfaceRe.FindStringSubmatch(line); len(m) > 1 {
			ifID := fmt.Sprintf("interface:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      ifID,
				Name:    m[1],
				Kind:    "interface",
				File:    relPath,
				Line:    lineNum,
				Package: currentNs,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     ifID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := phpTraitRe.FindStringSubmatch(line); len(m) > 1 {
			traitID := fmt.Sprintf("interface:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      traitID,
				Name:    m[1],
				Kind:    "interface",
				File:    relPath,
				Line:    lineNum,
				Package: currentNs,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     traitID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := phpEnumRe.FindStringSubmatch(line); len(m) > 1 {
			enumID := fmt.Sprintf("enum:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      enumID,
				Name:    m[1],
				Kind:    "enum",
				File:    relPath,
				Line:    lineNum,
				Package: currentNs,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     enumID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := phpFuncRe.FindStringSubmatch(line); len(m) > 1 {
			funcID := fmt.Sprintf("func:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      funcID,
				Name:    m[1],
				Kind:    "func",
				File:    relPath,
				Line:    lineNum,
				Package: currentNs,
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

func extractRubyFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
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
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		if m := rbRequireRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("module:%s", m[1])
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   "imports",
				Confidence: 1.0,
			})
		}

		if m := rbModuleRe.FindStringSubmatch(line); len(m) > 1 {
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
		} else if m := rbClassRe.FindStringSubmatch(line); len(m) > 1 {
			classID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:   classID,
				Name: m[1],
				Kind: "class",
				File: relPath,
				Line: lineNum,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := rbDefRe.FindStringSubmatch(line); len(m) > 1 {
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

func extractSwiftFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
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
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		if m := swiftImportRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("pkg:%s", m[1])
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   "imports",
				Confidence: 1.0,
			})
		}

		if m := swiftClassRe.FindStringSubmatch(line); len(m) > 1 {
			classID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:   classID,
				Name: m[1],
				Kind: "class",
				File: relPath,
				Line: lineNum,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     classID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := swiftStructRe.FindStringSubmatch(line); len(m) > 1 {
			structID := fmt.Sprintf("struct:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:   structID,
				Name: m[1],
				Kind: "struct",
				File: relPath,
				Line: lineNum,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     structID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := swiftProtocolRe.FindStringSubmatch(line); len(m) > 1 {
			protoID := fmt.Sprintf("interface:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:   protoID,
				Name: m[1],
				Kind: "interface",
				File: relPath,
				Line: lineNum,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     protoID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := swiftEnumRe.FindStringSubmatch(line); len(m) > 1 {
			enumID := fmt.Sprintf("enum:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:   enumID,
				Name: m[1],
				Kind: "enum",
				File: relPath,
				Line: lineNum,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     enumID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := swiftExtensionRe.FindStringSubmatch(line); len(m) > 1 {
			extID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     extID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := swiftFuncRe.FindStringSubmatch(line); len(m) > 1 {
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
