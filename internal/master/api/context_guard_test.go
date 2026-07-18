package api_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestProductionHandlersDoNotCreateContextlessDAO(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	root := filepath.Dir(current)
	forbidden := map[string]struct{}{"NewContext": {}, "NewUserContext": {}}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		daoAlias := ""
		for _, imported := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil || importPath != "github.com/VaalaCat/ai-gateway/internal/dao" {
				continue
			}
			daoAlias = "dao"
			if imported.Name != nil {
				daoAlias = imported.Name.Name
			}
		}
		if daoAlias == "" || daoAlias == "_" || daoAlias == "." {
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != daoAlias {
				return true
			}
			if _, blocked := forbidden[selector.Sel.Name]; blocked {
				position := fset.Position(call.Pos())
				t.Errorf("production API handler uses contextless DAO factory %s.%s at %s:%d", daoAlias, selector.Sel.Name, path, position.Line)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
