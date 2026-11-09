package core_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build/constraint"
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
type aliasVariantInput = platformPayload
type documentedAliasInput = documentedPayload
type partiallyBrokenInput = platformOnlyPayload
`,
		"shared/payload.go":   "package shared\ntype Payload struct { Foreign string }\n",
		"platform_windows.go": "//go:build windows\n\npackage fixture\ntype variantInput struct { WindowsField string }\ntype platformPayload struct { WindowsValue string }\ntype documentedInput struct {\n// WindowsField is documented.\nWindowsField string\n}\ntype documentedPayload struct {\n// WindowsValue is documented.\nWindowsValue string\n}\ntype platformOnlyInput platformOnlyPayload\ntype platformOnlyPayload struct {\n// Value is documented.\nValue string\n}\n",
		"platform_other.go":   "//go:build !windows\n\npackage fixture\ntype variantInput struct { OtherField string }\ntype platformPayload struct { OtherValue string }\ntype documentedInput struct {\n// OtherField is documented.\nOtherField string\n}\ntype documentedPayload struct {\n// OtherValue is documented.\nOtherValue string\n}\ntype platformOnlyPayload string\n",
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
	for _, unexpected := range []string{"duplicate type", "documentedInput.", "documentedAliasInput.", "platformOnlyInput"} {
		if strings.Contains(string(output), unexpected) {
			t.Errorf("checker rejected documented build variants (%s):\n%s", unexpected, output)
		}
	}
	for _, want := range []string{"hiddenInput.Field", "aliasInput.Exposed", "namedInput.Exposed", "foreignInput.Foreign", "genericInput.Value", "embeddedInput.Container", "unresolvedInput", "cannot resolve", "partiallyBrokenInput: cannot resolve", "variantInput.WindowsField", "variantInput.OtherField", "aliasVariantInput.WindowsValue", "aliasVariantInput.OtherValue"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("checker output lacks %q:\n%s", want, output)
		}
	}
}

func TestCompatibleBuildConditions(t *testing.T) {
	for _, tc := range []struct {
		expression string
		want       bool
	}{
		{"windows && !windows", false},
		{"windows && linux", false},
		{"windows && unix", false},
		{"android && !linux", false},
		{"android && linux", true},
		{"amd64 && arm64", false},
		{"(windows || linux) && !windows", true},
		{"custom && !custom", false},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			expr, err := constraint.Parse("//go:build " + tc.expression)
			if err != nil {
				t.Fatal(err)
			}
			if got := compatible(expr); got != tc.want {
				t.Errorf("compatible(%s) = %t, want %t", tc.expression, got, tc.want)
			}
		})
	}
	file, err := parser.ParseFile(token.NewFileSet(), "client_windows.go", "package fixture", parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	when, err := buildCondition(file, "client_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	notWindows, err := constraint.Parse("//go:build !windows")
	if err != nil {
		t.Fatal(err)
	}
	if compatible(andCondition(when, notWindows)) {
		t.Error("windows filename was compatible with !windows")
	}
}

type commentType struct {
	spec *ast.TypeSpec
	file *ast.File
	dir  string
	pkg  string
	when constraint.Expr
}

// buildCondition combines explicit build constraints with the GOOS and GOARCH
// filename suffixes that Go applies to source files.
func buildCondition(file *ast.File, path string) (constraint.Expr, error) {
	var explicit constraint.Expr
	var legacy []constraint.Expr
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, comment := range group.List {
			if constraint.IsGoBuild(comment.Text) || constraint.IsPlusBuild(comment.Text) {
				expr, err := constraint.Parse(comment.Text)
				if err != nil {
					return nil, err
				}
				if constraint.IsGoBuild(comment.Text) {
					explicit = expr
				} else {
					legacy = append(legacy, expr)
				}
			}
		}
	}
	if explicit == nil {
		for _, expr := range legacy {
			explicit = andCondition(explicit, expr)
		}
	}
	parts := strings.Split(strings.TrimSuffix(filepath.Base(path), ".go"), "_")
	if len(parts) > 1 && archTags[parts[len(parts)-1]] {
		explicit = andCondition(explicit, &constraint.TagExpr{Tag: parts[len(parts)-1]})
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 1 && osTags[parts[len(parts)-1]] {
		explicit = andCondition(explicit, &constraint.TagExpr{Tag: parts[len(parts)-1]})
	}
	return explicit, nil
}

var osTags = tagSet("aix android darwin dragonfly freebsd hurd illumos ios js linux netbsd openbsd plan9 solaris wasip1 windows zos")
var archTags = tagSet("386 amd64 amd64p32 arm arm64 loong64 mips mipsle mips64 mips64le ppc64 ppc64le riscv64 s390 s390x sparc64 wasm")
var unixTags = tagSet("aix android darwin dragonfly freebsd hurd illumos ios linux netbsd openbsd solaris")

func tagSet(list string) map[string]bool {
	tags := map[string]bool{}
	for _, tag := range strings.Fields(list) {
		tags[tag] = true
	}
	return tags
}

func andCondition(a, b constraint.Expr) constraint.Expr {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &constraint.AndExpr{X: a, Y: b}
}

// compatible reports whether a build can include every condition in the path.
func compatible(expr constraint.Expr) bool {
	if expr == nil {
		return true
	}
	tags := map[string]bool{}
	var collect func(constraint.Expr)
	collect = func(e constraint.Expr) {
		switch v := e.(type) {
		case *constraint.TagExpr:
			tags[v.Tag] = true
		case *constraint.NotExpr:
			collect(v.X)
		case *constraint.AndExpr:
			collect(v.X)
			collect(v.Y)
		case *constraint.OrExpr:
			collect(v.X)
			collect(v.Y)
		}
	}
	collect(expr)
	keys := make([]string, 0, len(tags))
	for tag := range tags {
		keys = append(keys, tag)
	}
	sort.Strings(keys)
	values := map[string]bool{}
	var search func(int, string, string) bool
	search = func(i int, osName, arch string) bool {
		if !validBuildTags(values, osName) || evaluateCondition(expr, values) < 0 {
			return false
		}
		if evaluateCondition(expr, values) > 0 {
			return true
		}
		tag := keys[i]
		values[tag] = false
		if search(i+1, osName, arch) {
			delete(values, tag)
			return true
		}
		// Android, iOS and illumos also satisfy their base OS tags.
		if osTags[tag] && osName != "" && !relatedOS(osName, tag) || archTags[tag] && arch != "" {
			delete(values, tag)
			return false
		}
		values[tag] = true
		if osTags[tag] && (osName == "" || tag == "android" || tag == "ios" || tag == "illumos") {
			osName = tag
		}
		if archTags[tag] {
			arch = tag
		}
		result := search(i+1, osName, arch)
		delete(values, tag)
		return result
	}
	return search(0, "", "")
}

// evaluateCondition returns -1 for false, 0 for unknown and 1 for true.
func evaluateCondition(expr constraint.Expr, values map[string]bool) int {
	switch v := expr.(type) {
	case *constraint.TagExpr:
		value, ok := values[v.Tag]
		if !ok {
			return 0
		}
		if value {
			return 1
		}
		return -1
	case *constraint.NotExpr:
		return -evaluateCondition(v.X, values)
	case *constraint.AndExpr:
		a, b := evaluateCondition(v.X, values), evaluateCondition(v.Y, values)
		if a < 0 || b < 0 {
			return -1
		}
		if a > 0 && b > 0 {
			return 1
		}
	case *constraint.OrExpr:
		a, b := evaluateCondition(v.X, values), evaluateCondition(v.Y, values)
		if a > 0 || b > 0 {
			return 1
		}
		if a < 0 && b < 0 {
			return -1
		}
	}
	return 0
}

func validBuildTags(values map[string]bool, osName string) bool {
	for child, parent := range map[string]string{"android": "linux", "ios": "darwin", "illumos": "solaris"} {
		if values[child] {
			if present, ok := values[parent]; ok && !present {
				return false
			}
		}
	}
	if unix, ok := values["unix"]; ok && osName != "" {
		isUnix := unixTags[osName]
		return unix == isUnix
	}
	return true
}

func relatedOS(a, b string) bool {
	return a == b || a == "android" && b == "linux" || a == "linux" && b == "android" ||
		a == "ios" && b == "darwin" || a == "darwin" && b == "ios" ||
		a == "illumos" && b == "solaris" || a == "solaris" && b == "illumos"
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
	types := map[string][]*commentType{}
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
		when, err := buildCondition(file, path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		packages[dir] = file.Name.Name
		for _, decl := range file.Decls {
			group, ok := decl.(*ast.GenDecl)
			if !ok || group.Tok != token.TYPE {
				continue
			}
			for _, spec := range group.Specs {
				ts := spec.(*ast.TypeSpec)
				item := &commentType{spec: ts, file: file, dir: dir, pkg: file.Name.Name, when: when}
				key := dir + "|" + file.Name.Name + "|" + ts.Name.Name
				types[key] = append(types[key], item)
				declarations = append(declarations, item)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var resolve func(*commentType, ast.Expr, constraint.Expr, map[string]bool) ([]*ast.StructType, error)
	follow := func(key string, when constraint.Expr, seen map[string]bool) ([]*ast.StructType, error) {
		if seen[key] {
			return nil, fmt.Errorf("cyclic type %s", key)
		}
		targets := types[key]
		if len(targets) == 0 {
			return nil, fmt.Errorf("type %s not found", key)
		}
		seen[key] = true
		defer delete(seen, key)
		var structures []*ast.StructType
		var failures []error
		matched := false
		for _, target := range targets {
			condition := andCondition(when, target.when)
			if !compatible(condition) {
				continue
			}
			matched = true
			resolved, err := resolve(target, target.spec.Type, condition, seen)
			structures = append(structures, resolved...)
			if err != nil {
				failures = append(failures, err)
			}
		}
		if !matched {
			return nil, fmt.Errorf("no compatible variant of %s", key)
		}
		return structures, errors.Join(failures...)
	}
	resolve = func(owner *commentType, expr ast.Expr, when constraint.Expr, seen map[string]bool) ([]*ast.StructType, error) {
		switch value := expr.(type) {
		case *ast.StructType:
			return []*ast.StructType{value}, nil
		case *ast.ParenExpr:
			return resolve(owner, value.X, when, seen)
		case *ast.IndexExpr:
			return resolve(owner, value.X, when, seen)
		case *ast.IndexListExpr:
			return resolve(owner, value.X, when, seen)
		case *ast.Ident:
			key := owner.dir + "|" + owner.pkg + "|" + value.Name
			return follow(key, when, seen)
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
				return follow(key, when, seen)
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
		if allowed {
			configs[key] = true
		}
		structures, err := resolve(item, item.spec.Type, item.when, map[string]bool{})
		if err != nil {
			pos := fset.Position(item.spec.Pos())
			rel, _ := filepath.Rel(root, pos.Filename)
			problems = append(problems, fmt.Sprintf("%s:%d %s: cannot resolve: %v", filepath.ToSlash(rel), pos.Line, name, err))
		}
		for _, structure := range structures {
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
