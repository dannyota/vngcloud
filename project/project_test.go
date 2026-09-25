package project

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestProjectZeroConfig(t *testing.T) {
	c := New(vngcloud.Config{})
	if _, err := c.ListProjects(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("ListProjects() err = %v, want ErrInvalidConfig", err)
	}
}

func TestListProjects(t *testing.T) {
	c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/project/list_projects.json")
	})))

	out, err := c.ListProjects(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "project-1" || out.Items[0].Region != "hcm-3" {
		t.Fatalf("unexpected projects: %+v", out)
	}
}
