package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenDocsIsDeterministic(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	if err := runGenDocs(dir1); err != nil {
		t.Fatalf("runGenDocs(dir1): %v", err)
	}
	if err := runGenDocs(dir2); err != nil {
		t.Fatalf("runGenDocs(dir2): %v", err)
	}
	entries, err := os.ReadDir(dir1)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("no files were generated")
	}
	for _, entry := range entries {
		b1, err := os.ReadFile(filepath.Join(dir1, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile %s (run 1): %v", entry.Name(), err)
		}
		b2, err := os.ReadFile(filepath.Join(dir2, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile %s (run 2): %v", entry.Name(), err)
		}
		if !bytes.Equal(b1, b2) {
			t.Errorf("%s differs between two runs", entry.Name())
		}
	}
}

func TestGenDocsWritesExpectedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	for _, name := range []string{"CLI.md", "CLI-Billing.md", "CLI-Pricing.md", "CLI-Compute.md", "CLI-Network.md", "CLI-DNS.md", "CLI-CDN.md", "CLI-Monitor.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
}

func TestGenDocsEveryOpAppears(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	check := func(file string, names []string) {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", file, err)
		}
		for _, name := range names {
			if !strings.Contains(string(data), "## "+name) {
				t.Errorf("%s is missing operation %q", file, name)
			}
		}
	}
	check("CLI-Billing.md", opNames(billingOps))
	check("CLI-Pricing.md", opNames(pricingOps))
	check("CLI-Compute.md", opNames(computeOps))
	check("CLI-Network.md", opNames(networkOps))
	check("CLI-DNS.md", opNames(dnsOps))
	check("CLI-CDN.md", opNames(cdnOps))
	check("CLI-Monitor.md", opNames(monitorOps))
}

func TestGenDocsStartsWithTheGeneratedMarker(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", entry.Name(), err)
		}
		if !strings.HasPrefix(string(data), genDocsMarker) {
			t.Errorf("%s does not start with the generated marker", entry.Name())
		}
	}
}

func TestGenDocsNoOSDependentContent(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory to check against")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", entry.Name(), err)
		}
		if strings.Contains(string(data), home) {
			t.Errorf("%s contains the local home directory path", entry.Name())
		}
	}
}

func TestGenDocsExitCodeTableCoversAmbiguousProjectAndRetryUnauthorized(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data := string(mustReadGenDocsCLIMD(t, dir))
	if !strings.Contains(data, "ambiguous project") {
		t.Errorf("exit code table is missing the ambiguous-project case:\n%s", data)
	}
	// Wording must hold for a toggle write, which the SDK never retries, as
	// well as an ordinary request, which it retries once after a 401.
	if !strings.Contains(data, "except on a toggle write") {
		t.Errorf("exit code table is missing the toggle-write 401 case:\n%s", data)
	}
}

func TestGenDocsReadOnlyTextMentionsNumericOnValue(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data := string(mustReadGenDocsCLIMD(t, dir))
	if !strings.Contains(data, "read_only = 1") {
		t.Errorf("read-only text is missing the read_only = 1 example:\n%s", data)
	}
}

func TestGenDocsErrorCodeTextMentionsStatusFallback(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data := string(mustReadGenDocsCLIMD(t, dir))
	if !strings.Contains(data, "status-derived") {
		t.Errorf("error class text is missing the status-derived fallback:\n%s", data)
	}
}

func TestGenDocsErrorClassesMentionPageFormat(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data := string(mustReadGenDocsCLIMD(t, dir))
	if !strings.Contains(data, "PageFormat") {
		t.Errorf("error class text is missing PageFormat:\n%s", data)
	}
}

func TestGenDocsErrorClassesMentionMonitorCodes(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data := string(mustReadGenDocsCLIMD(t, dir))
	for _, want := range []string{"UnexpectedStatus", "StatusUnconfirmed"} {
		if !strings.Contains(data, want) {
			t.Errorf("error class text is missing %s:\n%s", want, data)
		}
	}
}

// TestGenDocsErrorClassesMentionDNSCodes checks that the error-classes list
// documents the three vDNS wait codes and that the Output-on-stdout rule for
// WriteFailed and NotSettled is stated.
func TestGenDocsErrorClassesMentionDNSCodes(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data := string(mustReadGenDocsCLIMD(t, dir))
	for _, want := range []string{"ZoneBusy", "WriteFailed", "NotSettled", "prints the Output on stdout"} {
		if !strings.Contains(data, want) {
			t.Errorf("error class text is missing %q:\n%s", want, data)
		}
	}
}

// TestGenDocsErrorClassesNameTheExitOneCodes checks that the sentence
// closing the error-classes list names every exit-1 code (now five, with
// the three vDNS wait codes) rather than a vague "exit 1", which would read
// as ambiguous after a list of thirteen classes.
func TestGenDocsErrorClassesNameTheExitOneCodes(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data := string(mustReadGenDocsCLIMD(t, dir))
	want := "`UnexpectedStatus`, `StatusUnconfirmed`, `ZoneBusy`, `WriteFailed`, and `NotSettled` all exit 1"
	if !strings.Contains(data, want) {
		t.Errorf("error class text does not name every exit-1 code:\n%s", data)
	}
	if strings.Contains(data, "Both exit 1") {
		t.Errorf("error class text still has the ambiguous \"Both exit 1\":\n%s", data)
	}
}

func TestGenDocsHasConfigureSectionAndCLIInputJSONSyntax(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data := string(mustReadGenDocsCLIMD(t, dir))
	for _, want := range []string{
		"## configure",
		"configure set <key> -",
		"password",
		"totp_secret",
		"read_only",
		"--cli-input-json",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("CLI.md is missing %q", want)
		}
	}
}

func TestGenDocsRemovesStaleServicePages(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	stalePath := filepath.Join(dir, "CLI-OldService.md")
	if err := os.WriteFile(stalePath, []byte(genDocsMarker+"\n\nstale\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale generated page was not removed: %v", err)
	}
}

func TestGenDocsNeverRemovesAHandAuthoredFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	handAuthored := filepath.Join(dir, "CLI-Notes.md")
	if err := os.WriteFile(handAuthored, []byte("# hand-written notes, no generated marker\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	if _, err := os.Stat(handAuthored); err != nil {
		t.Fatalf("a file without the generated marker must be left alone: %v", err)
	}
}

func mustReadGenDocsCLIMD(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "CLI.md"))
	if err != nil {
		t.Fatalf("ReadFile CLI.md: %v", err)
	}
	return data
}

// TestGenDocsCreateCheckExampleIncludesLocations checks that create-check's
// example command line is runnable as printed: Locations has no flag type,
// so a runnable example must set it through --cli-input-json, not leave it
// out the way a plain required-flag example would.
func TestGenDocsCreateCheckExampleIncludesLocations(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLI-Monitor.md"))
	if err != nil {
		t.Fatalf("ReadFile CLI-Monitor.md: %v", err)
	}
	want := `--cli-input-json '{"Locations":["<location-id>"]}'`
	if !strings.Contains(string(data), want) {
		t.Errorf("create-check example is missing %q:\n%s", want, data)
	}
}

// TestGenDocsUpdateHostedZoneExampleSetsAField checks that update-hosted-zone's
// example command line is runnable as printed. HostedZoneID is its only
// required Input field, but UpdateHostedZone itself also rejects a call that
// leaves both VPCIDs and Description unset, so a plain required-flags-only
// example would print a command that exits 2 with InvalidUsage when run as
// shown.
func TestGenDocsUpdateHostedZoneExampleSetsAField(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLI-DNS.md"))
	if err != nil {
		t.Fatalf("ReadFile CLI-DNS.md: %v", err)
	}
	want := "vngcloud dns update-hosted-zone --hosted-zone-id <hosted-zone-id> --description <description>"
	if !strings.Contains(string(data), want) {
		t.Errorf("update-hosted-zone example is missing %q:\n%s", want, data)
	}
}

func TestGenDocsCommandIsHidden(t *testing.T) {
	withCleanEnv(t)
	out, err := runConfigure(t, "", []string{"--help"})
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	if strings.Contains(out, "gen-docs") {
		t.Fatalf("gen-docs should be hidden from help output:\n%s", out)
	}
}

func TestGenDocsCommandRuns(t *testing.T) {
	withCleanEnv(t)
	dir := t.TempDir()
	if _, err := runConfigure(t, "", []string{"gen-docs", dir}); err != nil {
		t.Fatalf("gen-docs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLI.md")); err != nil {
		t.Fatalf("CLI.md was not written: %v", err)
	}
}
