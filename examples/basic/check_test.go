package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSmokeCheckRejectsSDKErrors(t *testing.T) {
	for _, body := range []string{`[]`, `[{"id":"item-1"}]`} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/item", `{"regions":[{"statusCode":200,"body":`+body+`}]}`)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", `{"regions":[{"count":0,"error":"synthetic decode failure"}]}`)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("SDK decode failure passed the smoke check")
			}
		})
	}
}

func TestSmokeCheckRejectsMissingArtifacts(t *testing.T) {
	for _, kind := range []string{"raw", "sdk"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/matched", `{"regions":[{"statusCode":200,"body":[{"id":"item-1"}]}]}`)
			writeSmokeArtifact(t, root, "sdk", "synthetic/matched", `{"regions":[{"count":1,"items":[{"id":"item-1"}]}]}`)
			artifact := `{"regions":[{"statusCode":200,"body":[]}]}`
			if kind == "sdk" {
				artifact = `{"regions":[{"count":0,"items":[]}]}`
			}
			writeSmokeArtifact(t, root, kind, "synthetic/orphan", artifact)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("missing artifact passed the smoke check")
			}
			if len(reports) != 2 || reports[1].resource != "synthetic/orphan" {
				t.Fatalf("orphan artifact report missing: %+v", reports)
			}
			if kind == "sdk" && !reports[1].sdkWithoutRaw {
				t.Fatal("SDK-only artifact was not marked")
			}
		})
	}
}

func TestSmokeCheckReportsSDKOnlyOutput(t *testing.T) {
	root := t.TempDir()
	writeSmokeArtifact(t, root, "sdk", "synthetic/orphan", `{"regions":[{"count":0,"items":[]}]}`)
	reports, err := checkSmokeOutput(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || !reports[0].sdkWithoutRaw || printSmokeCheck(reports) {
		t.Fatalf("SDK-only output was not rejected: %+v", reports)
	}
}

func TestSmokeCheckRejectsCoverageLoss(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		sdk   string
		count int
	}{
		{"decoded empty", `[{"id":"item-1"}]`, `[]`, 0},
		{"raw empty", `[]`, `[{"id":"item-1"}]`, 1},
		{"row dropped", `[{"id":"item-1"},{"id":"item-2"}]`, `[{"id":"item-1"}]`, 1},
		{"row added", `[{"id":"item-1"}]`, `[{"id":"item-1"},{"id":"item-2"}]`, 2},
		{"all keys dropped", `[{"id":"item-1"}]`, `[{}]`, 1},
		{"one key dropped", `[{"id":"item-1","name":"synthetic"}]`, `[{"id":"item-1"}]`, 1},
		{"status dropped", `[{"id":"item-1","status":"ACTIVE"}]`, `[{"id":"item-1"}]`, 1},
		{"count hides dropped row", `[{"id":"item-1"},{"id":"item-2"}]`, `[{"id":"item-1"}]`, 2},
		{"key dropped in one row", `[{"id":"item-1","name":"one"},{"id":"item-2","name":"two"}]`, `[{"id":"item-1","name":"one"},{"id":"item-2"}]`, 2},
		{"nested key dropped", `[{"left":{"name":"one"},"right":{"name":"two"}}]`, `[{"left":{},"right":{"name":"two"}}]`, 1},
		{"literal key moved into object", `[{"left":{},"left.name":"one"}]`, `[{"left":{"name":"one"}}]`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/item", `{"regions":[{"statusCode":200,"body":{"data":`+tc.raw+`}}]}`)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", fmt.Sprintf(`{"regions":[{"count":%d,"items":%s}]}`, tc.count, tc.sdk))
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("row or key loss passed the smoke check")
			}
		})
	}
}

func TestSmokeCheckRequiresSuccessfulEmptyEvidence(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		sdk  string
	}{
		{"no captures", `{"regions":[]}`, `{"regions":[{"count":0,"items":[]}]}`},
		{"no SDK entries", `{"regions":[{"statusCode":200,"body":[]}]}`, `{"regions":[]}`},
		{"HTTP error", `{"regions":[{"statusCode":403,"body":[]}]}`, `{"regions":[{"count":0,"items":[]}]}`},
		{"missing raw body", `{"regions":[{"statusCode":200}]}`, `{"regions":[{"count":0,"items":[]}]}`},
		{"null raw body", `{"regions":[{"statusCode":200,"body":null}]}`, `{"regions":[{"count":0,"items":[]}]}`},
		{"missing raw payload", `{"regions":[{"statusCode":200,"body":{"code":200}}]}`, `{"regions":[{"count":0,"items":[]}]}`},
		{"null raw payload", `{"regions":[{"statusCode":200,"body":{"data":null}}]}`, `{"regions":[{"count":0,"items":[]}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/item", tc.raw)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", tc.sdk)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("unproven empty output passed the smoke check")
			}
		})
	}
}

func TestSmokeCheckAcceptsMatchedArtifacts(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		sdk   string
		count int
	}{
		{"populated", `{"data":[{"id":"item-1","name":"synthetic"}]}`, `[{"id":"item-1","name":"synthetic"}]`, 1},
		{"empty array", `[]`, `[]`, 0},
		{"empty envelope", `{"data":{"items":[]}}`, `[]`, 0},
		{"empty nil SDK slice", `{"data":[]}`, `null`, 0},
		{"empty storage envelope", `{"success":true,"datas":[]}`, `[]`, 0},
		{"SDK output wrapper", `{"data":{"items":[{"id":"item-1"},{"id":"item-2"}]}}`, `{"Items":[{"id":"item-1"},{"id":"item-2"}],"TotalItem":2}`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/item", `{"regions":[{"statusCode":200,"body":`+tc.raw+`}]}`)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", fmt.Sprintf(`{"regions":[{"count":%d,"items":%s}]}`, tc.count, tc.sdk))
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if !printSmokeCheck(reports) {
				t.Fatalf("matching artifacts failed: %+v", reports)
			}
		})
	}
}

func TestSmokeCheckMatchesCaptureScopes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		sdk  string
	}{
		{
			"config mismatch",
			`{"regions":[{"config":"one","statusCode":200,"body":[]}]}`,
			`{"regions":[{"config":"two","count":0,"items":[]}]}`,
		},
		{
			"region mismatch",
			`{"regions":[{"region":"han-1","statusCode":200,"body":[]}]}`,
			`{"regions":[{"region":"hcm-3","count":0,"items":[]}]}`,
		},
		{
			"keys moved between regions",
			`{"regions":[{"region":"han-1","statusCode":200,"body":[{"id":"one","name":"one"}]},{"region":"hcm-3","statusCode":200,"body":[{"id":"two"}]}]}`,
			`{"regions":[{"region":"han-1","count":1,"items":[{"id":"one"}]},{"region":"hcm-3","count":1,"items":[{"id":"two","name":"two"}]}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/item", tc.raw)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", tc.sdk)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("unmatched capture scopes passed the smoke check")
			}
		})
	}
}

func TestSmokeCheckAcceptsAggregatedCaptures(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		sdk  string
	}{
		{
			"paginated raw and aggregated SDK",
			`{"regions":[{"config":"synthetic","region":"han-1","statusCode":200,"body":{"data":[{"id":"one"}]}},{"config":"synthetic","region":"han-1","statusCode":200,"body":{"data":[{"id":"two"}]}}]}`,
			`{"regions":[{"config":"synthetic","region":"han-1","count":2,"items":[{"id":"one"},{"id":"two"}]}]}`,
		},
		{
			"account scope ignores capture region",
			`{"regions":[{"config":"synthetic","region":"han-1","statusCode":200,"body":{"data":[]}}]}`,
			`{"regions":[{"config":"synthetic","region":"","scope":"account","count":0}]}`,
		},
		{
			"failed attempt before successful capture",
			`{"regions":[{"statusCode":503,"body":{"error":"synthetic"}},{"statusCode":200,"body":[]}]}`,
			`{"regions":[{"count":0,"items":[]}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSmokeArtifact(t, root, "raw", "synthetic/item", tc.raw)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", tc.sdk)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if !printSmokeCheck(reports) {
				t.Fatalf("matching captures failed: %+v", reports)
			}
		})
	}
}

func TestSmokeCheckRejectsTrailingJSON(t *testing.T) {
	for _, kind := range []string{"raw", "sdk"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			raw := `{"regions":[{"statusCode":200,"body":[]}]}`
			sdk := `{"regions":[{"count":0,"items":[]}]}`
			if kind == "raw" {
				raw += `{}`
			} else {
				sdk += `{}`
			}
			writeSmokeArtifact(t, root, "raw", "synthetic/item", raw)
			writeSmokeArtifact(t, root, "sdk", "synthetic/item", sdk)
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatal(err)
			}
			if printSmokeCheck(reports) {
				t.Fatal("invalid JSON artifact passed the smoke check")
			}
		})
	}
}

func writeSmokeArtifact(t *testing.T, root, kind, resource, data string) {
	t.Helper()
	path := filepath.Join(root, kind, resource+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
