package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"danny.vn/vngcloud/cdn"
)

// wantCDNIPRanges is the exact 19 canonical CIDRs testdata/cdn/faq-vcdn.html
// holds, in order, per the cdn SDK package's own fixture test. It is kept
// here, rather than shared with that package, because it names an internal
// package boundary this package must not cross.
var wantCDNIPRanges = []string{
	"14.225.2.32/28",
	"14.225.10.64/28",
	"42.115.221.64/27",
	"42.115.221.128/27",
	"43.239.149.128/28",
	"61.28.226.48/28",
	"61.28.231.126/32",
	"113.164.14.192/27",
	"113.164.15.32/28",
	"113.164.15.80/29",
	"113.164.241.176/28",
	"118.69.83.64/27",
	"118.69.83.160/28",
	"118.69.84.64/28",
	"171.244.16.224/27",
	"171.244.28.64/27",
	"171.244.128.0/27",
	"210.245.26.0/24",
	"210.245.38.64/27",
}

// TestGoldenCDNListIPRanges checks the CDN design's exact output shapes
// through the generic renderer, using the real cdn.ListIPRangesOutput type:
// JSON keeps {"Items": [...], "Source": "..."}, text prints one CIDR per
// line, and table prints one "Value" column.
func TestGoldenCDNListIPRanges(t *testing.T) {
	v := &cdn.ListIPRangesOutput{
		Items:  wantCDNIPRanges,
		Source: "https://docs.greennode.ai/faq/vcdn",
	}
	checkGolden(t, "cdn-list-ip-ranges.json.golden", "json", "", v)
	checkGolden(t, "cdn-list-ip-ranges.table.golden", "table", "", v)
	checkGolden(t, "cdn-list-ip-ranges.text.golden", "text", "", v)
}

// cdnFixtureServer builds a svcFixture that serves fixturePath's content at
// the "/faq/vcdn" path newFakeServer's CDNDocs override points at, recording
// whether any request carried an Authorization header.
func cdnFixtureServer(t *testing.T, fixturePath, contentType string) (fixture *svcFixture, sawAuth *bool) {
	t.Helper()
	body, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", fixturePath, err)
	}
	sawAuth = new(bool)
	fixture = newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/faq/vcdn": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "" {
				*sawAuth = true
			}
			w.Header().Set("Content-Type", contentType)
			_, _ = w.Write(body)
		},
	})
	return fixture, sawAuth
}

// TestCDNListIPRangesJSONFromFixtureSendsNoAuthorization runs the real
// cdn list-ip-ranges command against the live-captured fixture through an
// httptest server, wired exactly as the production command would build it
// (region, project ID, and a static token configured for every other
// endpoint). It checks the JSON output's Items and Source, and that the
// docs server it served the page from never saw an Authorization header,
// even though the Config carries a token.
func TestCDNListIPRangesJSONFromFixtureSendsNoAuthorization(t *testing.T) {
	fixture, sawAuth := cdnFixtureServer(t, "../../testdata/cdn/faq-vcdn.html", "text/html; charset=utf-8")
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--output", "json", "cdn", "list-ip-ranges"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	if *sawAuth {
		t.Fatal("Authorization header reached the docs server")
	}

	var decoded struct {
		Items  []string
		Source string
	}
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if len(decoded.Items) != len(wantCDNIPRanges) {
		t.Fatalf("got %d items, want %d: %v", len(decoded.Items), len(wantCDNIPRanges), decoded.Items)
	}
	for i, want := range wantCDNIPRanges {
		if decoded.Items[i] != want {
			t.Fatalf("item[%d] = %q, want %q", i, decoded.Items[i], want)
		}
	}
	if decoded.Source == "" {
		t.Fatal("Source is empty")
	}
}

// TestCDNListIPRangesTextOutputOneCIDRPerLine checks --output text against
// the same live-captured fixture, end to end through the real command.
func TestCDNListIPRangesTextOutputOneCIDRPerLine(t *testing.T) {
	fixture, _ := cdnFixtureServer(t, "../../testdata/cdn/faq-vcdn.html", "text/html; charset=utf-8")
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--output", "text", "cdn", "list-ip-ranges"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}

	wantText := strings.Join(wantCDNIPRanges, "\n") + "\n"
	if stdout.String() != wantText {
		t.Fatalf("stdout = %q, want %q", stdout.String(), wantText)
	}
}

// TestCDNListIPRangesPageFormatErrorExitsOne checks that a page whose shape
// the parser no longer recognizes (the heading is missing) prints the
// PageFormat error code and exits 1, per the CDN design's "Errors".
func TestCDNListIPRangesPageFormatErrorExitsOne(t *testing.T) {
	fixture, _ := cdnFixtureServer(t, "../../testdata/cdn/heading-missing.html", "text/html; charset=utf-8")
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "cdn", "list-ip-ranges"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error for a page with no matching heading")
	}
	if got := classify(err).Code; got != "PageFormat" {
		t.Fatalf("Code = %q, want PageFormat (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
}
