package core_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestInputAndConfigFieldsHaveComments(t *testing.T) {
	root := filepath.Join("..", "..")
	configs := map[string]bool{
		"internal/core.Config":              false,
		"internal/core.ListProjectsOptions": false,
		"internal/transport.Config":         false,
		"storage.ObjectConfig":              false,
		"storage.ObjectCredentials":         false,
	}
	var missing []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".claude", "testdata", "examples", "livetest":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		if ast.IsGenerated(file) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			group, ok := decl.(*ast.GenDecl)
			if !ok || group.Tok != token.TYPE {
				continue
			}
			for _, spec := range group.Specs {
				typeSpec := spec.(*ast.TypeSpec)
				name := typeSpec.Name.Name
				key := filepath.ToSlash(filepath.Dir(rel)) + "." + name
				_, allowed := configs[key]
				if !ast.IsExported(name) || (!strings.HasSuffix(name, "Input") && !allowed) {
					continue
				}
				structure, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				if allowed {
					configs[key] = true
				}
				for _, field := range structure.Fields.List {
					if field.Doc != nil || field.Comment != nil {
						continue
					}
					names := field.Names
					if len(names) == 0 {
						expr := field.Type
						if star, ok := expr.(*ast.StarExpr); ok {
							expr = star.X
						}
						switch expr := expr.(type) {
						case *ast.Ident:
							names = []*ast.Ident{expr}
						case *ast.SelectorExpr:
							names = []*ast.Ident{expr.Sel}
						}
					}
					for _, fieldName := range names {
						if ast.IsExported(fieldName.Name) {
							line := fset.Position(fieldName.Pos()).Line
							missing = append(missing, fmt.Sprintf("%s:%d %s.%s", filepath.ToSlash(rel), line, name, fieldName.Name))
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var absent []string
	for name, found := range configs {
		if !found {
			absent = append(absent, name)
		}
	}
	sort.Strings(absent)
	if len(absent) > 0 {
		t.Errorf("allowlisted config types not found: %s", strings.Join(absent, ", "))
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d exported input fields lack comments:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
