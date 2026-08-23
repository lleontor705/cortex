// Package ast provides zero-CGO static code analysis, rich symbol extraction,
// and cross-file call graph resolution for Go, TypeScript/JavaScript, Python,
// Rust, SQL, and polyglot codebases.
package ast

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/domain/code"
)

// CodeEntity represents a structural symbol discovered in source code with rich semantic metadata.
type CodeEntity struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Kind        string                 `json:"kind"` // "func", "method", "struct", "interface", "class", "module", "table", "enum", "type"
	File        string                 `json:"file"`
	Line        int                    `json:"line"`
	EndLine     int                    `json:"end_line,omitempty"`
	StartCol    int                    `json:"start_col,omitempty"`
	EndCol      int                    `json:"end_col,omitempty"`
	Package     string                 `json:"package,omitempty"`
	ParentID    string                 `json:"parent_id,omitempty"`
	Visibility  string                 `json:"visibility,omitempty"` // "public", "private", "protected", "internal"
	Signature   string                 `json:"signature,omitempty"`
	DocSummary  string                 `json:"doc_summary,omitempty"`
	Parameters  []code.Parameter       `json:"parameters,omitempty"`
	ReturnType  string                 `json:"return_type,omitempty"`
	Complexity  int                    `json:"complexity,omitempty"`
	Metadata    map[string]any         `json:"metadata,omitempty"`
}

// CodeRelationship represents a directed architectural edge between two code entities.
type CodeRelationship struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Relation   string  `json:"relation"` // "calls", "imports", "implements", "defines", "uses", "contains", "extends", "instantiates", "uses_type", "references", "exports"
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning,omitempty"`
}

// ImportFact records an import statement in a source file for global cross-file symbol resolution.
type ImportFact struct {
	SourceFile   string `json:"source_file"`
	ImportPath   string `json:"import_path"`   // e.g. "./utils", "github.com/user/pkg", "os"
	LocalName    string `json:"local_name"`    // Local binding name (e.g. "foo" in `import { foo }`)
	ImportedName string `json:"imported_name"` // Original exported name (e.g. "bar" in `import { bar as foo }`)
	IsWildcard   bool   `json:"is_wildcard"`   // `import * as X` or Go dot import
	Line         int    `json:"line"`
}

// ExportFact records an exported symbol for cross-file linkage.
type ExportFact struct {
	SourceFile   string `json:"source_file"`
	ExportedName string `json:"exported_name"`
	SymbolID     string `json:"symbol_id"`
	IsDefault    bool   `json:"is_default"`
	Line         int    `json:"line"`
}

// ExtractionResult is the output of analyzing a single file or directory.
type ExtractionResult struct {
	Entities      []CodeEntity       `json:"entities"`
	Relationships []CodeRelationship `json:"relationships"`
	Imports       []ImportFact       `json:"imports"`
	Exports       []ExportFact       `json:"exports"`
	FilesScanned  int                `json:"files_scanned"`
}

// Extractor performs static AST and multi-pass symbol analysis.
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

	ext := strings.ToLower(filepath.Ext(filePath))
	res := extractByExtension(ext, filePath, relPath)
	res.FilesScanned = 1
	return res, nil
}

// isIgnoredDir returns true if the directory name matches known artifact or dependency folders.
func isIgnoredDir(name string) bool {
	if name != "." && name != ".." && strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "node_modules", "vendor", "dist", "build", "out", "target", "bin", "obj",
		"__pycache__", ".venv", "venv", ".gradle", ".idea", ".vs", ".vscode",
		"cmake-build-debug", "cmake-build-release", "packages", ".git", ".next":
		return true
	}
	return false
}

// extractByExtension routes source files to their dedicated zero-CGO AST/symbol extractor.
func extractByExtension(ext, fullPath, relPath string) *ExtractionResult {
	switch ext {
	case ".go":
		return extractGoFile(fullPath, relPath)
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return extractTSJSFile(fullPath, relPath)
	case ".py", ".pyw":
		return extractPythonFile(fullPath, relPath)
	case ".sql":
		return extractSQLFile(fullPath, relPath)
	case ".rs":
		return extractRustFile(fullPath, relPath)
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
	case ".c", ".cpp", ".cc", ".cxx", ".c++", ".h", ".hpp", ".hxx", ".h++":
		return extractCppFile(fullPath, relPath)
	case ".php", ".phtml":
		return extractPhpFile(fullPath, relPath)
	case ".rb", ".rake":
		return extractRubyFile(fullPath, relPath)
	case ".swift":
		return extractSwiftFile(fullPath, relPath)
	default:
		return &ExtractionResult{}
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
		Imports:       make([]ImportFact, 0),
		Exports:       make([]ExportFact, 0),
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
		res := extractByExtension(ext, path, relPath)
		if len(res.Entities) > 0 || len(res.Relationships) > 0 || len(res.Imports) > 0 {
			for _, ent := range res.Entities {
				if !entityMap[ent.ID] {
					entityMap[ent.ID] = true
					result.Entities = append(result.Entities, ent)
				}
			}
			result.Relationships = append(result.Relationships, res.Relationships...)
			result.Imports = append(result.Imports, res.Imports...)
			result.Exports = append(result.Exports, res.Exports...)
			result.FilesScanned++
		}

		return nil
	})

	return result, err
}

// ComputeFileHash computes the SHA-256 hash of a file.
func ComputeFileHash(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ExtractCodeGraph runs the high-density polyglot extraction and 2-pass symbol resolution
// to produce a complete, connected *code.CodeGraph.
func (e *Extractor) ExtractCodeGraph(targetPath, project string, maxFiles int) (*code.CodeGraph, error) {
	rawResult, err := e.ExtractPath(targetPath, maxFiles)
	if err != nil {
		return nil, err
	}

	resolver := NewResolver(e.RootPath, project)
	return resolver.Resolve(rawResult), nil
}
