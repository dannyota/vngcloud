package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"danny.vn/vngcloud/project"
)

// TestGoldenProjectListProjects checks list-projects' exact output shape
// through the generic renderer, using the real project.ListProjectsOutput
// type.
func TestGoldenProjectListProjects(t *testing.T) {
	v := &project.ListProjectsOutput{Items: []project.Project{
		{ID: "project-1", Name: "prod", Region: "hcm-3", Status: "ACTIVE", IsDefault: true, UserID: 100},
	}}
	checkGolden(t, "project-list-projects.json.golden", "json", "", v)
	checkGolden(t, "project-list-projects.table.golden", "table", "", v)
	checkGolden(t, "project-list-projects.text.golden", "text", "", v)
}

// TestProjectCommandMatchesDesignTable checks the CLI reads design's
// "project" table: exactly one command, list-projects, with no flag at all
// (its only Input field, Region, is NoFlag: --cli-input-json only).
func TestProjectCommandMatchesDesignTable(t *testing.T) {
	if got := opNames(projectOps); len(got) != 1 || got[0] != "list-projects" {
		t.Fatalf("project ops = %v, want exactly [list-projects]", got)
	}
	specs, err := flagSpecsFor(projectOps[0].newInput())
	if err != nil {
		t.Fatalf("flagSpecsFor: %v", err)
	}
	if got := withoutNoFlag(specs, projectOps[0].noFlag); len(got) != 0 {
		t.Fatalf("list-projects flags = %v, want none (Region is NoFlag)", got)
	}
}

// TestProjectListProjectsHasNoOwnRegionFlag checks the collision this design
// calls out by name: ListProjectsInput.Region's mechanical flag name would
// be --region, which is also the global flag's name, so list-projects must
// register no local flag of that name at all.
func TestProjectListProjectsHasNoOwnRegionFlag(t *testing.T) {
	cmd := newProjectCmd(&env{flags: &globalFlags{}})
	sub, _, err := cmd.Find([]string{"list-projects"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if f := sub.Flags().Lookup("region"); f != nil {
		t.Fatalf("list-projects registered its own --region flag: %+v", f)
	}
}

// TestProjectListProjectsRegionFiltersClientSide checks the CLI reads
// design's Testing note for project: the global --region flag picks the
// default filter, and --cli-input-json '{"Region":"..."}' overrides it,
// against a fixture holding projects in two regions.
func TestProjectListProjectsRegionFiltersClientSide(t *testing.T) {
	body := `{"projects":[{"projectId":"p-hcm","region":"hcm-3"},{"projectId":"p-han","region":"han-1"}]}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/projects": jsonHandler(http.StatusOK, body),
	})

	// The CLI's own JSON encoder uses Go field names, not project.Project's
	// API json tags (its ID field is tagged "projectId"), so decoding stdout
	// needs an untagged struct that matches by field name instead of the
	// real resource type.
	type decodedProject struct{ ID, Region string }
	runList := func(t *testing.T, args ...string) []decodedProject {
		t.Helper()
		root, stdout, stderr := newSvcRoot(t, fixture)
		root.SetArgs(append([]string{"--region", "hcm-3", "project", "list-projects"}, args...))
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
		}
		var decoded struct{ Items []decodedProject }
		if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
			t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
		}
		return decoded.Items
	}

	t.Run("global region flag picks the default filter", func(t *testing.T) {
		items := runList(t)
		if len(items) != 1 || items[0].ID != "p-hcm" {
			t.Fatalf("Items = %+v, want exactly the hcm-3 project", items)
		}
	})

	t.Run("cli-input-json overrides the filter", func(t *testing.T) {
		items := runList(t, "--cli-input-json", `{"Region":"han-1"}`)
		if len(items) != 1 || items[0].ID != "p-han" {
			t.Fatalf("Items = %+v, want exactly the han-1 project", items)
		}
	})
}
