package cli

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const purgeInputJSON = `{"Paths":["/index.html","/assets/app.js"]}`

func TestCDNPurgePathsSendsJSONAndPrintsEmptyOutput(t *testing.T) {
	var requestBody string
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/cdn/flush-cache": func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			requestBody = string(body)
			jsonHandler(http.StatusOK, `{"success":true,"code":200,"message":"ok","data":""}`)(w, r)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	t.Setenv("VNGCLOUD_VCDN_API_KEY", vcdnKeyPlaceholder)
	root.SetArgs([]string{"--region", "hcm-3", "--output", "json", "cdn", "purge-paths",
		"--cdn-domain", "cdn.example.test", "--cli-input-json", purgeInputJSON})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr)
	}
	if got, want := requestBody, `{"cdnDomain":"cdn.example.test","type":"URI","patterns":["/index.html","/assets/app.js"]}`; got != want {
		t.Fatalf("request body = %s, want %s", got, want)
	}
	if got := stdout.String(); got != "{}\n" {
		t.Fatalf("stdout = %q, want %q", got, "{}\n")
	}
}

func TestCDNPurgePathsHasNoPathsFlagAndNeedsNoYes(t *testing.T) {
	cmd := newCDNCmd(&env{})
	sub, _, err := cmd.Find([]string{"purge-paths"})
	if err != nil {
		t.Fatal(err)
	}
	if sub.Name() != "purge-paths" {
		t.Fatalf("found command %q, want purge-paths", sub.Name())
	}
	if flag := sub.Flags().Lookup("paths"); flag != nil {
		t.Fatal("--paths is registered")
	}
	found := false
	for _, op := range cdnOps {
		if op.name == "purge-paths" {
			found = true
			if op.destructive {
				t.Fatal("purge-paths requires --yes")
			}
		}
	}
	if !found {
		t.Fatal("purge-paths is not registered")
	}
}

func TestCDNPurgePathsReadOnlySendsNoRequest(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/cdn/flush-cache": jsonHandler(http.StatusOK, `{"success":true,"code":200,"data":""}`),
	})
	root, _, _ := newSvcRoot(t, fixture)
	t.Setenv("VNGCLOUD_VCDN_API_KEY", vcdnKeyPlaceholder)
	root.SetArgs([]string{"--region", "hcm-3", "--read-only", "cdn", "purge-paths",
		"--cdn-domain", "cdn.example.test", "--cli-input-json", purgeInputJSON})
	err := root.ExecuteContext(context.Background())
	if err == nil || classify(err).Code != "ReadOnly" || exitCode(err) != 2 {
		t.Fatalf("err = %v, code = %q, exit = %d", err, classify(err).Code, exitCode(err))
	}
	if got := fixture.requestCount(); got != 0 {
		t.Fatalf("request count = %d, want 0", got)
	}
}

func TestCDNPurgePathsRejectsEmptyAndWildcardPathsWithoutRequest(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input string
	}{
		{"empty", `{"Paths":[]}`},
		{"empty item", `{"Paths":[""]}`},
		{"wildcard", `{"Paths":["/assets/*.js"]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v1/cdn/flush-cache": jsonHandler(http.StatusOK, `{"success":true,"code":200,"data":""}`),
			})
			root, _, _ := newSvcRoot(t, fixture)
			t.Setenv("VNGCLOUD_VCDN_API_KEY", vcdnKeyPlaceholder)
			root.SetArgs([]string{"--region", "hcm-3", "cdn", "purge-paths",
				"--cdn-domain", "cdn.example.test", "--cli-input-json", tt.input})
			err := root.ExecuteContext(context.Background())
			if err == nil || exitCode(err) != 2 {
				t.Fatalf("err = %v, exit = %d, want exit 2", err, exitCode(err))
			}
			if got := fixture.requestCount(); got != 0 {
				t.Fatalf("request count = %d, want 0", got)
			}
		})
	}
}

func TestCDNPurgePathsCooldownHasDedicatedError(t *testing.T) {
	for _, tt := range []struct {
		name     string
		body     string
		wantCode string
		wantExit int
	}{
		{
			"cooldown",
			`{"success":false,"code":202,"message":"Last CDN flush cache time is 10/10/2026 09:00:00","data":""}`,
			"PurgeCooldown",
			1,
		},
		{
			"other 202",
			`{"success":false,"code":202,"message":"URI invalid","data":""}`,
			"202",
			2,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v1/cdn/flush-cache": jsonHandler(http.StatusOK, tt.body),
			})
			root, _, stderr := newSvcRoot(t, fixture)
			t.Setenv("VNGCLOUD_VCDN_API_KEY", vcdnKeyPlaceholder)
			root.SetArgs([]string{"--region", "hcm-3", "cdn", "purge-paths",
				"--cdn-domain", "cdn.example.test", "--cli-input-json", `{"Paths":["/index.html"]}`})
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := classify(err).Code; got != tt.wantCode {
				t.Fatalf("code = %q, want %q (stderr=%s)", got, tt.wantCode, stderr)
			}
			if got := exitCode(err); got != tt.wantExit {
				t.Fatalf("exit = %d, want %d", got, tt.wantExit)
			}
			if got := fixture.requestCount(); got != 1 {
				t.Fatalf("request count = %d, want 1", got)
			}
			if strings.Contains(stderr.String(), vcdnKeyPlaceholder) {
				t.Fatalf("stderr holds API key: %s", stderr)
			}
		})
	}
}
