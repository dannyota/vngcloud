package core_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestInputCommentCheckerResolvesTypes(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module fixture\n\ngo 1.27\n",
		"input.go": `package fixture
import "fixture/shared"
type hiddenInput struct { Field string }
type Payload struct { Exposed string }
type aliasInput = Payload
type namedInput Payload
type foreignInput shared.Payload
type Container[T any] struct { Value T }
type genericInput Container[string]
type embeddedInput struct { Container[string] }
type unresolvedInput Missing
`,
		"shared/payload.go": "package shared\ntype Payload struct { Foreign string }\n",
	}
	checker, err := os.ReadFile("doc_comments_test.go")
	if err != nil {
		t.Fatal(err)
	}
	files["internal/core/doc_comments_test.go"] = string(checker)
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "./internal/core", "-run", "^TestInputAndConfigFieldsHaveComments$", "-count=1")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOTOOLCHAIN=local", "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("checker accepted undocumented and unresolved inputs")
	}
	for _, want := range []string{"hiddenInput.Field", "aliasInput.Exposed", "namedInput.Exposed", "foreignInput.Foreign", "genericInput.Value", "embeddedInput.Container", "unresolvedInput", "cannot resolve"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("checker output lacks %q:\n%s", want, output)
		}
	}
}

type commentType struct {
	spec *ast.TypeSpec
	file *ast.File
	dir  string
	pkg  string
}

func TestInputAndConfigFieldsHaveComments(t *testing.T) {
	root := filepath.Join("..", "..")
	moduleFile, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	var module string
	for _, line := range strings.Split(string(moduleFile), "\n") {
		if strings.HasPrefix(line, "module ") {
			module = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			break
		}
	}
	if module == "" {
		t.Fatal("go.mod has no module path")
	}
	configs := map[string]bool{
		"internal/core.Config":              false,
		"internal/core.ListProjectsOptions": false,
		"internal/transport.Config":         false,
		"storage.ObjectConfig":              false,
		"storage.ObjectCredentials":         false,
	}
	fset := token.NewFileSet()
	types := map[string]*commentType{}
	packages := map[string]string{}
	var declarations []*commentType
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
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
		dir := filepath.ToSlash(filepath.Dir(rel))
		packages[dir] = file.Name.Name
		for _, decl := range file.Decls {
			group, ok := decl.(*ast.GenDecl)
			if !ok || group.Tok != token.TYPE {
				continue
			}
			for _, spec := range group.Specs {
				ts := spec.(*ast.TypeSpec)
				item := &commentType{spec: ts, file: file, dir: dir, pkg: file.Name.Name}
				key := dir + "|" + file.Name.Name + "|" + ts.Name.Name
				if types[key] != nil {
					return fmt.Errorf("duplicate type %s in %s", ts.Name.Name, dir)
				}
				types[key] = item
				declarations = append(declarations, item)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var resolve func(*commentType, ast.Expr, map[string]bool) (*ast.StructType, error)
	resolve = func(owner *commentType, expr ast.Expr, seen map[string]bool) (*ast.StructType, error) {
		switch value := expr.(type) {
		case *ast.StructType:
			return value, nil
		case *ast.ParenExpr:
			return resolve(owner, value.X, seen)
		case *ast.IndexExpr:
			return resolve(owner, value.X, seen)
		case *ast.IndexListExpr:
			return resolve(owner, value.X, seen)
		case *ast.Ident:
			key := owner.dir + "|" + owner.pkg + "|" + value.Name
			if seen[key] {
				return nil, fmt.Errorf("cyclic type %s", value.Name)
			}
			target := types[key]
			if target == nil {
				return nil, fmt.Errorf("type %s not found in %s", value.Name, owner.dir)
			}
			seen[key] = true
			return resolve(target, target.spec.Type, seen)
		case *ast.SelectorExpr:
			qualifier, ok := value.X.(*ast.Ident)
			if !ok {
				return nil, errors.New("unsupported selector")
			}
			for _, imp := range owner.file.Imports {
				path, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					return nil, err
				}
				dir := "."
				if path != module {
					if !strings.HasPrefix(path, module+"/") {
						continue
					}
					dir = strings.TrimPrefix(path, module+"/")
				}
				name := packages[dir]
				if imp.Name != nil {
					name = imp.Name.Name
				}
				if name != qualifier.Name {
					continue
				}
				key := dir + "|" + packages[dir] + "|" + value.Sel.Name
				if seen[key] {
					return nil, fmt.Errorf("cyclic type %s.%s", qualifier.Name, value.Sel.Name)
				}
				target := types[key]
				if target == nil {
					return nil, fmt.Errorf("type %s.%s not found", qualifier.Name, value.Sel.Name)
				}
				seen[key] = true
				return resolve(target, target.spec.Type, seen)
			}
			return nil, fmt.Errorf("module import %s not found", qualifier.Name)
		default:
			return nil, fmt.Errorf("type is not a resolvable struct (%T)", expr)
		}
	}
	var problems []string
	for _, item := range declarations {
		name := item.spec.Name.Name
		key := item.dir + "." + name
		_, allowed := configs[key]
		if !strings.HasSuffix(name, "Input") && !allowed {
			continue
		}
		structure, err := resolve(item, item.spec.Type, map[string]bool{})
		if err != nil {
			pos := fset.Position(item.spec.Pos())
			rel, _ := filepath.Rel(root, pos.Filename)
			problems = append(problems, fmt.Sprintf("%s:%d %s: cannot resolve: %v", filepath.ToSlash(rel), pos.Line, name, err))
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
			embeddedType:
				for {
					switch value := expr.(type) {
					case *ast.StarExpr:
						expr = value.X
					case *ast.ParenExpr:
						expr = value.X
					case *ast.IndexExpr:
						expr = value.X
					case *ast.IndexListExpr:
						expr = value.X
					default:
						break embeddedType
					}
				}
				switch value := expr.(type) {
				case *ast.Ident:
					names = []*ast.Ident{value}
				case *ast.SelectorExpr:
					names = []*ast.Ident{value.Sel}
				}
			}
			for _, fieldName := range names {
				if ast.IsExported(fieldName.Name) {
					pos := fset.Position(fieldName.Pos())
					rel, _ := filepath.Rel(root, pos.Filename)
					problems = append(problems, fmt.Sprintf("%s:%d %s.%s", filepath.ToSlash(rel), pos.Line, name, fieldName.Name))
				}
			}
		}
	}
	for name, found := range configs {
		if !found {
			problems = append(problems, "allowlisted config type not found: "+name)
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Errorf("%d input comment problems:\n%s", len(problems), strings.Join(problems, "\n"))
	}
}
