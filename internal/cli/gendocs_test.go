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
	for _, name := range []string{"CLI.md", "CLI-Billing.md", "CLI-Pricing.md", "CLI-Compute.md", "CLI-Network.md", "CLI-DNS.md", "CLI-CDN.md", "CLI-Monitor.md", "CLI-Project.md", "CLI-Portal.md"} {
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
	check("CLI-Project.md", opNames(projectOps))
	check("CLI-Portal.md", opNames(portalOps))
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

// TestGenDocsErrorClassesMentionNotFound checks that the error-classes list
// documents NotFound as a class name in its own right, not just as the
// "code" value inside the leading JSON example: a not-found result such as
// monitor.GetChannel's page walk can reach the CLI without ever becoming an
// *APIError, so it needs a class entry of its own alongside PageFormat and
// the vDNS and vMonitor codes.
func TestGenDocsErrorClassesMentionNotFound(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data := string(mustReadGenDocsCLIMD(t, dir))
	if !strings.Contains(data, "`NotFound`") {
		t.Errorf("error class text is missing `NotFound` as a documented class:\n%s", data)
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

// TestGenDocsMonitorChannelOpsDocumentRedaction checks that list-channels
// and get-channel's own sections of the generated page state the CLI's
// channel redaction rule, so a reader learns it from the wiki instead of
// having to find internal/cli/redact.go.
func TestGenDocsMonitorChannelOpsDocumentRedaction(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLI-Monitor.md"))
	if err != nil {
		t.Fatalf("ReadFile CLI-Monitor.md: %v", err)
	}
	for _, op := range []string{"list-channels", "get-channel"} {
		section := genDocsSection(t, string(data), op)
		if !strings.Contains(section, "edact") {
			t.Errorf("%s section is missing a redaction note:\n%s", op, section)
		}
		if !strings.Contains(section, "Email, SMS, and Telegram") {
			t.Errorf("%s section does not name the address types that print in full:\n%s", op, section)
		}
	}
}

// TestGenDocsPortalOpsDocumentAccountDataAndRedaction checks that
// get-user-info's section names the account-data risk, and that every
// portal operation's section states the map-key redaction rule, so a reader
// learns both from the wiki instead of internal/cli/redact_maps.go.
func TestGenDocsPortalOpsDocumentAccountDataAndRedaction(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLI-Portal.md"))
	if err != nil {
		t.Fatalf("ReadFile CLI-Portal.md: %v", err)
	}

	userInfo := genDocsSection(t, string(data), "get-user-info")
	if !strings.Contains(userInfo, "account data") {
		t.Errorf("get-user-info section is missing the account-data note:\n%s", userInfo)
	}

	for _, op := range []string{"get-user-info", "list-zones", "list-quota-used", "get-quota", "get-tag-quota"} {
		section := genDocsSection(t, string(data), op)
		if !strings.Contains(section, "<redacted>") {
			t.Errorf("%s section is missing the redaction note:\n%s", op, section)
		}
	}
}

// genDocsSection returns op's own heading block from a rendered service
// page: from "## op\n" up to (not including) the next "## " heading, or to
// the end of data when op is the last one on the page.
func genDocsSection(t *testing.T, data, op string) string {
	t.Helper()
	heading := "## " + op + "\n"
	start := strings.Index(data, heading)
	if start == -1 {
		t.Fatalf("no %q heading found:\n%s", heading, data)
	}
	rest := data[start+len(heading):]
	if next := strings.Index(rest, "\n## "); next != -1 {
		return rest[:next]
	}
	return rest
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

// TestGenDocsCreateChannelExampleUsesCLIInputJSONFile checks that
// create-channel's generated example is runnable as printed: Address is a
// required, flag-settable field, but the CLI's own guard refuses it as a
// literal flag for a Webhook or Slack channel, so the example must show
// --cli-input-json file://channel.json instead of a literal --address flag.
func TestGenDocsCreateChannelExampleUsesCLIInputJSONFile(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLI-Monitor.md"))
	if err != nil {
		t.Fatalf("ReadFile CLI-Monitor.md: %v", err)
	}
	section := genDocsSection(t, string(data), "create-channel")
	example := genDocsExampleBlock(t, section)
	if !strings.Contains(example, "--cli-input-json file://channel.json") {
		t.Errorf("create-channel example is missing the file:// form: %q", example)
	}
	if strings.Contains(example, "--address") {
		t.Errorf("create-channel example still shows a literal --address flag: %q", example)
	}
}

// TestGenDocsUpdateChannelExampleUsesCLIInputJSONFile mirrors
// TestGenDocsCreateChannelExampleUsesCLIInputJSONFile for update-channel,
// whose --address guard is unconditional.
func TestGenDocsUpdateChannelExampleUsesCLIInputJSONFile(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLI-Monitor.md"))
	if err != nil {
		t.Fatalf("ReadFile CLI-Monitor.md: %v", err)
	}
	section := genDocsSection(t, string(data), "update-channel")
	example := genDocsExampleBlock(t, section)
	if !strings.Contains(example, "--cli-input-json file://channel.json") {
		t.Errorf("update-channel example is missing the file:// form: %q", example)
	}
	if strings.Contains(example, "--address") {
		t.Errorf("update-channel example still shows an --address flag: %q", example)
	}
}

// genDocsExampleBlock returns the ```sh ... ``` example command line inside
// section, the flag table's own --address row.
func genDocsExampleBlock(t *testing.T, section string) string {
	t.Helper()
	const open = "```sh\n"
	start := strings.Index(section, open)
	if start == -1 {
		t.Fatalf("no %q code block found:\n%s", open, section)
	}
	rest := section[start+len(open):]
	end := strings.Index(rest, "```")
	if end == -1 {
		t.Fatalf("unterminated code block:\n%s", section)
	}
	return rest[:end]
}

// TestGenDocsChannelWritesDocumentTheAddressGuard checks that create-channel
// and update-channel each state their own literal-address refusal, so a
// reader learns the guard's exit code and the --cli-input-json escape from
// the wiki instead of having to find internal/cli/svc_monitor.go.
func TestGenDocsChannelWritesDocumentTheAddressGuard(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLI-Monitor.md"))
	if err != nil {
		t.Fatalf("ReadFile CLI-Monitor.md: %v", err)
	}
	for _, op := range []string{"create-channel", "update-channel"} {
		section := genDocsSection(t, string(data), op)
		if !strings.Contains(section, "exit code 2") {
			t.Errorf("%s section is missing the guard's exit code:\n%s", op, section)
		}
		if !strings.Contains(section, "--cli-input-json file://channel.json") {
			t.Errorf("%s section does not name the --cli-input-json escape:\n%s", op, section)
		}
	}
}

// TestGenDocsNoFlagFieldIsJSONOnlyNotAFlag checks the CLI reads design's
// NoFlag rule end to end through gen-docs: project's list-projects page must
// document Region as settable only through --cli-input-json, and must never
// show it as a --region flag, which would collide with the global flag of
// the same name.
func TestGenDocsNoFlagFieldIsJSONOnlyNotAFlag(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLI-Project.md"))
	if err != nil {
		t.Fatalf("ReadFile CLI-Project.md: %v", err)
	}
	want := "`Region` (via `--cli-input-json` only)"
	if !strings.Contains(string(data), want) {
		t.Errorf("list-projects doc is missing %q:\n%s", want, data)
	}
	if strings.Contains(string(data), "`--region`") {
		t.Errorf("list-projects doc must not list --region as a flag:\n%s", data)
	}
}

// TestGenDocsGetExamplesQueryTheWrappedResourceField checks the CLI reads
// design's "Commands" rendering note: a Get whose Output wraps one resource
// gets a --query <Field> in its wiki example, so running the example as
// printed under table or text prints columns instead of one compact-JSON
// cell. This is a shared gendocs.go change, so it reaches every existing
// service with that Output shape, not only project and portal.
func TestGenDocsGetExamplesQueryTheWrappedResourceField(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	cases := []struct{ file, want string }{
		{"CLI-Billing.md", "vngcloud billing get-budget --budget-uuid <budget-uuid> --query Budget"},
		{"CLI-Compute.md", "vngcloud compute get-server --server-id <server-id> --query Server"},
		{"CLI-Network.md", "vngcloud network get-vpc --vpc-id <vpc-id> --query VPC"},
		{"CLI-DNS.md", "vngcloud dns get-hosted-zone --hosted-zone-id <hosted-zone-id> --query HostedZone"},
		{"CLI-Monitor.md", "vngcloud monitor get-check --check-id <check-id> --query Check"},
		{"CLI-Portal.md", "vngcloud portal get-quota --name <name> --query Quota"},
		{"CLI-Portal.md", "vngcloud portal get-user-info --query UserInfo"},
		{"CLI-Portal.md", "vngcloud portal get-tag-quota --query TagQuota"},
	}
	for _, tt := range cases {
		data, err := os.ReadFile(filepath.Join(dir, tt.file))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", tt.file, err)
		}
		if !strings.Contains(string(data), tt.want) {
			t.Errorf("%s is missing %q:\n%s", tt.file, tt.want, data)
		}
	}
}

// TestGenDocsListExampleHasNoQueryField checks that a List op, whose Output
// holds Items rather than one wrapped resource, never gets the Get-only
// --query <Field> addition, even though core.List and core.PagedList have
// as few as one exported field (Items) themselves.
func TestGenDocsListExampleHasNoQueryField(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "CLI-Project.md"))
	if err != nil {
		t.Fatalf("ReadFile CLI-Project.md: %v", err)
	}
	if strings.Contains(string(data), "list-projects --query") {
		t.Errorf("list-projects example must not query a wrapped field:\n%s", data)
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
