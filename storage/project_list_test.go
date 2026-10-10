package storage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func TestProjectStrictLists(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"absent", testutil.FixtureBody(t, fixtures+"project_list_empty.json"), true},
		{"explicit empty", `{"success":true,"datas":[]}`, true},
		{"data empty", `{"success":true,"data":[]}`, true},
		{"datas null", `{"success":true,"datas":null}`, false},
		{"data null", `{"success":true,"data":null}`, false},
		{"null with fallback", `{"success":true,"datas":null,"data":[]}`, false},
		{"null secondary", `{"success":true,"datas":[],"data":null}`, false},
		{"datas object", `{"success":true,"datas":{}}`, false},
		{"data string", `{"success":true,"data":"invalid"}`, false},
		{"malformed secondary", `{"success":true,"datas":[],"data":{}}`, false},
		{"incomplete absent", `{"success":true,"isNext":true}`, false},
		{"incomplete array", `{"success":true,"datas":[],"isNext":true}`, false},
		{"refusal", `{"success":false}`, false},
		{"missing success", `{"code":200}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var env envelope
			if err := json.Unmarshal([]byte(tc.body), &env); err != nil {
				t.Fatal(err)
			}
			items, err := completeProjectList("storage.CreateProject", &env)
			if (err == nil) != tc.valid || (tc.valid && len(items) != 0) {
				t.Fatalf("complete list: items %d error %v", len(items), err)
			}
			catalog, err := requiredProjectList[ProjectType]("storage.CreateProject", &env)
			if (err == nil) != tc.valid || (tc.valid && len(catalog) != 0) {
				t.Fatalf("catalog list: items %d error %v", len(catalog), err)
			}
		})
	}
	var env envelope
	if json.Unmarshal([]byte(`{"success":true}`), &env) != nil {
		t.Fatal("invalid envelope")
	}
	env.Datas = json.RawMessage(`[invalid`)
	if _, err := completeProjectList("storage.CreateProject", &env); err == nil {
		t.Fatal("malformed list accepted")
	}
}

func TestProjectEmptyFixtureReads(t *testing.T) {
	empty := testutil.FixtureBody(t, fixtures+"project_list_empty.json")
	s := &projectWriteServer{before: empty, after: projectList(newProjectJSON)}
	c := newTestClient(t, s.handler(t))
	out, err := c.CreateProject(context.Background(), validProjectCreate())
	if err != nil || out.Project == nil || s.orders != 1 {
		t.Fatalf("create error %v orders %d", err, s.orders)
	}
	s = &projectWriteServer{before: empty}
	c = newTestClient(t, s.handler(t))
	_, err = c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
	if !errors.Is(err, core.ErrNotFound) || s.deletes != 0 {
		t.Fatal("empty list did not prove absence")
	}
	s = &projectWriteServer{before: projectList(newProjectJSON), buckets: empty, after: empty}
	c = newTestClient(t, s.handler(t))
	_, err = c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
	if err != nil || s.deletes != 1 {
		t.Fatalf("delete error %v deletes %d", err, s.deletes)
	}
	s = &projectWriteServer{before: empty, after: empty}
	c = newTestClient(t, s.handler(t))
	_, err = c.CreateProject(context.Background(), validProjectCreate())
	if !errors.Is(err, ErrPaymentRequired) || s.orders != 1 {
		t.Fatal("empty checkout confirmation was not classified")
	}
	c = newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, fixtures+"project_list_empty.json")
	}))
	listed, err := c.ListProjects(context.Background(), nil)
	if err != nil || len(listed.Items) != 0 {
		t.Fatalf("public list changed: error %v", err)
	}
}

func TestProjectPublicListCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantError  bool
	}{
		{"observed empty", testutil.FixtureBody(t, fixtures+"project_list_empty.json"), false},
		{"null", `{"success":true,"datas":null}`, false},
		{"incomplete empty", `{"success":true,"datas":[],"isNext":true}`, false},
		{"non-array", `{"success":true,"datas":{}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			out, err := c.ListProjects(context.Background(), nil)
			if (err != nil) != tc.wantError || (err == nil && len(out.Items) != 0) {
				t.Fatalf("output %+v error %v", out, err)
			}
			if tc.name == "observed empty" && out.Items != nil {
				t.Fatal("public empty list representation changed")
			}
		})
	}
}
