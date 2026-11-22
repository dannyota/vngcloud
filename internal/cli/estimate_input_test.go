package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const unknownEstimateSpec = `{"PublicIPs":[{"Key":"ip","Kind":"floating-ip","Quantity":1}]}`

func TestEstimateStrictInput(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"missing", ""}, {"empty", `{}`}, {"null", `null`}, {"array", `[]`},
		{"duplicate", `{"PublicIPs":[],"PublicIPs":[]}`},
		{"escaped duplicate", `{"PublicIPs":[],"Public\u0049Ps":[]}`},
		{"nested duplicate", `{"PublicIPs":[{"Key":"ip","Key":"other","Kind":"floating-ip","Quantity":1}]}`},
		{"case", `{"publicIPs":[]}`},
		{"nested case", `{"PublicIPs":[{"key":"ip","Kind":"floating-ip","Quantity":1}]}`},
		{"unknown", `{"Endpoint":"withheld-canary"}`},
		{"nested unknown", `{"Servers":[{"Password":"withheld-canary"}]}`},
		{"wrong type", `{"MaxRequests":"withheld-canary"}`},
		{"trailing", unknownEstimateSpec + `{}`},
		{"malformed", `{"PublicIPs":`},
		{"depth", `{"Servers":` + strings.Repeat("[", 17) + `0` + strings.Repeat("]", 17) + `}`},
		{"missing quantity", `{"PublicIPs":[{"Key":"ip","Kind":"floating-ip"}]}`},
		{"large quantity", `{"PublicIPs":[{"Key":"ip","Kind":"floating-ip","Quantity":1001}]}`},
		{"duplicate component", `{"PublicIPs":[{"Key":"ip","Kind":"floating-ip","Quantity":1},{"Key":"ip","Kind":"reservation","Quantity":1}]}`},
		{"invalid enum", `{"PublicIPs":[{"Key":"ip","Kind":"withheld-canary","Quantity":1}]}`},
		{"invalid type", `{"PublicIPs":[{"Key":"ip","Kind":"reservation","Type":"public","Quantity":1}]}`},
		{"negative limit", `{"PublicIPs":[{"Key":"ip","Kind":"reservation","Quantity":1}],"MaxRequests":-1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newSvcFixture(nil)
			root, stdout, stderr := newSvcRoot(t, fixture)
			root.SetArgs([]string{"--region", "hcm-3", "pricing", "estimate", "--cli-input-json", tc.input})
			err := root.ExecuteContext(context.Background())
			if exitCode(err) != 2 || stdout.Len() != 0 || fixture.requestCount() != 0 {
				t.Fatalf("exit=%d stdout=%s requests=%d err=%v stderr=%s", exitCode(err), stdout, fixture.requestCount(), err, stderr)
			}
			if strings.Contains(err.Error(), "withheld-canary") {
				t.Fatalf("input reflected: %v", err)
			}
		})
	}
}

func TestEstimateInputSizeAndFiles(t *testing.T) {
	for _, file := range []bool{false, true} {
		for _, extra := range []int{-1, 0, 1} {
			t.Run(fmt.Sprintf("file=%t/bytes=%d", file, (1<<20)+extra), func(t *testing.T) {
				fixture := newSvcFixture(nil)
				root, stdout, _ := newSvcRoot(t, fixture)
				input := unknownEstimateSpec + strings.Repeat(" ", (1<<20)-len(unknownEstimateSpec)+extra)
				if file {
					path := filepath.Join(t.TempDir(), "setup.json")
					if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
						t.Fatal(err)
					}
					input = "file://" + path
				}
				root.SetArgs([]string{"--region", "hcm-3", "pricing", "estimate", "--cli-input-json", input})
				err := root.ExecuteContext(context.Background())
				want := 0
				if extra > 0 {
					want = 2
				}
				if exitCode(err) != want || fixture.requestCount() != 0 {
					t.Fatalf("exit=%d want=%d requests=%d err=%v", exitCode(err), want, fixture.requestCount(), err)
				}
				if want == 0 && !json.Valid(stdout.Bytes()) {
					t.Fatalf("invalid output: %s", stdout)
				}
			})
		}
	}
	for _, path := range []string{t.TempDir(), filepath.Join(t.TempDir(), "absent")} {
		fixture := newSvcFixture(nil)
		root, stdout, _ := newSvcRoot(t, fixture)
		root.SetArgs([]string{"pricing", "estimate", "--cli-input-json", "file://" + path})
		err := root.ExecuteContext(context.Background())
		if exitCode(err) != 2 || stdout.Len() != 0 || fixture.requestCount() != 0 {
			t.Fatalf("file refusal: %v", err)
		}
	}
}

func TestEstimateFileDescriptorAndClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup.json")
	if err := os.WriteFile(path, []byte(unknownEstimateSpec), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openRegularFileNonBlocking(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := readEstimateFile(f)
	if err != nil || string(data) != unknownEstimateSpec {
		t.Fatalf("opened descriptor replaced by path: data=%s err=%v", data, err)
	}
	if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("regular descriptor was not closed: %v", err)
	}
	directory, err := openRegularFileNonBlocking(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readEstimateFile(directory); exitCode(err) != 2 {
		t.Fatalf("accepted directory: %v", err)
	}
	if _, err := directory.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("refused descriptor was not closed: %v", err)
	}
}

func TestEstimateLocalBoundsBeforeConfig(t *testing.T) {
	withCleanEnv(t)
	withTestOptions(t)
	validIP := `{"Key":"ip","Kind":"reservation","Quantity":1}`
	for _, input := range []string{
		`{"Servers":[{"Key":"web","Quantity":1,"ZoneID":"zone-1","FlavorID":"flavor-1","ImageID":"image-1","RootDiskSize":20,"RootDiskTypeID":"type-1","DataDiskSize":20}]}`,
		`{"Servers":[{"Key":"web","Quantity":1,"ZoneID":"../escape","FlavorID":"flavor-1","ImageID":"image-1","RootDiskSize":20,"RootDiskTypeID":"type-1"}]}`,
		`{"Volumes":[{"Key":"disk","Quantity":1,"ZoneID":"zone-1","VolumeTypeID":"type-1","Size":20,"ServerKey":"absent"}]}`,
		`{"Storage":[{"Key":"storage","Quantity":1,"Type":"Gold","QuotaGB":0}]}`,
		`{"Storage":[{"Key":"storage","Quantity":1,"Type":"Gold","QuotaGB":30,"Region":"not-a-region"}]}`,
		`{"PublicIPs":[` + strings.Repeat(validIP+",", 1000) + validIP + `]}`,
	} {
		var stdout, stderr strings.Builder
		code := Main(context.Background(), []string{"pricing", "estimate", "--cli-input-json", input}, strings.NewReader(""), &stdout, &stderr)
		if code != 2 || stdout.Len() != 0 {
			t.Fatalf("validation needed config: exit=%d stderr=%s", code, &stderr)
		}
	}
	rows := make([]string, 11)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"Key":"ip%d","Kind":"reservation","Quantity":1000}`, i)
	}
	var stdout, stderr strings.Builder
	if code := Main(context.Background(), []string{"pricing", "estimate", "--cli-input-json", `{"PublicIPs":[` + strings.Join(rows, ",") + `]}`}, strings.NewReader(""), &stdout, &stderr); code != 2 || stdout.Len() != 0 {
		t.Fatalf("summed quantities: exit=%d stderr=%s", code, &stderr)
	}
}

func TestEstimateFlagsAndOriginalInput(t *testing.T) {
	for _, args := range [][]string{
		{"--max-requests", "2001"}, {"--max-requests", "-1"},
		{"--output", "bad"}, {"--query", "["}, {"--servers", "[]"},
		{"--max-price", "1"}, {"--sort-by", "name"}, {"--descending"},
		{"--max-requests", "500", "--cli-input-json", `{"PublicIPs":[{"Key":"ip","Kind":"reservation","Quantity":1}],"MaxRequests":-1}`},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fixture := newSvcFixture(nil)
			root, stdout, _ := newSvcRoot(t, fixture)
			root.SetArgs(append([]string{"--region", "hcm-3", "pricing", "estimate", "--cli-input-json", unknownEstimateSpec}, args...))
			err := root.ExecuteContext(context.Background())
			if exitCode(err) != 2 || stdout.Len() != 0 || fixture.requestCount() != 0 {
				t.Fatalf("invalid flags: %v", err)
			}
		})
	}
}

func TestEstimateControlsOverrideJSON(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": jsonHandler(500, `{}`),
	})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--read-only", "pricing", "estimate", "--cli-input-json",
		`{"PublicIPs":[{"Key":"unknown","Kind":"reservation","Quantity":1},{"Key":"priced","Kind":"public-vip","Type":"public","Quantity":1},{"Key":"other","Kind":"public-vip","Type":"public-vm","Quantity":1}],"AllowPartial":false,"MaxRequests":1}`,
		"--allow-partial", "--max-requests", "10"})
	err := root.ExecuteContext(context.Background())
	if exitCode(err) != 1 || !strings.Contains(stdout.String(), `"Key": "unknown"`) || fixture.requestCount() != 2 {
		t.Fatalf("override: exit=%d requests=%d stdout=%s err=%v", exitCode(err), fixture.requestCount(), stdout, err)
	}
}
