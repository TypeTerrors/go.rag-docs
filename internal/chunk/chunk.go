package chunk

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SourceKind identifies where a package originated.
type SourceKind string

const (
	SourceProject    SourceKind = "project"
	SourceThirdParty SourceKind = "third-party"
	SourceStdlib     SourceKind = "stdlib"
)

// PackageSource represents a package that should be chunked.
type PackageSource struct {
	ModulePath    string
	ModuleVersion string
	ModuleDir     string
	ImportPath    string
	Dir           string
	Kind          SourceKind
}

// Chunk is the unit of text emitted for RAG ingestion.
type Chunk struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Metadata Metadata `json:"metadata"`
}

// Metadata provides AnythingLLM with contextual details on a chunk.
type Metadata struct {
	Path          string `json:"path"`
	PackageName   string `json:"package"`
	ImportPath    string `json:"importPath"`
	ModulePath    string `json:"module"`
	ModuleVersion string `json:"moduleVersion,omitempty"`
	Symbol        string `json:"symbol,omitempty"`
	Kind          string `json:"kind"`
	Source        string `json:"source"`
}

// Build walks the provided package sources and returns extracted chunks.
func Build(sources []PackageSource) ([]Chunk, error) {
	var all []Chunk
	for _, src := range sources {
		chunks, err := buildForPackage(src)
		if err != nil {
			return nil, err
		}
		all = append(all, chunks...)
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Metadata.ModulePath != all[j].Metadata.ModulePath {
			return all[i].Metadata.ModulePath < all[j].Metadata.ModulePath
		}
		if all[i].Metadata.Path != all[j].Metadata.Path {
			return all[i].Metadata.Path < all[j].Metadata.Path
		}
		return all[i].ID < all[j].ID
	})
	return all, nil
}

func buildForPackage(src PackageSource) ([]Chunk, error) {
	dirEntries, err := os.ReadDir(src.Dir)
	if err != nil {
		return nil, err
	}

	var goFiles []string
	for _, entry := range dirEntries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if shouldSkipFile(name) {
			continue
		}
		goFiles = append(goFiles, filepath.Join(src.Dir, name))
	}
	sort.Strings(goFiles)

	var chunks []Chunk
	for _, file := range goFiles {
		fileChunks, err := parseFile(src, file)
		if err != nil {
			return nil, fmt.Errorf("chunk %s: %w", file, err)
		}
		chunks = append(chunks, fileChunks...)
	}
	return chunks, nil
}

func shouldSkipFile(name string) bool {
	switch {
	case strings.HasSuffix(name, "_test.go"),
		strings.HasSuffix(name, "_mock.go"),
		strings.HasSuffix(name, "_generated.go"),
		strings.Contains(name, ".pb.go"),
		strings.Contains(name, "_pb2.go"):
		return true
	default:
		return false
	}
}

func parseFile(src PackageSource, filePath string) ([]Chunk, error) {
	fset := token.NewFileSet()
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	file, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	fileRel := relativePath(src.ModuleDir, filePath)
	var chunks []Chunk

	if doc := commentText(file.Doc); doc != "" {
		text := doc
		if strings.TrimSpace(text) != "" {
			chunks = append(chunks, Chunk{
				ID:   fmt.Sprintf("%s:%s:file-doc", fileRel, file.Name.Name),
				Text: strings.TrimSpace(text),
				Metadata: Metadata{
					Path:          fileRel,
					PackageName:   file.Name.Name,
					ImportPath:    src.ImportPath,
					ModulePath:    src.ModulePath,
					ModuleVersion: src.ModuleVersion,
					Kind:          "file-doc",
					Source:        string(src.Kind),
				},
			})
		}
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			chunks = append(chunks, buildFuncChunk(src, fileRel, file.Name.Name, fset, content, d))
		case *ast.GenDecl:
			chunks = append(chunks, buildGenChunks(src, fileRel, file.Name.Name, fset, content, d)...)
		default:
			continue
		}
	}

	return chunks, nil
}

func buildFuncChunk(src PackageSource, path, pkg string, fset *token.FileSet, content []byte, decl *ast.FuncDecl) Chunk {
	symbol := decl.Name.Name
	if decl.Recv != nil {
		recv := formatReceiver(decl.Recv.List)
		symbol = fmt.Sprintf("func (%s) %s", recv, decl.Name.Name)
	} else {
		symbol = fmt.Sprintf("func %s", decl.Name.Name)
	}

	text := extractSnippet(fset, content, decl.Pos(), decl.End())
	doc := commentText(decl.Doc)

	var buf bytes.Buffer
	if doc != "" {
		buf.WriteString(strings.TrimSpace(doc))
		buf.WriteString("\n\n")
	}
	buf.WriteString(text)

	id := fmt.Sprintf("%s:%s", path, decl.Name.Name)
	return Chunk{
		ID:   id,
		Text: buf.String(),
		Metadata: Metadata{
			Path:          path,
			PackageName:   pkg,
			ImportPath:    src.ImportPath,
			ModulePath:    src.ModulePath,
			ModuleVersion: src.ModuleVersion,
			Symbol:        symbol,
			Kind:          "function",
			Source:        string(src.Kind),
		},
	}
}

func buildGenChunks(src PackageSource, path, pkg string, fset *token.FileSet, content []byte, decl *ast.GenDecl) []Chunk {
	if len(decl.Specs) == 0 {
		return nil
	}

	var chunks []Chunk
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			snippet := extractSnippet(fset, content, s.Pos(), s.End())
			doc := gatherDoc(decl.Doc, s.Doc)
			var buf bytes.Buffer
			if doc != "" {
				buf.WriteString(doc)
				buf.WriteString("\n\n")
			}
			buf.WriteString(snippet)

			id := fmt.Sprintf("%s:type:%s", path, s.Name.Name)
			chunks = append(chunks, Chunk{
				ID:   id,
				Text: buf.String(),
				Metadata: Metadata{
					Path:          path,
					PackageName:   pkg,
					ImportPath:    src.ImportPath,
					ModulePath:    src.ModulePath,
					ModuleVersion: src.ModuleVersion,
					Symbol:        fmt.Sprintf("type %s", s.Name.Name),
					Kind:          "type",
					Source:        string(src.Kind),
				},
			})
		case *ast.ValueSpec:
			// group value specs to reduce noise.
			if len(s.Names) == 0 {
				continue
			}
			snippet := extractSnippet(fset, content, s.Pos(), s.End())
			doc := gatherDoc(decl.Doc, s.Doc)
			var buf bytes.Buffer
			if doc != "" {
				buf.WriteString(doc)
				buf.WriteString("\n\n")
			}
			buf.WriteString(snippet)

			nameParts := make([]string, len(s.Names))
			for i, name := range s.Names {
				nameParts[i] = name.Name
			}
			symbol := fmt.Sprintf("%s %s", strings.ToLower(decl.Tok.String()), strings.Join(nameParts, ", "))
			id := fmt.Sprintf("%s:%s:%s", path, strings.ToLower(decl.Tok.String()), strings.Join(nameParts, ","))

			chunks = append(chunks, Chunk{
				ID:   id,
				Text: buf.String(),
				Metadata: Metadata{
					Path:          path,
					PackageName:   pkg,
					ImportPath:    src.ImportPath,
					ModulePath:    src.ModulePath,
					ModuleVersion: src.ModuleVersion,
					Symbol:        symbol,
					Kind:          strings.ToLower(decl.Tok.String()),
					Source:        string(src.Kind),
				},
			})
		default:
			continue
		}
	}
	return chunks
}

func extractSnippet(fset *token.FileSet, content []byte, start, end token.Pos) string {
	startPos := fset.PositionFor(start, true).Offset
	endPos := fset.PositionFor(end, true).Offset
	if startPos < 0 {
		startPos = 0
	}
	if endPos > len(content) {
		endPos = len(content)
	}
	return strings.TrimSpace(string(content[startPos:endPos]))
}

func commentText(g *ast.CommentGroup) string {
	if g == nil {
		return ""
	}
	return strings.TrimSpace(g.Text())
}

func gatherDoc(groups ...*ast.CommentGroup) string {
	var parts []string
	for _, g := range groups {
		text := commentText(g)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func formatReceiver(list []*ast.Field) string {
	if len(list) == 0 {
		return ""
	}
	var parts []string
	for _, f := range list {
		var names []string
		for _, name := range f.Names {
			names = append(names, name.Name)
		}
		parts = append(parts, fmt.Sprintf("%s %s", strings.Join(names, ", "), exprString(f.Type)))
	}
	return strings.Join(parts, ", ")
}

func exprString(expr ast.Expr) string {
	var buf bytes.Buffer
	if err := formatNode(&buf, expr); err != nil {
		return ""
	}
	return buf.String()
}

func formatNode(buf *bytes.Buffer, node ast.Node) error {
	fset := token.NewFileSet()
	if err := printer.Fprint(buf, fset, node); err != nil {
		return err
	}
	return nil
}

func relativePath(root, path string) string {
	if root == "" {
		return path
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
