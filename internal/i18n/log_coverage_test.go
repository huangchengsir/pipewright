package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// This source contract prevents a new untranslated process message from quietly
// restoring mixed-language diagnostics. It does not inspect pipeline log streams.
func TestOwnedProcessLogsHaveCatalogEntries(t *testing.T) {
	root := filepath.Join("..", "..")
	used := map[string]bool{}
	check := func(file string, arg ast.Expr, prefix string) {
		t.Helper()
		literal, ok := arg.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			t.Errorf("untracked dynamic process template in %s", file)
			return
		}
		source, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatal(err)
		}
		source = prefix + source
		if _, ok := processLogChinese[source]; !ok {
			t.Errorf("process template missing catalog entry in %s: %q", file, source)
		}
		used[source] = true
	}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			aliases := map[string]string{}
			for _, imp := range file.Imports {
				imported, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return err
				}
				if imported != "log" && imported != "github.com/huangchengsir/pipewright/internal/logging" {
					continue
				}
				alias := "log"
				if imported != "log" {
					alias = "logging"
				}
				if imp.Name != nil {
					alias = imp.Name.Name
				}
				aliases[alias] = imported
			}
			relative, _ := filepath.Rel(root, path)
			relative = filepath.ToSlash(relative)
			for _, decl := range file.Decls {
				function, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				ast.Inspect(function, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok || len(call.Args) == 0 {
						return true
					}
					if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "debugf" && relative == "internal/pacloader/pacloader.go" {
						check(relative, call.Args[0], "[pac] ")
						return true
					}
					selected, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					receiver, ok := selected.X.(*ast.Ident)
					if !ok {
						return true
					}
					imported, ok := aliases[receiver.Name]
					if !ok {
						return true
					}
					if imported == "log" {
						if relative != "internal/logging/logging.go" {
							t.Errorf("owned process log bypasses locale wrapper in %s", relative)
						}
						return true
					}
					if selected.Sel.Name != "Printf" && selected.Sel.Name != "Fatalf" {
						t.Errorf("unverified logging method %s in %s", selected.Sel.Name, relative)
						return true
					}
					if relative == "internal/pacloader/pacloader.go" && function.Name.Name == "debugf" {
						binary, ok := call.Args[0].(*ast.BinaryExpr)
						if !ok || binary.Op != token.ADD {
							t.Error("PAC wrapper must preserve static prefix")
							return true
						}
						a, ok := binary.X.(*ast.BasicLit)
						b, bok := binary.Y.(*ast.Ident)
						if !ok || a.Value != `"[pac] "` || !bok || b.Name != "format" {
							t.Error("PAC wrapper changed its bounded format forwarding")
						}
						return true
					}
					check(relative, call.Args[0], "")
					return true
				})
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for source := range processLogChinese {
		if !used[source] {
			t.Errorf("orphaned process template: %q", source)
		}
	}
}
