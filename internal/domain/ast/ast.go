// Package ast provides zero-CGO static code analysis and symbol extraction
// for Go, TypeScript/JavaScript, Python, and SQL files, building deterministic
// code knowledge graphs.
package ast

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CodeEntity represents a structural symbol discovered in source code.
type CodeEntity struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // "func", "struct", "interface", "class", "module", "table"
	File      string `json:"file"`
	Line      int    `json:"line"`
	Package   string `json:"package,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// CodeRelationship represents a directed edge between two code entities.
type CodeRelationship struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Relation   string  `json:"relation"` // "calls", "imports", "implements", "defines", "uses"
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning,omitempty"`
}

// ExtractionResult is the output of analyzing a file or directory tree.
type ExtractionResult struct {
	Entities      []CodeEntity       `json:"entities"`
	Relationships []CodeRelationship `json:"relationships"`
	FilesScanned  int                `json:"files_scanned"`
}

// Extractor performs static AST and regex-based symbol analysis.
type Extractor struct {
	RootPath string
}

// NewExtractor creates a new AST extractor rooted at the given path.
func NewExtractor(root string) *Extractor {
	return &Extractor{RootPath: root}
}

// ExtractPath scans either a single file or a directory recursively.
func (e *Extractor) ExtractPath(targetPath string, maxFiles int) (*ExtractionResult, error) {
	fi, err := os.Stat(targetPath)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return e.ExtractFile(targetPath)
	}
	return e.ExtractDir(targetPath, maxFiles)
}

// ExtractFile extracts code entities and relationships for a single file.
func (e *Extractor) ExtractFile(filePath string) (*ExtractionResult, error) {
	relPath, _ := filepath.Rel(e.RootPath, filePath)
	if relPath == "" {
		relPath = filePath
	}
	relPath = filepath.ToSlash(relPath)

	result := &ExtractionResult{
		Entities:      make([]CodeEntity, 0),
		Relationships: make([]CodeRelationship, 0),
		FilesScanned:  1,
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	entities, rels := extractByExtension(ext, filePath, relPath)
	result.Entities = entities
	result.Relationships = rels

	return result, nil
}

// isIgnoredDir returns true if the directory name matches known artifact or dependency folders.
func isIgnoredDir(name string) bool {
	if name != "." && name != ".." && strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "node_modules", "vendor", "dist", "build", "out", "target", "bin", "obj",
		"__pycache__", ".venv", "venv", ".gradle", ".idea", ".vs", ".vscode",
		"cmake-build-debug", "cmake-build-release", "packages":
		return true
	}
	return false
}

// extractByExtension routes source files to their dedicated zero-CGO AST/symbol extractor.
func extractByExtension(ext, fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
	switch ext {
	case ".go":
		return extractGoFile(fullPath, relPath)
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return extractTSJSFile(fullPath, relPath)
	case ".py", ".pyw":
		return extractPythonFile(fullPath, relPath)
	case ".sql":
		return extractSQLFile(fullPath, relPath)
	case ".cs":
		return extractCSharpFile(fullPath, relPath)
	case ".fs", ".fsi", ".fsx":
		return extractFSharpFile(fullPath, relPath)
	case ".vb":
		return extractVBDotNetFile(fullPath, relPath)
	case ".java":
		return extractJavaFile(fullPath, relPath)
	case ".kt", ".kts":
		return extractKotlinFile(fullPath, relPath)
	case ".rs":
		return extractRustFile(fullPath, relPath)
	case ".c", ".cpp", ".cc", ".cxx", ".c++", ".h", ".hpp", ".hxx", ".h++":
		return extractCppFile(fullPath, relPath)
	case ".php", ".phtml":
		return extractPhpFile(fullPath, relPath)
	case ".rb", ".rake":
		return extractRubyFile(fullPath, relPath)
	case ".swift":
		return extractSwiftFile(fullPath, relPath)
	default:
		return nil, nil
	}
}

// ExtractDir scans a directory recursively and extracts code entities and relationships.
func (e *Extractor) ExtractDir(dir string, maxFiles int) (*ExtractionResult, error) {
	if maxFiles <= 0 {
		maxFiles = 500
	}

	result := &ExtractionResult{
		Entities:      make([]CodeEntity, 0),
		Relationships: make([]CodeRelationship, 0),
	}

	entityMap := make(map[string]bool)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if isIgnoredDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		if result.FilesScanned >= maxFiles {
			return filepath.SkipDir
		}

		relPath, _ := filepath.Rel(e.RootPath, path)
		if relPath == "" {
			relPath = path
		}
		relPath = filepath.ToSlash(relPath)

		ext := strings.ToLower(filepath.Ext(path))
		entities, rels := extractByExtension(ext, path, relPath)
		if len(entities) > 0 || len(rels) > 0 {
			for _, ent := range entities {
				if !entityMap[ent.ID] {
					entityMap[ent.ID] = true
					result.Entities = append(result.Entities, ent)
				}
			}
			result.Relationships = append(result.Relationships, rels...)
			result.FilesScanned++
		}

		return nil
	})

	return result, err
}

// extractGoFile uses go/parser and go/ast for pure Go AST extraction.
func extractGoFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
	var entities []CodeEntity
	var rels []CodeRelationship

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, fullPath, nil, parser.ParseComments)
	if err != nil {
		return entities, rels
	}

	pkgName := node.Name.Name
	fileEntityID := fmt.Sprintf("module:%s", relPath)
	entities = append(entities, CodeEntity{
		ID:      fileEntityID,
		Name:    filepath.Base(relPath),
		Kind:    "module",
		File:    relPath,
		Line:    1,
		Package: pkgName,
	})

	// Extract imports
	for _, imp := range node.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		targetID := fmt.Sprintf("pkg:%s", importPath)
		rels = append(rels, CodeRelationship{
			Source:     fileEntityID,
			Target:     targetID,
			Relation:   "imports",
			Confidence: 1.0,
			Reasoning:  "Direct Go import statement",
		})
	}

	// Extract functions, methods, types
	ast.Inspect(node, func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.TypeSpec:
			pos := fset.Position(t.Pos())
			kind := "struct"
			if _, ok := t.Type.(*ast.InterfaceType); ok {
				kind = "interface"
			}
			structID := fmt.Sprintf("type:%s.%s", pkgName, t.Name.Name)
			entities = append(entities, CodeEntity{
				ID:      structID,
				Name:    t.Name.Name,
				Kind:    kind,
				File:    relPath,
				Line:    pos.Line,
				Package: pkgName,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     structID,
				Relation:   "defines",
				Confidence: 1.0,
			})

		case *ast.FuncDecl:
			pos := fset.Position(t.Pos())
			funcName := t.Name.Name
			kind := "func"
			receiver := ""
			if t.Recv != nil && len(t.Recv.List) > 0 {
				kind = "method"
				if ident, ok := t.Recv.List[0].Type.(*ast.Ident); ok {
					receiver = ident.Name
				} else if star, ok := t.Recv.List[0].Type.(*ast.StarExpr); ok {
					if ident, ok := star.X.(*ast.Ident); ok {
						receiver = ident.Name
					}
				}
			}

			funcID := fmt.Sprintf("func:%s.%s", pkgName, funcName)
			if receiver != "" {
				funcID = fmt.Sprintf("method:%s.%s.%s", pkgName, receiver, funcName)
			}

			entities = append(entities, CodeEntity{
				ID:      funcID,
				Name:    funcName,
				Kind:    kind,
				File:    relPath,
				Line:    pos.Line,
				Package: pkgName,
			})

			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     funcID,
				Relation:   "defines",
				Confidence: 1.0,
			})

			if receiver != "" {
				structID := fmt.Sprintf("type:%s.%s", pkgName, receiver)
				rels = append(rels, CodeRelationship{
					Source:     funcID,
					Target:     structID,
					Relation:   "implements",
					Confidence: 1.0,
				})
			}

			// Inspect calls inside function body
			if t.Body != nil {
				ast.Inspect(t.Body, func(bodyNode ast.Node) bool {
					if call, ok := bodyNode.(*ast.CallExpr); ok {
						switch fun := call.Fun.(type) {
						case *ast.Ident:
							targetID := fmt.Sprintf("func:%s.%s", pkgName, fun.Name)
							rels = append(rels, CodeRelationship{
								Source:     funcID,
								Target:     targetID,
								Relation:   "calls",
								Confidence: 0.9,
							})
						case *ast.SelectorExpr:
							if pkgIdent, ok := fun.X.(*ast.Ident); ok {
								targetID := fmt.Sprintf("func:%s.%s", pkgIdent.Name, fun.Sel.Name)
								rels = append(rels, CodeRelationship{
									Source:     funcID,
									Target:     targetID,
									Relation:   "calls",
									Confidence: 0.9,
								})
							}
						}
					}
					return true
				})
			}
		}
		return true
	})

	return entities, rels
}

// Regex definitions for JavaScript/TypeScript, Python and SQL
var (
	tsImportRe = regexp.MustCompile(`import\s+(?:\{[^}]*\}|\*\s+as\s+\w+|\w+)\s+from\s+['"]([^'"]+)['"]`)
	tsFuncRe   = regexp.MustCompile(`(?:export\s+)?(?:async\s+)?function\s+([a-zA-Z0-9_]+)\s*\(`)
	tsClassRe  = regexp.MustCompile(`(?:export\s+)?(?:abstract\s+)?class\s+([a-zA-Z0-9_]+)`)
	tsConstFn  = regexp.MustCompile(`(?:export\s+)?const\s+([a-zA-Z0-9_]+)\s*=\s*(?:async\s*)?\([^)]*\)\s*=>`)

	pyImportRe = regexp.MustCompile(`(?:from\s+([a-zA-Z0-9_.]+)\s+import|import\s+([a-zA-Z0-9_.]+))`)
	pyClassRe  = regexp.MustCompile(`class\s+([a-zA-Z0-9_]+)(?:\(([^)]*)\))?:`)
	pyFuncRe   = regexp.MustCompile(`(?:async\s+)?def\s+([a-zA-Z0-9_]+)\s*\(`)

	sqlTableRe = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:[a-zA-Z0-9_]+\.)?([a-zA-Z0-9_]+)`)
)

func extractTSJSFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
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
		line := strings.TrimSpace(scanner.Text())

		if m := tsImportRe.FindStringSubmatch(line); len(m) > 1 {
			targetID := fmt.Sprintf("module:%s", m[1])
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     targetID,
				Relation:   "imports",
				Confidence: 1.0,
			})
		}

		if m := tsClassRe.FindStringSubmatch(line); len(m) > 1 {
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
		}

		if m := tsFuncRe.FindStringSubmatch(line); len(m) > 1 {
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
		} else if m := tsConstFn.FindStringSubmatch(line); len(m) > 1 {
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

func extractPythonFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
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
		line := strings.TrimSpace(scanner.Text())

		if m := pyImportRe.FindStringSubmatch(line); len(m) > 1 {
			pkg := m[1]
			if pkg == "" && len(m) > 2 {
				pkg = m[2]
			}
			if pkg != "" {
				targetID := fmt.Sprintf("pkg:%s", pkg)
				rels = append(rels, CodeRelationship{
					Source:     fileEntityID,
					Target:     targetID,
					Relation:   "imports",
					Confidence: 1.0,
				})
			}
		}

		if m := pyClassRe.FindStringSubmatch(line); len(m) > 1 {
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
		}

		if m := pyFuncRe.FindStringSubmatch(line); len(m) > 1 {
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

func extractSQLFile(fullPath, relPath string) ([]CodeEntity, []CodeRelationship) {
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

		if m := sqlTableRe.FindStringSubmatch(line); len(m) > 1 {
			tableID := fmt.Sprintf("table:%s", m[1])
			entities = append(entities, CodeEntity{
				ID:   tableID,
				Name: m[1],
				Kind: "table",
				File: relPath,
				Line: lineNum,
			})
			rels = append(rels, CodeRelationship{
				Source:     fileEntityID,
				Target:     tableID,
				Relation:   "defines",
				Confidence: 1.0,
			})
		}
	}

	return entities, rels
}
