package cli

import (
	"context"
	"errors"
	"io"
	"reflect"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/cdn"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/containerregistry"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/iam"
	"danny.vn/vngcloud/loadbalancer"
	"danny.vn/vngcloud/network"
	"danny.vn/vngcloud/storage"
	"danny.vn/vngcloud/tagging"
	"danny.vn/vngcloud/volume"
)

type opKind int

const (
	kindRead opKind = iota
	kindWrite
)

// Op is one operation of a service's typed operation table, parameterized
// only by the client type C. Its own Input and Output types are erased
// behind newInput, newOutput, and call, so a []Op[C] can hold operations with
// differing Input and Output types side by side, while the compiler still
// rejects an Op built from another client type's method.
type Op[C any] struct {
	name        string
	short       string
	render      func(io.Writer, string, string, any) error
	methodName  string
	kind        opKind
	destructive bool
	guard       func(cmd *cobra.Command, in any) error
	noFlag      map[string]bool
	// optional holds Input fields whose vngcloud:"required" tag the CLI
	// does not enforce for this op; see Optional.
	optional map[string]bool
	// globalProject names the Input field the global --project-id flag
	// fills, for an op whose project is not the account's vServer project.
	globalProject string
	newInput      func() any
	newOutput     func() any
	call          func(cmd *cobra.Command, client *C, ctx context.Context, in any) (any, error)
	// extraFlags registers a flag beyond those flagSpecsFor derives from the
	// Input struct, for an op whose command needs one its Input carries no
	// field for. compute's create-ssh-key is the only op that sets this
	// today, for --secret-file; see internal/cli/secretfile.go.
	extraFlags func(cmd *cobra.Command)
}

// writeOption configures a Write operation. Destructive, Guard, WriteRedact,
// WriteNoFlag, and WriteGlobalProjectID are the five today; cli.WaitFor (for an asynchronous
// write) is undefined until the first such write needs it.
type writeOption struct {
	destructive bool
	guard       func(cmd *cobra.Command, in any) error
	redact      func(out any)
	noFlag      map[string]bool
	// globalProject is set by WriteGlobalProjectID.
	globalProject string
}

// Destructive marks a Write operation as not undoable by one more command:
// it fails with exit code 2 and names --yes unless --yes is given.
func Destructive() writeOption { return writeOption{destructive: true} }

// Guard adds a check that runs on a Write operation's merged Input, after
// --cli-input-json and every flag are applied but before any request: fn
// returns a usage error to refuse the command, or nil to let it proceed.
// cmd lets fn tell a flag the user actually typed (cmd.Flags().Changed)
// apart from a value --cli-input-json alone set, since only the former
// reaches argv, and so process listings and shell history. monitor's
// create-channel and update-channel use it to refuse a literal --address
// for a channel whose address can carry a secret.
func Guard(fn func(cmd *cobra.Command, in any) error) writeOption {
	return writeOption{guard: fn}
}

// WriteNoFlag marks Input fields of a Write operation that stay settable
// only through --cli-input-json, the Write-side counterpart to Read's
// NoFlag (whose own doc comment gives the mechanism). dns's
// CreateHostedZoneInput.VPCIDs and monitor's CreateCheckInput.Locations use
// it: both are required []string fields, a type flags.go can bind a
// repeatable flag to once compute's CreateServerInput.SecurityGroupIDs needs
// that support, but giving either of those two writes a new flag is outside
// the design that added it, so both keep their pre-existing
// --cli-input-json-only behavior.
func WriteNoFlag(fields ...string) writeOption {
	m := make(map[string]bool, len(fields))
	for _, f := range fields {
		m[f] = true
	}
	return writeOption{noFlag: m}
}

// WriteRedact marks a Write operation's Output as holding a value the CLI
// must never print unchanged, Write's counterpart to Read's Redact: fn runs
// on the SDK's own result immediately after the call returns, on every path
// that returns a non-nil Output, including the one where a caller error
// (such as dns.ErrFailed) still carries an Output the CLI prints on stderr's
// error path. That single point runs before the result can reach
// renderOutput in any format or survive any --query, on both the success and
// that error path.
func WriteRedact[Out any](fn func(*Out)) writeOption {
	return writeOption{redact: func(out any) { fn(out.(*Out)) }}
}

// WriteGlobalProjectID is GlobalProjectID for a Write operation: the global
// --project-id flag fills the named Input field, which gets no flag of its
// own, and an empty result exits 2 before any request.
func WriteGlobalProjectID(field string) writeOption {
	return writeOption{noFlag: map[string]bool{field: true}, globalProject: field}
}

// readOption configures a Read operation: NoFlag, Optional, GlobalProjectID,
// and Redact.
type readOption struct {
	noFlag        map[string]bool
	optional      map[string]bool
	globalProject string
	redact        any // func(*Out) for the Read's own Out, checked in Read
}

// Redact marks a Read operation's Output as holding a value the CLI must
// never print unchanged: fn runs on the SDK's own result and mutates it in
// place, before the result reaches renderOutput, so a secret it holds can
// never reach json, table, or text output, or survive a --query, by any
// flag.
func Redact[Out any](fn func(*Out)) readOption {
	return readOption{redact: fn}
}

// NoFlag marks Input fields that stay settable only through
// --cli-input-json: flags.go never derives a flag for one, validateOps skips
// it in the global-flag collision check, and gen-docs lists it as JSON-only.
// project's ListProjectsInput.Region needs this because its mechanical flag
// name ("--region") would collide with the global --region flag, and the
// field only filters a result the global flag already scopes; see the CLI
// reads design's "project" section.
func NoFlag(fields ...string) readOption {
	m := make(map[string]bool, len(fields))
	for _, f := range fields {
		m[f] = true
	}
	return readOption{noFlag: m}
}

// Optional marks Input fields that this operation does not require, though
// their vngcloud:"required" tag does: the CLI's required-flag check skips
// them and gen-docs lists them as not required. The three create quotes use
// it for the fields that do not change a price. The SDK still checks the
// shape of a value that is set. validateOps rejects a name that is not a
// field of the Input.
func Optional(fields ...string) readOption {
	m := make(map[string]bool, len(fields))
	for _, f := range fields {
		m[f] = true
	}
	return readOption{optional: m}
}

// GlobalProjectID marks an Input field that the global --project-id flag
// fills, in place of a flag of its own that would collide with it. Only the
// flag counts: a project ID from the environment or the profile is the
// account's vServer project, which is not a vStorage project. A command that
// leaves the field empty fails with exit 2 before any request.
func GlobalProjectID(field string) readOption {
	return readOption{noFlag: map[string]bool{field: true}, globalProject: field}
}

// Read registers a read operation: name is its kebab-case command name, and
// method is an SDK method expression such as (*compute.Client).ListServers.
// opts holds NoFlag, Optional, GlobalProjectID, and Redact where an operation
// needs them. A Redact
// whose function does not take this Read's Output panics at registration.
func Read[C, In, Out any](name string, method func(*C, context.Context, *In) (*Out, error), opts ...readOption) Op[C] {
	var redact func(*Out)
	noFlag := map[string]bool{}
	optional := map[string]bool{}
	globalProject := ""
	for _, o := range opts {
		if o.globalProject != "" {
			globalProject = o.globalProject
		}
		for f := range o.noFlag {
			noFlag[f] = true
		}
		for f := range o.optional {
			optional[f] = true
		}
		if o.redact != nil {
			if redact != nil {
				panic("cli: " + name + " was given a second Redact option; an op takes at most one")
			}
			fn, ok := o.redact.(func(*Out))
			if !ok {
				panic("cli: Redact function does not match the Output of " + name)
			}
			redact = fn
		}
	}
	return Op[C]{
		name:          name,
		methodName:    funcName(method),
		kind:          kindRead,
		noFlag:        noFlag,
		optional:      optional,
		globalProject: globalProject,
		newInput:      func() any { return new(In) },
		newOutput:     func() any { return new(Out) },
		call: func(_ *cobra.Command, client *C, ctx context.Context, in any) (any, error) {
			out, err := method(client, ctx, in.(*In))
			if err != nil {
				return nil, err
			}
			if redact != nil {
				redact(out)
			}
			return out, nil
		},
	}
}

// Write registers a write operation: one that changes server state, whatever
// its HTTP method. --debug logs write started and write finished around the
// call; under read-only, or without --yes for a Destructive write, the
// command is refused before a client is built.
func Write[C, In, Out any](name string, method func(*C, context.Context, *In) (*Out, error), opts ...writeOption) Op[C] {
	var redact func(out any)
	op := Op[C]{
		name:       name,
		methodName: funcName(method),
		kind:       kindWrite,
		newInput:   func() any { return new(In) },
		newOutput:  func() any { return new(Out) },
	}
	for _, o := range opts {
		if o.destructive {
			op.destructive = true
		}
		if o.guard != nil {
			op.guard = o.guard
		}
		if o.redact != nil {
			redact = o.redact
		}
		if o.globalProject != "" {
			op.globalProject = o.globalProject
		}
		for f := range o.noFlag {
			if op.noFlag == nil {
				op.noFlag = map[string]bool{}
			}
			op.noFlag[f] = true
		}
	}
	op.call = func(_ *cobra.Command, client *C, ctx context.Context, in any) (any, error) {
		out, err := method(client, ctx, in.(*In))
		// redact runs whenever out is non-nil, whether or not err is also
		// set: a write whose design defines a failed or unsettled wait, such
		// as dns.ErrFailed, still returns an Output the CLI prints on the
		// error path, and that Output must reach the redact hook exactly the
		// same as the success path's does.
		if out != nil && redact != nil {
			redact(out)
		}
		return out, err
	}
	return op
}

// funcName recovers the Go name of an SDK method expression such as
// (*compute.Client).ListServers, for example "ListServers". A method
// expression compiles to a plain, unwrapped function, so
// runtime.FuncForPC reports its fully qualified name with no receiver value
// bound to it; only the last path segment is kept.
func funcName(method any) string {
	ptr := reflect.ValueOf(method).Pointer()
	full := runtime.FuncForPC(ptr).Name()
	full = strings.TrimSuffix(full, "-fm")
	if idx := strings.LastIndex(full, "."); idx >= 0 {
		full = full[idx+1:]
	}
	return full
}

// Service builds the cobra command for one SDK service: a parent command
// named name, with one subcommand per op. short is its one-line --help
// summary, shown next to name in the parent command's own listing. It panics
// if any op's registered name does not match the kebab-case form of its SDK
// method (outside the rename table), or if any op's Input field would derive
// a flag name that collides with a global flag: both are programmer mistakes
// in the operation table, not something a CLI user can trigger, so tests
// catch them by calling Service (or validateOps directly) for every real
// service table.
func Service[C any](e *env, name, short string, newClient func(vngcloud.Config) *C, ops ...Op[C]) *cobra.Command {
	if err := validateOps(name, ops); err != nil {
		panic(err)
	}

	cmd := &cobra.Command{
		Use:   name,
		Short: short,
		Args:  parentArgs,
		RunE:  unknownCommandRunE,
	}
	for _, op := range ops {
		cmd.AddCommand(newOpCmd(e, name, newClient, op))
	}
	return cmd
}

// validateOps checks every op in ops against the invariants Service
// enforces; see Service's doc comment.
func validateOps[C any](serviceName string, ops []Op[C]) error {
	for _, op := range ops {
		if err := checkOpName(op.methodName, op.name); err != nil {
			return newUsageError("service %q: %s", serviceName, err)
		}
		input := op.newInput()
		fieldNames, err := inputFieldNames(input)
		if err != nil {
			return newUsageError("service %q op %q: %s", serviceName, op.name, err)
		}
		for name := range op.noFlag {
			if !fieldNames[name] {
				return newUsageError("service %q op %q: NoFlag(%q) names no field of its Input",
					serviceName, op.name, name)
			}
		}
		for name := range op.optional {
			if !fieldNames[name] {
				return newUsageError("service %q op %q: Optional(%q) names no field of its Input",
					serviceName, op.name, name)
			}
		}
		specs, err := flagSpecsFor(input)
		if err != nil {
			return newUsageError("service %q op %q: %s", serviceName, op.name, err)
		}
		for _, spec := range withoutNoFlag(specs, op.noFlag) {
			if globalFlagNames[spec.flagName] {
				return newUsageError("service %q op %q: flag --%s collides with a global flag",
					serviceName, op.name, spec.flagName)
			}
		}
	}
	return nil
}

// newOpCmd builds the leaf command for one operation: its flags (from the
// Input struct), --cli-input-json, and the guarded call to the SDK method.
func newOpCmd[C any](e *env, serviceName string, newClient func(vngcloud.Config) *C, op Op[C]) *cobra.Command {
	input := op.newInput()
	specs, err := flagSpecsFor(input)
	if err != nil {
		panic(err)
	}
	specs = withoutNoFlag(specs, op.noFlag)

	cmd := &cobra.Command{
		Use:   op.name,
		Short: op.short,
		Args:  noArgs,
	}
	bound := registerFlags(cmd, specs)
	cmd.Flags().String("cli-input-json", "", "a JSON object ('<json>' or file://path) supplying Input fields by their Go name")
	if op.extraFlags != nil {
		op.extraFlags(cmd)
	}

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return runOp(cmd.Context(), e, cmd, serviceName, newClient, op, input, bound)
	}
	return cmd
}

// runOp builds the Input, enforces every guard, and, once every guard has
// passed, builds the Config and client and calls the operation.
func runOp[C any](ctx context.Context, e *env, cmd *cobra.Command, serviceName string, newClient func(vngcloud.Config) *C, op Op[C], input any, bound []boundFlag) error {
	rawJSON, err := cmd.Flags().GetString("cli-input-json")
	if err != nil {
		return usageError{msg: err.Error()}
	}
	if err := applyCLIInputJSON(rawJSON, input); err != nil {
		return err
	}
	applyChangedFlags(cmd, input, bound)
	if err := applyGlobalProjectID(e, op, input); err != nil {
		return err
	}

	// op.guard runs on the merged Input before the read-only check below: it
	// is a property of the Input's own shape (a literal --address on argv),
	// not of the profile, so it is refused the same way regardless of
	// read-only, and always before any request.
	if op.kind == kindWrite && op.guard != nil {
		if err := op.guard(cmd, input); err != nil {
			return err
		}
	}

	// A read-only refusal from the flag or environment source is checked
	// before checkRequiredFlags, not after: read-only rejects the whole
	// command outright, so an agent sees exactly one reason it was refused
	// rather than a required-flag error that a --yes or a fixed flag set
	// would not actually clear.
	if op.kind == kindWrite {
		if on, source, err := readOnlyPreConfig(e.flags); err != nil {
			return err
		} else if on {
			return readOnlyError{source: source}
		}
	}

	if err := checkRequiredFlags(input, op.noFlag, op.optional); err != nil {
		return err
	}
	// Compiled again in renderOutput once there is a result to run it
	// against; compiling here too means a bad --query is refused before any
	// request, including a destructive one.
	if _, err := compileQuery(e.flags.query); err != nil {
		return err
	}
	if e.flags.output != "" && !validOutputFormats[e.flags.output] {
		return newUsageError("--output must be json, table, or text, got %q", e.flags.output)
	}

	if op.kind == kindWrite {
		if op.destructive && !e.flags.yes {
			return newUsageError("%s %s is destructive; pass --yes to confirm", serviceName, op.name)
		}
	}

	logger := debugLogger(e)
	cfg, err := loadConfig(ctx, e, logger)
	if err != nil {
		return err
	}
	format := resolveOutput(e.flags, cfg)
	if !validOutputFormats[format] {
		return newUsageError("--output must be json, table, or text, got %q", format)
	}

	if op.kind == kindWrite {
		if on, source, err := readOnlyFromProfile(cfg, resolvedProfileName(e.flags)); err != nil {
			return err
		} else if on {
			return readOnlyError{source: source}
		}
		if logger != nil {
			logger.DebugContext(ctx, "write started", "operation", serviceName+" "+op.name)
		}
	}

	client := newClient(cfg)
	out, callErr := op.call(cmd, client, ctx, input)

	if op.kind == kindWrite && logger != nil {
		logger.DebugContext(ctx, "write finished", "operation", serviceName+" "+op.name)
	}
	if callErr != nil {
		// A vDNS, network, compute, containerregistry, iam, or tagging write
		// that reached the server still carries its Output: the resource's
		// id, needed to clean up or check again later. --query is skipped
		// here, unlike the success path below, so that id is never filtered
		// out by a query the caller wrote for the success shape. A render
		// failure is not reported over callErr, the call's own error,
		// which already carries the right error class and exit code.
		// containerregistry.ErrUserNotFound joins this same group:
		// create-user's own create succeeded, so its Output (the redacted
		// secret and, once written, SecretFile) must still print even
		// though the post-create list could not confirm the new user by
		// name. volume.ErrFailed and volume.ErrNotSettled join it too: a
		// vServer volume write's own wait can fail after the request already
		// reached the server, and its Output (the volume read before the
		// write) is the caller's only way to see what it was doing.
		// compute.ErrFailed joins compute.ErrNotSettled for the same reason
		// on the server side: create-server's, delete-server's, and every
		// server lifecycle write's own wait can report ERROR after the
		// request already reached the server, and its Output (the server
		// read before or during the wait) is the caller's only way to see
		// what it was doing.
		// tagging.ErrNotSettled joins it for the same reason as the
		// others: TagResource's or UntagResource's PUT already replaced the
		// resource's user tags, so the last tags a read returned are worth
		// printing even though the confirm read itself failed or mismatched.
		// loadbalancer.ErrFailed and loadbalancer.ErrNotSettled join it too:
		// create-load-balancer's and delete-load-balancer's own post-write
		// waits (and every child write's) still carry the last resource a
		// read returned.
		// storage.ErrNotSettled joins it: a delete-bucket the server accepted
		// but still showed when the wait ended returns no Output, and a nil
		// Output prints nothing rather than null.
		if op.kind == kindWrite && (errors.Is(callErr, dns.ErrFailed) || errors.Is(callErr, dns.ErrNotSettled) ||
			errors.Is(callErr, network.ErrFailed) || errors.Is(callErr, network.ErrNotSettled) ||
			errors.Is(callErr, compute.ErrFailed) || errors.Is(callErr, compute.ErrNotSettled) ||
			errors.Is(callErr, containerregistry.ErrNotSettled) ||
			errors.Is(callErr, containerregistry.ErrUserNotFound) || errors.Is(callErr, iam.ErrNotSettled) ||
			errors.Is(callErr, tagging.ErrNotSettled) ||
			errors.Is(callErr, volume.ErrFailed) || errors.Is(callErr, volume.ErrNotSettled) ||
			errors.Is(callErr, loadbalancer.ErrFailed) || errors.Is(callErr, loadbalancer.ErrNotSettled) ||
			errors.Is(callErr, storage.ErrNotSettled) || errors.Is(callErr, cdn.ErrNotSettled)) && !isNilOutput(out) {
			_ = renderOutput(e.stdout, format, "", out, true)
		}
		return callErr
	}
	if op.render != nil {
		return op.render(e.stdout, format, e.flags.query, out)
	}
	return renderOutput(e.stdout, format, e.flags.query, out, op.kind == kindWrite)
}

// isNilOutput reports whether out is nil or a nil pointer, which a write
// wrapper returns as a non-nil interface when the SDK gave no Output.
func isNilOutput(out any) bool {
	if out == nil {
		return true
	}
	v := reflect.ValueOf(out)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

// applyGlobalProjectID copies the global --project-id flag into the Input
// field op.globalProject names. The flag wins over --cli-input-json; without
// the flag, a value from --cli-input-json stays. An empty result is a usage
// error here, so the command stops before any request.
func applyGlobalProjectID[C any](e *env, op Op[C], input any) error {
	if op.globalProject == "" {
		return nil
	}
	field := reflect.ValueOf(input).Elem().FieldByName(op.globalProject)
	if e.flags.projectID != "" {
		field.SetString(e.flags.projectID)
	}
	if field.String() == "" {
		return newUsageError("--project-id is required")
	}
	return nil
}
