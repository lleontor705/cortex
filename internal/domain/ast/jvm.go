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

func extractJavaFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
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

		if m := javaPackageRe.FindStringSubmatch(line); len(m) > 1 {
			currentPkg = m[1]
		}

		if m := javaImportRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("pkg:%s", m[1])
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   "imports",
				Confidence: 1.0,
			})
		}

		if m := javaClassRe.FindStringSubmatch(line); len(m) > 1 {
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
		} else if m := javaInterfaceRe.FindStringSubmatch(line); len(m) > 1 {
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
		} else if m := javaEnumRe.FindStringSubmatch(line); len(m) > 1 {
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
		} else if m := javaRecordRe.FindStringSubmatch(line); len(m) > 1 {
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
		} else if m := javaMethodRe.FindStringSubmatch(line); len(m) > 1 {
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

func extractKotlinFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
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

		if m := ktPackageRe.FindStringSubmatch(line); len(m) > 1 {
			currentPkg = m[1]
		}

		if m := ktImportRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("pkg:%s", m[1])
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   "imports",
				Confidence: 1.0,
			})
		}

		if m := ktClassRe.FindStringSubmatch(line); len(m) > 1 {
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
		} else if m := ktInterfaceRe.FindStringSubmatch(line); len(m) > 1 {
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
		} else if m := ktObjectRe.FindStringSubmatch(line); len(m) > 1 && m[1] != "" {
			objID := fmt.Sprintf("class:%s:%s", relPath, m[1])
			entities = append(entities, CodeEntity{
				ID:      objID,
				Name:    m[1],
				Kind:    "class",
				File:    relPath,
				Line:    lineNum,
				Package: currentPkg,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     objID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		} else if m := ktEnumRe.FindStringSubmatch(line); len(m) > 1 {
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
		} else if m := ktFunRe.FindStringSubmatch(line); len(m) > 1 {
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
