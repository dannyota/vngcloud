package core_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"danny.vn/vngcloud/backup"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/cdn"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/containerregistry"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/globalloadbalancer"
	"danny.vn/vngcloud/iam"
	"danny.vn/vngcloud/loadbalancer"
	"danny.vn/vngcloud/monitor"
	"danny.vn/vngcloud/network"
	"danny.vn/vngcloud/portal"
	"danny.vn/vngcloud/pricing"
	"danny.vn/vngcloud/project"
	"danny.vn/vngcloud/storage"
	"danny.vn/vngcloud/tagging"
	"danny.vn/vngcloud/vks"
	"danny.vn/vngcloud/volume"
)

// serviceClients lists every service package's Client type. A new service
// package must be added here; TestEveryServiceClientIsListed fails until it
// is.
var serviceClients = map[string]reflect.Type{
	"backup":             reflect.TypeFor[*backup.Client](),
	"billing":            reflect.TypeFor[*billing.Client](),
	"cdn":                reflect.TypeFor[*cdn.Client](),
	"compute":            reflect.TypeFor[*compute.Client](),
	"containerregistry":  reflect.TypeFor[*containerregistry.Client](),
	"dns":                reflect.TypeFor[*dns.Client](),
	"globalloadbalancer": reflect.TypeFor[*globalloadbalancer.Client](),
	"iam":                reflect.TypeFor[*iam.Client](),
	"loadbalancer":       reflect.TypeFor[*loadbalancer.Client](),
	"monitor":            reflect.TypeFor[*monitor.Client](),
	"network":            reflect.TypeFor[*network.Client](),
	"portal":             reflect.TypeFor[*portal.Client](),
	"pricing":            reflect.TypeFor[*pricing.Client](),
	"project":            reflect.TypeFor[*project.Client](),
	"storage":            reflect.TypeFor[*storage.Client](),
	"tagging":            reflect.TypeFor[*tagging.Client](),
	"volume":             reflect.TypeFor[*volume.Client](),
	"vks":                reflect.TypeFor[*vks.Client](),
}

// listShapeExceptions names list methods whose Output holds several lists by
// design and so has no Items field.
var listShapeExceptions = map[string]bool{
	"iam.ListPolicyAttachments": true,
}

// TestListOutputsHaveItems checks the list-shape rule: the Output of every
// method named List* is a struct with an Items slice field, as core.List and
// core.PagedList have, so one --query works on every list command.
func TestListOutputsHaveItems(t *testing.T) {
	for pkg, client := range serviceClients {
		for i := range client.NumMethod() {
			m := client.Method(i)
			name := pkg + "." + m.Name
			if !strings.HasPrefix(m.Name, "List") || listShapeExceptions[name] {
				continue
			}
			if m.Type.NumOut() != 2 {
				t.Errorf("%s returns %d values, want (*Output, error)", name, m.Type.NumOut())
				continue
			}
			out := m.Type.Out(0)
			if out.Kind() == reflect.Pointer {
				out = out.Elem()
			}
			if out.Kind() != reflect.Struct {
				t.Errorf("%s returns %s, want a struct with an Items slice", name, out)
				continue
			}
			items, ok := out.FieldByName("Items")
			if !ok || items.Type.Kind() != reflect.Slice {
				t.Errorf("%s returns %s without an Items slice field", name, out)
			}
		}
	}
}

// TestEveryServiceClientIsListed fails when a top-level package declares a
// Client type that serviceClients does not name.
func TestEveryServiceClientIsListed(t *testing.T) {
	root := filepath.Join("..", "..")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := filepath.Glob(filepath.Join(root, e.Name(), "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") || !declaresClient(t, file) {
				continue
			}
			if serviceClients[e.Name()] == nil {
				t.Errorf("package %s declares Client but is not in serviceClients", e.Name())
			}
		}
	}
}

func declaresClient(t *testing.T, file string) bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, s := range gd.Specs {
			if ts, ok := s.(*ast.TypeSpec); ok && ts.Name.Name == "Client" {
				return true
			}
		}
	}
	return false
}
