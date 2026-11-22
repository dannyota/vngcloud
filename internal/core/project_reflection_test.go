package core_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/project"
)

type projectRotatingCredentials struct{ n int }

func (p *projectRotatingCredentials) Token(context.Context) (vngcloud.Token, error) {
	p.n++
	token := "first-credential-marker"
	if p.n > 1 {
		token = "second-credential-marker"
	}
	return vngcloud.Token{AccessToken: token, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*projectRotatingCredentials) Invalidate(string) {}

func TestProjectListRejectsEarlierReflectedCredential(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/projects") {
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			id := "first-credential-marker"
			if calls > 2 {
				id = "second-credential-marker"
			}
			_, _ = w.Write([]byte(`{"projects":[{"projectId":"` + id + `","region":"hcm-3"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	var captures []vngcloud.ResponseCapture
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithCredentialsProvider(&projectRotatingCredentials{}), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL}), vngcloud.WithRetry(0, 0), vngcloud.WithResponseCapture(func(c vngcloud.ResponseCapture) { captures = append(captures, c) }))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := project.New(cfg).ListProjects(context.Background(), nil); err == nil || out != nil {
		t.Fatal("reflected credential accepted")
	}
	c := core.ClientOf(cfg)
	if _, err := c.RequireProjectID(context.Background()); err == nil || c.ProjectID() != "" {
		t.Fatal("reflected credential cached")
	}
	if err := c.DoJSON(context.Background(), transport.Request{URL: server.URL + "/next"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(captures) != 1 || captures[0].URL != server.URL+"/next" {
		t.Fatalf("next request capture missing or unexpected: %d captures", len(captures))
	}
	for _, capture := range captures {
		if strings.Contains(capture.URL, "first-credential-marker") || strings.Contains(string(capture.Body), "first-credential-marker") {
			t.Fatal("credential leaked into capture")
		}
	}
}
