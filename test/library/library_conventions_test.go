package library_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

const modulePath = "github.com/tiaot33/mechbox"

// AGENTS.md 与 docs/testing.md 里写明的分层、依赖方向、注释和文件命名约定在这里做静态检查,秒级拦下违例。
func TestCodebaseConventions(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	t.Run("dependency direction", func(t *testing.T) {
		t.Parallel()
		checkDependencyDirection(t, root)
	})
	t.Run("comments", func(t *testing.T) {
		t.Parallel()
		checkCommentConventions(t, root)
	})
	t.Run("test file names", func(t *testing.T) {
		t.Parallel()
		checkLibraryTestFileNames(t, root)
	})
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("missing go.mod at %s: %v", root, err)
	}
	return root
}

func walkGoFiles(t *testing.T, root, rel string, visit func(relPath, fullPath string)) {
	t.Helper()
	base := filepath.Join(root, filepath.FromSlash(rel))
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata" || name == "dist" || name == "bindings" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		r, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		visit(filepath.ToSlash(r), path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", rel, err)
	}
}

func checkDependencyDirection(t *testing.T, root string) {
	fset := token.NewFileSet()
	platformtestPkg := modulePath + "/internal/library/platformtest"
	cmdPrefix := modulePath + "/cmd/"
	testOnlyExports := map[string]string{
		modulePath + "/internal/library/internal/wsl":      "Emulate",
		modulePath + "/internal/library/internal/agentdir": "WithoutModes",
		modulePath + "/internal/library/internal/proxy":    "Emulate",
	}

	for _, dir := range []string{"internal", "cmd", "test"} {
		walkGoFiles(t, root, dir, func(relPath, fullPath string) {
			isTest := strings.HasSuffix(relPath, "_test.go")
			inPlatformtest := strings.HasPrefix(relPath, "internal/library/platformtest/")
			mode := parser.ImportsOnly
			if !isTest && !inPlatformtest && strings.HasPrefix(relPath, "internal/library/") {
				mode = 0
			}
			file, err := parser.ParseFile(fset, fullPath, nil, mode)
			if err != nil {
				t.Fatalf("parse %s: %v", relPath, err)
			}

			aliasToTestOnly := map[string]string{}
			for _, imp := range file.Imports {
				pkg, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					continue
				}
				line := fset.Position(imp.Pos()).Line
				if strings.HasPrefix(relPath, "internal/library/") && (pkg == modulePath+"/cmd" || strings.HasPrefix(pkg, cmdPrefix)) {
					t.Errorf("%s:%d: internal/library 不能依赖应用包 %s (AGENTS.md「依赖方向」)", relPath, line, pkg)
				}
				if !isTest && !inPlatformtest && (pkg == platformtestPkg || strings.HasPrefix(pkg, platformtestPkg+"/")) {
					t.Errorf("%s:%d: 非测试代码不能导入 platformtest (%s)", relPath, line, pkg)
				}
				if symbol, ok := testOnlyExports[pkg]; ok && !isTest && !inPlatformtest {
					name := pkg[strings.LastIndex(pkg, "/")+1:]
					if imp.Name != nil {
						name = imp.Name.Name
					}
					if name != "_" && name != "." {
						aliasToTestOnly[name] = symbol
					}
				}
			}

			if len(aliasToTestOnly) > 0 {
				ast.Inspect(file, func(n ast.Node) bool {
					sel, ok := n.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					ident, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					if want, found := aliasToTestOnly[ident.Name]; found && sel.Sel.Name == want {
						line := fset.Position(sel.Pos()).Line
						t.Errorf("%s:%d: %s.%s 只供 platformtest 调用,非测试业务代码不能调用", relPath, line, ident.Name, want)
					}
					return true
				})
			}
		})
	}
}

func checkCommentConventions(t *testing.T, root string) {
	fset := token.NewFileSet()

	walkGoFiles(t, root, "internal/library/internal", func(relPath, fullPath string) {
		if strings.HasSuffix(relPath, "_test.go") {
			return
		}
		file, err := parser.ParseFile(fset, fullPath, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", relPath, err)
		}
		cgoPreamble := cgoPreambles(file)
		for _, cg := range file.Comments {
			if cgoPreamble[cg] || isDirectiveOnly(cg) {
				continue
			}
			line := fset.Position(cg.Pos()).Line
			t.Errorf("%s:%d: internal/library/internal 子包的非测试代码不写注释 (AGENTS.md「注释」): %s", relPath, line, firstCommentLine(cg))
		}
	})

	entries, err := os.ReadDir(filepath.Join(root, "internal", "library"))
	if err != nil {
		t.Fatalf("read internal/library: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		relPath := "internal/library/" + entry.Name()
		fullPath := filepath.Join(root, "internal", "library", entry.Name())
		file, err := parser.ParseFile(fset, fullPath, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", relPath, err)
		}

		allowed := cgoPreambles(file)
		if entry.Name() == "doc.go" && file.Doc != nil {
			allowed[file.Doc] = true
		}

		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !isExportedFuncOrMethod(d) {
					continue
				}
				line := fset.Position(d.Pos()).Line
				if d.Doc == nil {
					t.Errorf("%s:%d: 导出的函数或方法 %s 缺少中文 doc comment (AGENTS.md「注释」)", relPath, line, d.Name.Name)
					continue
				}
				allowed[d.Doc] = true
				requireChinese(t, relPath, fset.Position(d.Doc.Pos()).Line, d.Name.Name, d.Doc.Text())

			case *ast.GenDecl:
				if d.Tok != token.TYPE && d.Tok != token.CONST && d.Tok != token.VAR {
					continue
				}
				hasExported := false
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if !ast.IsExported(s.Name.Name) {
							continue
						}
						hasExported = true
						doc := s.Doc
						if doc == nil {
							doc = d.Doc
						}
						line := fset.Position(s.Pos()).Line
						if doc == nil {
							t.Errorf("%s:%d: 导出的类型 %s 缺少中文 doc comment (AGENTS.md「注释」)", relPath, line, s.Name.Name)
						} else {
							if s.Doc != nil {
								allowed[s.Doc] = true
							}
							requireChinese(t, relPath, fset.Position(doc.Pos()).Line, s.Name.Name, doc.Text())
						}
						allowExportedTypeMemberComments(s.Type, allowed)

					case *ast.ValueSpec:
						for _, name := range s.Names {
							if !ast.IsExported(name.Name) {
								continue
							}
							hasExported = true
							if s.Doc != nil {
								allowed[s.Doc] = true
							}
							if s.Comment != nil {
								allowed[s.Comment] = true
							}
							doc := firstNonNil(s.Doc, s.Comment, d.Doc)
							line := fset.Position(name.Pos()).Line
							if doc == nil {
								t.Errorf("%s:%d: 导出的常量或变量 %s 缺少中文 doc comment (AGENTS.md「注释」)", relPath, line, name.Name)
							} else {
								requireChinese(t, relPath, fset.Position(doc.Pos()).Line, name.Name, doc.Text())
							}
						}
					}
				}
				if hasExported && d.Doc != nil {
					allowed[d.Doc] = true
				}
			}
		}

		for _, cg := range file.Comments {
			if allowed[cg] || isDirectiveOnly(cg) {
				continue
			}
			line := fset.Position(cg.Pos()).Line
			t.Errorf("%s:%d: library 包非导出的实现不写注释 (AGENTS.md「注释」): %s", relPath, line, firstCommentLine(cg))
		}
	}
}

func checkLibraryTestFileNames(t *testing.T, root string) {
	libEntries, err := os.ReadDir(filepath.Join(root, "internal", "library"))
	if err != nil {
		t.Fatalf("read internal/library: %v", err)
	}
	var sourceStems []string
	for _, entry := range libEntries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "doc.go" {
			continue
		}
		sourceStems = append(sourceStems, strings.TrimSuffix(name, ".go"))
	}

	sourceDirEntries, err := os.ReadDir(filepath.Join(root, "internal", "library", "internal", "source"))
	if err != nil {
		t.Fatalf("read internal/library/internal/source: %v", err)
	}
	helperSourcePkgs := map[string]bool{"catalog": true, "discovery": true, "gitcmd": true}
	var sourceKinds []string
	for _, entry := range sourceDirEntries {
		if entry.IsDir() && !helperSourcePkgs[entry.Name()] {
			sourceKinds = append(sourceKinds, entry.Name())
		}
	}

	testEntries, err := os.ReadDir(filepath.Join(root, "test", "library"))
	if err != nil {
		t.Fatalf("read test/library: %v", err)
	}
	for _, entry := range testEntries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") || name == "test_helpers_test.go" {
			continue
		}
		stem := strings.TrimSuffix(name, "_test.go")
		if matchesLibraryTestStem(stem, sourceStems, sourceKinds) {
			continue
		}
		t.Errorf(
			"test/library/%s: 文件名前缀不符合 docs/testing.md 约定(应对应 internal/library/<源文件>_*.go、source_<来源>_test.go 或 library_<方面>_test.go)",
			name,
		)
	}
}

func matchesLibraryTestStem(stem string, sourceStems, sourceKinds []string) bool {
	if strings.HasPrefix(stem, "library_") && len(stem) > len("library_") {
		return true
	}
	if strings.HasPrefix(stem, "source_") {
		kind := strings.TrimPrefix(stem, "source_")
		return slices.ContainsFunc(sourceKinds, func(k string) bool {
			return kind == k || strings.HasPrefix(kind, k+"_")
		})
	}
	return slices.ContainsFunc(sourceStems, func(s string) bool {
		return stem == s || strings.HasPrefix(stem, s+"_")
	})
}

func isExportedFuncOrMethod(d *ast.FuncDecl) bool {
	if !ast.IsExported(d.Name.Name) {
		return false
	}
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return true
	}
	typ := d.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if idx, ok := typ.(*ast.IndexExpr); ok {
		typ = idx.X
	}
	ident, ok := typ.(*ast.Ident)
	return ok && ast.IsExported(ident.Name)
}

func allowExportedTypeMemberComments(expr ast.Expr, allowed map[*ast.CommentGroup]bool) {
	switch tp := expr.(type) {
	case *ast.StructType:
		if tp.Fields == nil {
			return
		}
		for _, field := range tp.Fields.List {
			if field.Doc != nil {
				allowed[field.Doc] = true
			}
			if field.Comment != nil {
				allowed[field.Comment] = true
			}
		}
	case *ast.InterfaceType:
		if tp.Methods == nil {
			return
		}
		for _, method := range tp.Methods.List {
			if method.Doc != nil {
				allowed[method.Doc] = true
			}
			if method.Comment != nil {
				allowed[method.Comment] = true
			}
		}
	}
}

func firstNonNil(groups ...*ast.CommentGroup) *ast.CommentGroup {
	for _, g := range groups {
		if g != nil {
			return g
		}
	}
	return nil
}

func isDirectiveOnly(cg *ast.CommentGroup) bool {
	for _, c := range cg.List {
		if !strings.HasPrefix(c.Text, "//go:") {
			return false
		}
	}
	return true
}

func cgoPreambles(file *ast.File) map[*ast.CommentGroup]bool {
	allowed := map[*ast.CommentGroup]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}
		for _, spec := range gen.Specs {
			imp, ok := spec.(*ast.ImportSpec)
			if !ok || imp.Path.Value != `"C"` {
				continue
			}
			if gen.Doc != nil {
				allowed[gen.Doc] = true
			}
			if imp.Doc != nil {
				allowed[imp.Doc] = true
			}
		}
	}
	return allowed
}

func firstCommentLine(cg *ast.CommentGroup) string {
	if cg == nil || len(cg.List) == 0 {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(cg.List[0].Text), "\n")
	return line
}

func requireChinese(t *testing.T, relPath string, line int, symbol, text string) {
	t.Helper()
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return
		}
	}
	t.Errorf("%s:%d: 导出项 %s 的注释应使用中文 (AGENTS.md「注释」)", relPath, line, symbol)
}
