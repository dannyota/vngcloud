package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// quoteOptionalCases lists the three create quotes whose unpriced fields are
// optional on the command. pricedArgs holds only the priced flags, and
// wantInfo is the request's resourceInfo for them. badArgs adds one
// unpriced flag with a malformed ID, which the SDK must still refuse.
var quoteOptionalCases = []struct {
	name       string
	pricedArgs []string
	wantInfo   map[string]any
	badArgs    []string
	optional   []string
	required   []string
	docFile    string
	docOp      string
}{
	{
		name: "compute quote-create-server",
		pricedArgs: []string{
			"compute", "quote-create-server", "--zone-id", "zone-1", "--flavor-id", "flavor-1",
			"--image-id", "image-1", "--root-disk-size", "20", "--root-disk-type-id", "voltype-1",
		},
		wantInfo: pricedServerQuoteInfo,
		badArgs:  []string{"--vpc-id", "vpc/1"},
		optional: []string{"name", "vpc-id", "subnet-id", "security-group-id", "ssh-key-id"},
		required: []string{"zone-id", "flavor-id", "image-id", "root-disk-size", "root-disk-type-id"},
		docFile:  "CLI-Compute.md",
		docOp:    "quote-create-server",
	},
	{
		name:       "volume quote-create-volume",
		pricedArgs: []string{"volume", "quote-create-volume", "--zone-id", "zone-1", "--size", "10", "--volume-type-id", "voltype-1"},
		wantInfo:   pricedVolumeQuoteInfo,
		badArgs:    []string{"--volume-type-id", "type/1"},
		optional:   []string{"name"},
		required:   []string{"zone-id", "size", "volume-type-id"},
		docFile:    "CLI-Volume.md",
		docOp:      "quote-create-volume",
	},
	{
		name:       "loadbalancer quote-create-load-balancer",
		pricedArgs: []string{"loadbalancer", "quote-create-load-balancer", "--package-id", "pkg-1", "--zone-id", "zone-1"},
		wantInfo: map[string]any{
			"packageId": "pkg-1", "zoneId": "zone-1",
			"period": float64(1), "isPoc": false, "isBuyMorePoc": false,
		},
		badArgs:  []string{"--subnet-id", "subnet/1"},
		optional: []string{"name", "scheme", "subnet-id", "type"},
		required: []string{"package-id", "zone-id"},
		docFile:  "CLI-LoadBalancer.md",
		docOp:    "quote-create-load-balancer",
	},
}

// TestQuoteCreateRunsWithOnlyPricedFlagsAndSendsPricedBody checks each
// create quote with only its priced flags: it passes the required-flag check
// and sends exactly the priced keys.
func TestQuoteCreateRunsWithOnlyPricedFlagsAndSendsPricedBody(t *testing.T) {
	for _, tc := range quoteOptionalCases {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v1/price": func(w http.ResponseWriter, r *http.Request) {
					defer func() { _ = r.Body.Close() }()
					body, _ = io.ReadAll(r.Body)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"optimumPrice":1,"originalPrice":1,"discountPrice":0,"propertiesPrice":[]}`))
				},
			})
			root, _, stderr := newSvcRoot(t, fixture)
			root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"}, tc.pricedArgs...))
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("%v (stderr=%s)", err, stderr.String())
			}
			var decoded struct {
				ResourceInfo map[string]any `json:"resourceInfo"`
			}
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("body is not valid JSON: %v (%s)", err, body)
			}
			if !reflect.DeepEqual(decoded.ResourceInfo, tc.wantInfo) {
				t.Fatalf("resourceInfo = %v, want %v", decoded.ResourceInfo, tc.wantInfo)
			}
		})
	}
}

// TestQuoteCreateSetOptionalFlagStillShapeChecked checks that an unpriced
// flag set to a malformed ID exits 2 with no request, so the quote fails the
// way the create will. The volume case repeats a required flag, since
// quote-create-volume has no unpriced flag with a shape.
func TestQuoteCreateSetOptionalFlagStillShapeChecked(t *testing.T) {
	for _, tc := range quoteOptionalCases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v1/price": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			root, _, stderr := newSvcRoot(t, fixture)
			args := append([]string{"--region", "hcm-3", "--project-id", "proj-1"}, tc.pricedArgs...)
			root.SetArgs(append(args, tc.badArgs...))
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected an error for %v", tc.badArgs)
			}
			if exitCode(err) != 2 {
				t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr.String())
			}
			if n := fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}

// docRow returns the table row of flag in op's section of a generated CLI
// page, and whether the row exists.
func docRow(t *testing.T, page, op, flag string) (string, bool) {
	t.Helper()
	section := genDocsSection(t, page, op)
	prefix := "| `--" + flag + "` |"
	for line := range strings.SplitSeq(section, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line, true
		}
	}
	return "", false
}

// docRequiredCell returns the Required cell of flag's row in op's section.
func docRequiredCell(t *testing.T, page, op, flag string) (string, bool) {
	t.Helper()
	line, ok := docRow(t, page, op, flag)
	if !ok {
		return "", false
	}
	cells := strings.Split(line, "|")
	return strings.TrimSpace(cells[len(cells)-2]), true
}

// TestGenDocsQuoteCreateListsUnpricedFlagsAsOptional checks that the
// generated pages mark each unpriced flag of a create quote optional and
// each priced flag required.
func TestGenDocsQuoteCreateListsUnpricedFlagsAsOptional(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	for _, tc := range quoteOptionalCases {
		data, err := os.ReadFile(filepath.Join(dir, tc.docFile))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", tc.docFile, err)
		}
		for _, flag := range tc.optional {
			if got, ok := docRequiredCell(t, string(data), tc.docOp, flag); !ok || got != "" {
				t.Errorf("%s --%s: Required cell = %q, found = %v, want an empty cell", tc.docOp, flag, got, ok)
			}
		}
		for _, flag := range tc.required {
			if got, ok := docRequiredCell(t, string(data), tc.docOp, flag); !ok || got != "yes" {
				t.Errorf("%s --%s: Required cell = %q, found = %v, want yes", tc.docOp, flag, got, ok)
			}
		}
	}
}
