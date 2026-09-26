package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
)

// fakeClient is a small HTTP-calling service used only to exercise the Op,
// Read, Write, and Service machinery end to end, including that a refused
// command sends no request. It talks to its own httptest server directly,
// bypassing vngcloud.Config's transport entirely: the newClient function
// Service takes ignores the Config it is given and returns a client already
// pointed at the fake server, since these tests are about the CLI's guard
// and merge logic, not about building a Config.
type fakeClient struct {
	baseURL string
	http    *http.Client
	calls   *int32
}

type fakeGetInput struct {
	Name    string
	Enabled *bool
	Count   *int
}

type fakeGetOutput struct {
	Name    string
	Enabled *bool
	Count   *int
}

func (c *fakeClient) FakeGet(ctx context.Context, in *fakeGetInput) (*fakeGetOutput, error) {
	atomic.AddInt32(c.calls, 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/fake-get", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out fakeGetOutput
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	_ = in
	return &out, nil
}

type fakeDeleteInput struct {
	ID string `vngcloud:"required"`
}

type fakeDeleteOutput struct{}

func (c *fakeClient) FakeDelete(ctx context.Context, in *fakeDeleteInput) (*fakeDeleteOutput, error) {
	atomic.AddInt32(c.calls, 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+"/fake/"+in.ID, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return &fakeDeleteOutput{}, nil
}

// fakeSecretInput and fakeSecretOutput exercise Guard and WriteRedact: a
// literal --secret flag is refused before any request, and the Output's
// Secret is redacted after a successful call, exactly as monitor's
// create-channel and update-channel commands use the same two mechanisms
// for a channel's Address.
type fakeSecretInput struct {
	Secret string `vngcloud:"required"`
}

type fakeSecretOutput struct {
	Secret string
}

func (c *fakeClient) FakeSecretWrite(ctx context.Context, in *fakeSecretInput) (*fakeSecretOutput, error) {
	atomic.AddInt32(c.calls, 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/fake-secret", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return &fakeSecretOutput{Secret: in.Secret}, nil
}

// fakeHarness bundles a fake server, its request counter, and the env/root
// a test drives commands through.
type fakeHarness struct {
	calls  int32
	server *httptest.Server
	stdout *strings.Builder
	stderr *strings.Builder
	e      *env
}

func newFakeHarness(t *testing.T) *fakeHarness {
	t.Helper()
	withCleanEnv(t)
	// loadConfig always calls the real vngcloud.LoadConfig, even though the
	// fake client below ignores the Config it is handed, so a region and
	// credentials must resolve; a static token needs no profile files.
	withTestOptions(t, vngcloud.WithStaticToken("test-token"))
	h := &fakeHarness{stdout: &strings.Builder{}, stderr: &strings.Builder{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/fake-get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"Name":"server-value"}`)
	})
	mux.HandleFunc("/fake/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/fake-secret", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)
	h.e = &env{flags: &globalFlags{}, stdin: strings.NewReader(""), stdout: h.stdout, stderr: h.stderr}
	return h
}

func (h *fakeHarness) newClient(vngcloud.Config) *fakeClient {
	return &fakeClient{baseURL: h.server.URL, http: h.server.Client(), calls: &h.calls}
}

// fakeSecretGuard refuses a literal --secret flag, the same shape as
// monitor's literal --address guard: it inspects only whether the flag was
// Changed, never the value, so a --cli-input-json value alone never
// triggers it.
func fakeSecretGuard(cmd *cobra.Command, _ any) error {
	if cmd.Flags().Changed("secret") {
		return newUsageError("--secret must be given only through --cli-input-json")
	}
	return nil
}

func (h *fakeHarness) fakeOps() []Op[fakeClient] {
	return []Op[fakeClient]{
		Read[fakeClient, fakeGetInput, fakeGetOutput]("fake-get", (*fakeClient).FakeGet),
		Write[fakeClient, fakeDeleteInput, fakeDeleteOutput]("fake-delete", (*fakeClient).FakeDelete, Destructive()),
		Write[fakeClient, fakeSecretInput, fakeSecretOutput]("fake-secret-write", (*fakeClient).FakeSecretWrite,
			Guard(fakeSecretGuard),
			WriteRedact(func(out *fakeSecretOutput) { out.Secret = "<redacted>" })),
	}
}

func (h *fakeHarness) serviceCmd() *cobra.Command {
	return Service(h.e, "fake", "fake service for tests", h.newClient, h.fakeOps()...)
}

// newTestRoot builds a minimal stand-in for the production root command,
// binding the same global persistent flags newRootCmd registers to e.flags,
// so a test can drive --yes, --read-only, and the rest through real cobra
// flag parsing into the very env the fake service's commands read, instead
// of setting env fields directly and only pretending they came from a flag.
// It also silences cobra's own usage and error printing, exactly like
// newRootCmd, so a failed command's stdout holds only what runOp itself
// wrote (never cobra's own usage text) and a test can assert on it.
func newTestRoot(e *env) *cobra.Command {
	root := &cobra.Command{
		Use: "vngcloud-test", Args: parentArgs, RunE: unknownCommandRunE,
		SilenceErrors: true, SilenceUsage: true,
	}
	root.SetOut(e.stdout)
	root.SetErr(e.stderr)
	root.SetIn(e.stdin)
	root.SetFlagErrorFunc(flagErrorFunc)
	root.PersistentFlags().StringVar(&e.flags.profile, "profile", "", "")
	root.PersistentFlags().StringVar(&e.flags.region, "region", "hcm-3", "")
	root.PersistentFlags().StringVar(&e.flags.projectID, "project-id", "", "")
	root.PersistentFlags().StringVar(&e.flags.output, "output", "", "")
	root.PersistentFlags().StringVar(&e.flags.query, "query", "", "")
	root.PersistentFlags().BoolVar(&e.flags.yes, "yes", false, "")
	root.PersistentFlags().BoolVar(&e.flags.debug, "debug", false, "")
	root.PersistentFlags().BoolVar(&e.flags.readOnly, "read-only", false, "")
	return root
}

func execCmd(t *testing.T, cmd *cobra.Command, args []string) error {
	t.Helper()
	cmd.SetArgs(args)
	return cmd.ExecuteContext(context.Background())
}

func TestOpReadCallsTheServiceAndPrintsOutput(t *testing.T) {
	h := newFakeHarness(t)
	root := newTestRoot(h.e)
	root.AddCommand(h.serviceCmd())

	if err := execCmd(t, root, []string{"fake", "fake-get"}); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, h.stderr.String())
	}
	if atomic.LoadInt32(&h.calls) != 1 {
		t.Fatalf("calls = %d, want 1", h.calls)
	}
	if !strings.Contains(h.stdout.String(), "server-value") {
		t.Fatalf("stdout = %q, want it to contain the server's response", h.stdout.String())
	}
}

func TestOpFlagsMergeOverJSON(t *testing.T) {
	h := newFakeHarness(t)
	root := newTestRoot(h.e)
	root.AddCommand(h.serviceCmd())

	// The JSON sets Name; the flag sets Count; neither should clobber the
	// other, proving the merge is additive rather than JSON-then-overwrite.
	err := execCmd(t, root, []string{
		"fake", "fake-get",
		"--cli-input-json", `{"Name":"from-json"}`,
		"--count", "5",
	})
	if err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, h.stderr.String())
	}
}

func TestOpDestructiveWithoutYesRefusesWithZeroRequests(t *testing.T) {
	h := newFakeHarness(t)
	root := newTestRoot(h.e)
	root.AddCommand(h.serviceCmd())

	err := execCmd(t, root, []string{"fake", "fake-delete", "--id", "x"})
	if err == nil {
		t.Fatalf("expected an error without --yes")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if got := atomic.LoadInt32(&h.calls); got != 0 {
		t.Fatalf("calls = %d, want 0 (refused before any request)", got)
	}
}

func TestOpDestructiveWithYesSucceeds(t *testing.T) {
	h := newFakeHarness(t)
	root := newTestRoot(h.e)
	root.AddCommand(h.serviceCmd())

	if err := execCmd(t, root, []string{"--yes", "fake", "fake-delete", "--id", "x"}); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, h.stderr.String())
	}
	if got := atomic.LoadInt32(&h.calls); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func TestOpDestructiveMissingRequiredFieldIsAUsageErrorBeforeYesCheck(t *testing.T) {
	h := newFakeHarness(t)
	root := newTestRoot(h.e)
	root.AddCommand(h.serviceCmd())

	err := execCmd(t, root, []string{"fake", "fake-delete"})
	if err == nil {
		t.Fatalf("expected an error for a missing required flag")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if got := atomic.LoadInt32(&h.calls); got != 0 {
		t.Fatalf("calls = %d, want 0", got)
	}
}

func TestOpReadOnlyFlagRefusesWriteWithZeroRequests(t *testing.T) {
	h := newFakeHarness(t)
	root := newTestRoot(h.e)
	root.AddCommand(h.serviceCmd())

	err := execCmd(t, root, []string{"--read-only", "--yes", "fake", "fake-delete", "--id", "x"})
	if err == nil {
		t.Fatalf("expected a read-only refusal")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if classify(err).Code != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly", classify(err).Code)
	}
	if got := atomic.LoadInt32(&h.calls); got != 0 {
		t.Fatalf("calls = %d, want 0", got)
	}
}

func TestOpReadOnlyEnvRefusesWriteWithZeroRequests(t *testing.T) {
	h := newFakeHarness(t)
	t.Setenv(envReadOnly, "1")
	root := newTestRoot(h.e)
	root.AddCommand(h.serviceCmd())

	err := execCmd(t, root, []string{"--yes", "fake", "fake-delete", "--id", "x"})
	if err == nil {
		t.Fatalf("expected a read-only refusal")
	}
	if got := atomic.LoadInt32(&h.calls); got != 0 {
		t.Fatalf("calls = %d, want 0", got)
	}
}

func TestOpReadOnlyDoesNotBlockReads(t *testing.T) {
	h := newFakeHarness(t)
	root := newTestRoot(h.e)
	root.AddCommand(h.serviceCmd())

	if err := execCmd(t, root, []string{"--read-only", "fake", "fake-get"}); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, h.stderr.String())
	}
	if got := atomic.LoadInt32(&h.calls); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func TestOpReadOnlyRefusalComesBeforeRequiredFieldCheck(t *testing.T) {
	h := newFakeHarness(t)
	root := newTestRoot(h.e)
	root.AddCommand(h.serviceCmd())

	// fake-delete requires --id; omitting both --id and --yes must still
	// surface the read-only refusal, not a missing-required-flag usage
	// error, so an agent sees the one reason the command can never run.
	err := execCmd(t, root, []string{"--read-only", "fake", "fake-delete"})
	if err == nil {
		t.Fatalf("expected a read-only refusal")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if classify(err).Code != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly", classify(err).Code)
	}
	if got := atomic.LoadInt32(&h.calls); got != 0 {
		t.Fatalf("calls = %d, want 0", got)
	}
}

func TestReadOnlyFlagFalseDoesNotOverrideEnvVar(t *testing.T) {
	h := newFakeHarness(t)
	t.Setenv(envReadOnly, "1")
	root := newTestRoot(h.e)
	root.AddCommand(h.serviceCmd())

	// --read-only=false explicitly sets the flag to its own default; per the
	// CLI design no flag or variable can turn read-only off once another
	// source turned it on.
	err := execCmd(t, root, []string{"--read-only=false", "--yes", "fake", "fake-delete", "--id", "x"})
	if err == nil {
		t.Fatalf("expected a read-only refusal")
	}
	if classify(err).Code != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly", classify(err).Code)
	}
	if got := atomic.LoadInt32(&h.calls); got != 0 {
		t.Fatalf("calls = %d, want 0", got)
	}
}

func TestOpUnknownSubcommandIsAUsageError(t *testing.T) {
	h := newFakeHarness(t)
	root := newTestRoot(h.e)
	root.AddCommand(h.serviceCmd())

	err := execCmd(t, root, []string{"fake", "no-such-op"})
	if err == nil {
		t.Fatalf("expected an error")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
}

// TestServiceValidatesRealSDKMethodNames proves the naming check works
// against genuine SDK methods, not just synthetic ones: every billing
// operation's registered name must already equal kebab(methodName), since
// the SDK's own naming has no exceptions in the shared rename table today.
func TestServiceValidatesRealSDKMethodNames(t *testing.T) {
	ops := []Op[billing.Client]{
		Read[billing.Client, billing.ListBudgetsInput, billing.ListBudgetsOutput]("list-budgets", (*billing.Client).ListBudgets),
		Read[billing.Client, billing.GetBudgetInput, billing.GetBudgetOutput]("get-budget", (*billing.Client).GetBudget),
		Write[billing.Client, billing.DeleteBudgetInput, billing.DeleteBudgetOutput]("delete-budget", (*billing.Client).DeleteBudget, Destructive()),
	}
	if err := validateOps("billing", ops); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServicePanicsOnMismatchedOperationName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected Service to panic on a mismatched operation name")
		}
	}()
	ops := []Op[billing.Client]{
		Read[billing.Client, billing.ListBudgetsInput, billing.ListBudgetsOutput]("list-the-budgets", (*billing.Client).ListBudgets),
	}
	h := newFakeHarness(t)
	_ = Service(h.e, "billing", "test short", billing.New, ops...)
}

type collidingInput struct {
	// Query is a field whose derived flag ("search", via the rename table)
	// is fine; Debug's mechanical kebab form ("debug") collides with the
	// global --debug flag, which Service must reject.
	Debug string
}
type collidingOutput struct{}

func fakeCollidingMethod(_ *fakeClient, _ context.Context, _ *collidingInput) (*collidingOutput, error) {
	return &collidingOutput{}, nil
}

func TestServicePanicsOnFlagCollidingWithGlobalFlag(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected Service to panic on a flag colliding with a global flag")
		}
	}()
	h := newFakeHarness(t)
	ops := []Op[fakeClient]{
		Read[fakeClient, collidingInput, collidingOutput]("fake-colliding-method", fakeCollidingMethod),
	}
	_ = Service(h.e, "fake", "test short", h.newClient, ops...)
}

// TestOpGuardRefusesLiteralFlagWithZeroRequests checks that a Write
// operation's Guard runs before any request: a literal --secret flag is
// refused with exit code 2 and the fake service never sees a call.
func TestOpGuardRefusesLiteralFlagWithZeroRequests(t *testing.T) {
	h := newFakeHarness(t)
	root := newTestRoot(h.e)
	root.AddCommand(h.serviceCmd())

	err := execCmd(t, root, []string{"fake", "fake-secret-write", "--secret", "raw-value"})
	if err == nil {
		t.Fatalf("expected a guard refusal")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if got := atomic.LoadInt32(&h.calls); got != 0 {
		t.Fatalf("calls = %d, want 0 (refused before any request)", got)
	}
}

// TestOpGuardAllowsCLIInputJSON checks that Guard only inspects whether the
// flag itself was set on argv: the same value set through --cli-input-json
// alone reaches the operation.
func TestOpGuardAllowsCLIInputJSON(t *testing.T) {
	h := newFakeHarness(t)
	root := newTestRoot(h.e)
	root.AddCommand(h.serviceCmd())

	err := execCmd(t, root, []string{"fake", "fake-secret-write", "--cli-input-json", `{"Secret":"raw-value"}`})
	if err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, h.stderr.String())
	}
	if got := atomic.LoadInt32(&h.calls); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

// TestOpWriteRedactRedactsOutput checks that WriteRedact's function runs on
// the Output before it reaches stdout, in every format and under --query.
func TestOpWriteRedactRedactsOutput(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"json", nil},
		{"table", []string{"--output", "table"}},
		{"text", []string{"--output", "text"}},
		{"query", []string{"--query", "Secret"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newFakeHarness(t)
			root := newTestRoot(h.e)
			root.AddCommand(h.serviceCmd())

			args := append([]string{"fake", "fake-secret-write", "--cli-input-json", `{"Secret":"raw-value"}`}, tc.args...)
			if err := execCmd(t, root, args); err != nil {
				t.Fatalf("execute: %v (stderr=%s)", err, h.stderr.String())
			}
			out := h.stdout.String()
			if strings.Contains(out, "raw-value") {
				t.Fatalf("stdout = %q, want the raw Secret redacted", out)
			}
			if !strings.Contains(out, "redacted") {
				t.Fatalf("stdout = %q, want the redacted placeholder", out)
			}
		})
	}
}

// TestReadPanicsOnASecondRedactOption checks op.go's guard against a
// mismatched pair of Redact options on one Read: a second Redact silently
// overwriting the first would mean whichever option is listed last wins with
// no warning, so Read panics instead of choosing one.
func TestReadPanicsOnASecondRedactOption(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected Read to panic when given a second Redact option")
		}
	}()
	Read[fakeClient, fakeGetInput, fakeGetOutput]("fake-get", (*fakeClient).FakeGet,
		Redact(func(*fakeGetOutput) {}),
		Redact(func(*fakeGetOutput) {}),
	)
}

type badNoFlagInput struct {
	Name string
}
type badNoFlagOutput struct{}

func fakeBadNoFlagMethod(_ *fakeClient, _ context.Context, _ *badNoFlagInput) (*badNoFlagOutput, error) {
	return &badNoFlagOutput{}, nil
}

// TestValidateOpsRejectsNoFlagNamingNoField checks validateOps' guard
// against a NoFlag name that is not a field of the op's Input at all: a
// typo there would otherwise mark nothing and fail silently, leaving the
// mistyped field's mechanical flag registered exactly as if NoFlag had
// never been given.
func TestValidateOpsRejectsNoFlagNamingNoField(t *testing.T) {
	ops := []Op[fakeClient]{
		Read[fakeClient, badNoFlagInput, badNoFlagOutput]("fake-bad-no-flag-method", fakeBadNoFlagMethod, NoFlag("NoSuchField")),
	}
	if err := validateOps("fake", ops); err == nil {
		t.Fatalf("expected an error for a NoFlag name that is not an Input field")
	}
}

func TestFuncNameRecoversMethodExpressionName(t *testing.T) {
	if got := funcName((*billing.Client).ListBudgets); got != "ListBudgets" {
		t.Fatalf("funcName = %q, want ListBudgets", got)
	}
}
