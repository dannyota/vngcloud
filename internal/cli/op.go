package cli

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/dns"
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
	methodName  string
	kind        opKind
	destructive bool
	noFlag      map[string]bool
	newInput    func() any
	newOutput   func() any
	call        func(client *C, ctx context.Context, in any) (any, error)
}

// writeOption configures a Write operation. Destructive is the only one
// today; cli.WaitFor (for an asynchronous write) is undefined until the
// first such write needs it.
type writeOption struct{ destructive bool }

// Destructive marks a Write operation as not undoable by one more command:
// it fails with exit code 2 and names --yes unless --yes is given.
func Destructive() writeOption { return writeOption{destructive: true} }

// readOption configures a Read operation: NoFlag, Redact, or both.
type readOption struct {
	noFlag map[string]bool
	redact any // func(*Out) for the Read's own Out, checked in Read
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

// Read registers a read operation: name is its kebab-case command name, and
// method is an SDK method expression such as (*compute.Client).ListServers.
// opts holds NoFlag and Redact where an operation needs them. A Redact
// whose function does not take this Read's Output panics at registration.
func Read[C, In, Out any](name string, method func(*C, context.Context, *In) (*Out, error), opts ...readOption) Op[C] {
	var redact func(*Out)
	noFlag := map[string]bool{}
	for _, o := range opts {
		for f := range o.noFlag {
			noFlag[f] = true
		}
		if o.redact != nil {
			fn, ok := o.redact.(func(*Out))
			if !ok {
				panic("cli: Redact function does not match the Output of " + name)
			}
			redact = fn
		}
	}
	return Op[C]{
		name:       name,
		methodName: funcName(method),
		kind:       kindRead,
		noFlag:     noFlag,
		newInput:   func() any { return new(In) },
		newOutput:  func() any { return new(Out) },
		call: func(client *C, ctx context.Context, in any) (any, error) {
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
	op := Op[C]{
		name:       name,
		methodName: funcName(method),
		kind:       kindWrite,
		newInput:   func() any { return new(In) },
		newOutput:  func() any { return new(Out) },
		call: func(client *C, ctx context.Context, in any) (any, error) {
			return method(client, ctx, in.(*In))
		},
	}
	for _, o := range opts {
		if o.destructive {
			op.destructive = true
		}
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

// validateOps checks every op in ops against the two invariants Service
// enforces; see Service's doc comment.
func validateOps[C any](serviceName string, ops []Op[C]) error {
	for _, op := range ops {
		if err := checkOpName(op.methodName, op.name); err != nil {
			return newUsageError("service %q: %s", serviceName, err)
		}
		specs, err := flagSpecsFor(op.newInput())
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
		Use:  op.name,
		Args: noArgs,
	}
	bound := registerFlags(cmd, specs)
	cmd.Flags().String("cli-input-json", "", "a JSON object ('<json>' or file://path) supplying Input fields by their Go name")

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

	if err := checkRequiredFlags(input); err != nil {
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
	out, callErr := op.call(client, ctx, input)

	if op.kind == kindWrite && logger != nil {
		logger.DebugContext(ctx, "write finished", "operation", serviceName+" "+op.name)
	}
	if callErr != nil {
		// A vDNS write that reached the server still carries its Output: the
		// zone's id, needed to clean up or check again later. --query is
		// skipped here, unlike the success path below, so that id is never
		// filtered out by a query the caller wrote for the success shape. A
		// render failure is not reported over callErr, the call's own error,
		// which already carries the right error class and exit code.
		if op.kind == kindWrite && (errors.Is(callErr, dns.ErrFailed) || errors.Is(callErr, dns.ErrNotSettled)) {
			_ = renderOutput(e.stdout, format, "", out, true)
		}
		return callErr
	}
	return renderOutput(e.stdout, format, e.flags.query, out, op.kind == kindWrite)
}
