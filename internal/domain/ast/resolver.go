package ast

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

// Resolver orchestrates global multi-pass cross-file symbol and call graph resolution.
type Resolver struct {
	RootPath string
	Project  string
}

// NewResolver initializes a symbol resolver.
func NewResolver(rootPath, project string) *Resolver {
	if project == "" {
		project = "default"
	}
	return &Resolver{
		RootPath: rootPath,
		Project:  project,
	}
}

// Resolve processes raw extraction results across all files in a project,
// constructing a fully resolved, connected *code.CodeGraph.
func (r *Resolver) Resolve(raw *ExtractionResult) *code.CodeGraph {
	fileHashes := make(map[string]string)
	symbols := make([]code.Symbol, 0, len(raw.Entities))

	// Lookup indexes
	symbolIDMap := make(map[string]string)              // rawEntityID -> canonicalSymbolID
	byName := make(map[string][]string)                 // name -> []canonicalSymbolID
	byFileAndName := make(map[string]map[string]string) // file -> (name -> canonicalSymbolID)
	byPkgAndName := make(map[string]map[string]string)  // pkg -> (name -> canonicalSymbolID)
	exportsByFile := make(map[string]map[string]string) // file -> (exportedName -> canonicalSymbolID)
	importsByFile := make(map[string][]ImportFact)      // file -> []ImportFact

	now := time.Now().UTC()

	// -------------------------------------------------------------
	// PASS 1: Build Global Symbol Table & Canonical Entities
	// -------------------------------------------------------------
	for _, ent := range raw.Entities {
		hash, ok := fileHashes[ent.File]
		if !ok {
			fullPath := filepath.Join(r.RootPath, ent.File)
			hash = ComputeFileHash(fullPath)
			fileHashes[ent.File] = hash
		}

		canonicalKey := fmt.Sprintf("sym:%s:%s:%s:%s:%d", r.Project, ent.Kind, ent.Name, ent.File, ent.Line)
		h := sha256.Sum256([]byte(canonicalKey))
		symbolID := hex.EncodeToString(h[:16])

		docSummary := ent.DocSummary
		if docSummary == "" {
			docSummary = fmt.Sprintf("%s %s in %s (line %d)", ent.Kind, ent.Name, ent.File, ent.Line)
		}

		endLine := ent.EndLine
		if endLine <= 0 {
			endLine = ent.Line
		}

		sym := code.Symbol{
			ID:          symbolID,
			Project:     r.Project,
			FilePath:    ent.File,
			LineNumber:  ent.Line,
			EndLine:     endLine,
			StartColumn: ent.StartCol,
			EndColumn:   ent.EndCol,
			Kind:        ent.Kind,
			Name:        ent.Name,
			PackageName: ent.Package,
			ParentID:    ent.ParentID,
			Visibility:  ent.Visibility,
			Signature:   ent.Signature,
			DocSummary:  docSummary,
			Parameters:  ent.Parameters,
			ReturnType:  ent.ReturnType,
			Complexity:  ent.Complexity,
			Metadata:    ent.Metadata,
			FileHash:    hash,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		symbols = append(symbols, sym)

		// Populate indexes
		symbolIDMap[ent.ID] = symbolID
		symbolIDMap[ent.Name] = symbolID
		byName[ent.Name] = append(byName[ent.Name], symbolID)

		if _, exists := byFileAndName[ent.File]; !exists {
			byFileAndName[ent.File] = make(map[string]string)
		}
		byFileAndName[ent.File][ent.Name] = symbolID

		if ent.Package != "" {
			if _, exists := byPkgAndName[ent.Package]; !exists {
				byPkgAndName[ent.Package] = make(map[string]string)
			}
			byPkgAndName[ent.Package][ent.Name] = symbolID
		}
	}

	// Index Exports
	for _, exp := range raw.Exports {
		canonicalID, ok := symbolIDMap[exp.SymbolID]
		if !ok {
			canonicalID = symbolIDMap[exp.ExportedName]
		}
		if canonicalID != "" {
			if _, exists := exportsByFile[exp.SourceFile]; !exists {
				exportsByFile[exp.SourceFile] = make(map[string]string)
			}
			exportsByFile[exp.SourceFile][exp.ExportedName] = canonicalID
			if exp.IsDefault {
				exportsByFile[exp.SourceFile]["default"] = canonicalID
			}
		}
	}

	// Index Imports
	for _, imp := range raw.Imports {
		importsByFile[imp.SourceFile] = append(importsByFile[imp.SourceFile], imp)
	}

	// -------------------------------------------------------------
	// PASS 2: Multi-Pass Cross-File Relationship Resolution
	// -------------------------------------------------------------
	relationKeySet := make(map[string]bool)
	relations := make([]code.Relation, 0, len(raw.Relationships))

	for _, rel := range raw.Relationships {
		srcID, okSrc := symbolIDMap[rel.Source]
		if !okSrc {
			// Try fallback by name
			if candidates := byName[rel.Source]; len(candidates) > 0 {
				srcID = candidates[0]
				okSrc = true
			}
		}

		tgtID, okTgt := symbolIDMap[rel.Target]
		reasoning := rel.Reasoning
		confidence := rel.Confidence
		if confidence <= 0 {
			confidence = code.ConfidenceExtracted
		}

		// Cross-File Resolution if target not resolved by exact ID
		if !okTgt {
			targetName := extractSymbolNameFromTarget(rel.Target)

			// 1. Check if source file explicitly imported this symbol
			resolvedByImport := false
			sourceFile := extractFileFromRawID(rel.Source)
			if sourceFile != "" {
				if imps, hasImps := importsByFile[sourceFile]; hasImps {
					for _, imp := range imps {
						if imp.LocalName == targetName || imp.ImportedName == targetName || imp.IsWildcard {
							// Resolve target file path
							resolvedTargetFile := r.resolveModulePath(sourceFile, imp.ImportPath)
							if resolvedTargetFile != "" {
								if fileSyms, hasFile := exportsByFile[resolvedTargetFile]; hasFile {
									if matchID, ok := fileSyms[targetName]; ok {
										tgtID = matchID
										okTgt = true
										resolvedByImport = true
										confidence = code.ConfidenceExtracted
										reasoning = fmt.Sprintf("Resolved cross-file import from %s", resolvedTargetFile)
										break
									}
								}
								if !resolvedByImport {
									if fileSyms, hasFile := byFileAndName[resolvedTargetFile]; hasFile {
										if matchID, ok := fileSyms[targetName]; ok {
											tgtID = matchID
											okTgt = true
											resolvedByImport = true
											confidence = code.ConfidenceExtracted
											reasoning = fmt.Sprintf("Resolved symbol from imported file %s", resolvedTargetFile)
											break
										}
									}
								}
							}
						}
					}
				}
			}

			// 2. Check same file / same directory / same package
			if !okTgt && sourceFile != "" {
				if fileSyms, hasFile := byFileAndName[sourceFile]; hasFile {
					if matchID, ok := fileSyms[targetName]; ok {
						tgtID = matchID
						okTgt = true
						confidence = code.ConfidenceInferred
						reasoning = fmt.Sprintf("Resolved local symbol %s in %s", targetName, sourceFile)
					}
				}
			}

			// 3. Check global project symbol table by unique name
			if !okTgt {
				if candidates, exists := byName[targetName]; exists {
					if len(candidates) == 1 {
						tgtID = candidates[0]
						okTgt = true
						confidence = code.ConfidenceInferred
						reasoning = fmt.Sprintf("Resolved globally unique symbol %s", targetName)
					} else if len(candidates) > 1 {
						// Multiple matches: pick first candidate with lower confidence
						tgtID = candidates[0]
						okTgt = true
						confidence = code.ConfidenceAmbiguous
						reasoning = fmt.Sprintf("Resolved heuristic candidate for %s", targetName)
					}
				}
			}
		}

		// Emit edge if both endpoints are valid and not self-looping on the same relation
		if okSrc && okTgt && srcID != tgtID {
			dedupKey := fmt.Sprintf("%s:%s:%s", srcID, tgtID, rel.Relation)
			if !relationKeySet[dedupKey] {
				relationKeySet[dedupKey] = true
				relations = append(relations, code.Relation{
					Project:    r.Project,
					SourceID:   srcID,
					TargetID:   tgtID,
					Relation:   rel.Relation,
					Confidence: confidence,
					Reasoning:  reasoning,
					CreatedAt:  now,
				})
			}
		}
	}

	return &code.CodeGraph{
		Project:   r.Project,
		Symbols:   symbols,
		Relations: relations,
	}
}

// resolveModulePath resolves relative or package import paths to canonical relative file paths.
func (r *Resolver) resolveModulePath(sourceFile, importPath string) string {
	if strings.HasPrefix(importPath, ".") {
		dir := filepath.Dir(sourceFile)
		baseResolved := filepath.ToSlash(filepath.Clean(filepath.Join(dir, importPath)))

		// Check common extensions
		candidates := []string{
			baseResolved,
			baseResolved + ".ts",
			baseResolved + ".tsx",
			baseResolved + ".js",
			baseResolved + ".jsx",
			baseResolved + ".py",
			baseResolved + ".go",
			filepath.ToSlash(filepath.Join(baseResolved, "index.ts")),
			filepath.ToSlash(filepath.Join(baseResolved, "index.tsx")),
			filepath.ToSlash(filepath.Join(baseResolved, "index.js")),
			filepath.ToSlash(filepath.Join(baseResolved, "__init__.py")),
		}

		for _, cand := range candidates {
			fullCand := filepath.Join(r.RootPath, cand)
			if _, err := filepath.Glob(fullCand); err == nil {
				return cand
			}
		}
		return baseResolved
	}
	return importPath
}

func extractSymbolNameFromTarget(rawTarget string) string {
	parts := strings.Split(rawTarget, ":")
	name := parts[len(parts)-1]
	dotParts := strings.Split(name, ".")
	return dotParts[len(dotParts)-1]
}

func extractFileFromRawID(rawID string) string {
	parts := strings.Split(rawID, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}
