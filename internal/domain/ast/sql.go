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
	sqlTableRe     = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z0-9_."]+)`)
	sqlViewRe      = regexp.MustCompile(`(?i)CREATE\s+(?:OR\s+REPLACE\s+)?VIEW\s+([a-zA-Z0-9_."]+)`)
	sqlFKTableRe   = regexp.MustCompile(`(?i)FOREIGN\s+KEY\s*\([^)]+\)\s*REFERENCES\s+([a-zA-Z0-9_."]+)`)
	sqlColFKRe     = regexp.MustCompile(`(?i)\bREFERENCES\s+([a-zA-Z0-9_."]+)\s*(?:\([^)]+\))?`)
	sqlColumnDefRe = regexp.MustCompile(`^\s*([a-zA-Z0-9_"]+)\s+([a-zA-Z0-9_()]+)`)
)

func extractSQLFile(fullPath, relPath string) *ExtractionResult {
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

	var currentTableID string
	var tableColumns []code.Parameter

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		// 1. CREATE TABLE
		if m := sqlTableRe.FindStringSubmatch(trimmed); len(m) > 1 {
			rawName := cleanSQLName(m[1])
			tableID := fmt.Sprintf("table:%s", rawName)
			currentTableID = tableID
			tableColumns = nil

			res.Entities = append(res.Entities, CodeEntity{
				ID:         tableID,
				Name:       rawName,
				Kind:       code.KindTable,
				File:       relPath,
				Line:       lineNum,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
				Signature:  trimmed,
			})

			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     tableID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     tableID,
				Relation:   code.RelationContains,
				Confidence: code.ConfidenceExtracted,
			})

			res.Exports = append(res.Exports, ExportFact{
				SourceFile:   relPath,
				ExportedName: rawName,
				SymbolID:     tableID,
				Line:         lineNum,
			})
			continue
		}

		// 2. CREATE VIEW
		if m := sqlViewRe.FindStringSubmatch(trimmed); len(m) > 1 {
			rawName := cleanSQLName(m[1])
			viewID := fmt.Sprintf("table:%s", rawName)

			res.Entities = append(res.Entities, CodeEntity{
				ID:         viewID,
				Name:       rawName,
				Kind:       code.KindTable,
				File:       relPath,
				Line:       lineNum,
				ParentID:   fileEntityID,
				Visibility: code.VisibilityPublic,
				Signature:  trimmed,
			})

			res.Relationships = append(res.Relationships, CodeRelationship{
				Source:     fileEntityID,
				Target:     viewID,
				Relation:   code.RelationDefines,
				Confidence: code.ConfidenceExtracted,
			})
			continue
		}

		// 3. Foreign Key Reference (Table Constraint or Column Constraint)
		if currentTableID != "" {
			if m := sqlFKTableRe.FindStringSubmatch(trimmed); len(m) > 1 {
				targetTable := cleanSQLName(m[1])
				targetID := fmt.Sprintf("table:%s", targetTable)
				res.Relationships = append(res.Relationships, CodeRelationship{
					Source:     currentTableID,
					Target:     targetID,
					Relation:   code.RelationReferences,
					Confidence: code.ConfidenceExtracted,
					Reasoning:  fmt.Sprintf("Foreign key constraint references table %s", targetTable),
				})
			} else if m := sqlColFKRe.FindStringSubmatch(trimmed); len(m) > 1 {
				targetTable := cleanSQLName(m[1])
				targetID := fmt.Sprintf("table:%s", targetTable)
				res.Relationships = append(res.Relationships, CodeRelationship{
					Source:     currentTableID,
					Target:     targetID,
					Relation:   code.RelationReferences,
					Confidence: code.ConfidenceExtracted,
					Reasoning:  fmt.Sprintf("Column references table %s", targetTable),
				})
			}

			// Column extraction inside CREATE TABLE
			if !strings.HasPrefix(strings.ToUpper(trimmed), "CONSTRAINT") &&
				!strings.HasPrefix(strings.ToUpper(trimmed), "PRIMARY") &&
				!strings.HasPrefix(strings.ToUpper(trimmed), "FOREIGN") &&
				!strings.HasPrefix(strings.ToUpper(trimmed), "UNIQUE") &&
				!strings.HasPrefix(strings.ToUpper(trimmed), "KEY") &&
				!strings.HasPrefix(trimmed, ")") {
				if colMatch := sqlColumnDefRe.FindStringSubmatch(trimmed); len(colMatch) > 2 {
					colName := cleanSQLName(colMatch[1])
					colType := colMatch[2]
					tableColumns = append(tableColumns, code.Parameter{
						Name: colName,
						Type: colType,
					})
				}
			}

			if strings.HasPrefix(trimmed, ");") || trimmed == ")" {
				// End of table definition - attach columns
				for idx, ent := range res.Entities {
					if ent.ID == currentTableID {
						res.Entities[idx].Parameters = tableColumns
						break
					}
				}
				currentTableID = ""
				tableColumns = nil
			}
		}
	}

	return res
}

func cleanSQLName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, `"'`+"`")
	parts := strings.Split(name, ".")
	return parts[len(parts)-1]
}
