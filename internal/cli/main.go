// Package cli implements the vngcloud command: a cobra root command built
// fresh per invocation, one operation table per SDK service, output
// formatting, and configure and gen-docs. It calls GreenNode only through
// the public danny.vn/vngcloud package.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/monitor"
)

// Main builds a fresh command tree from args, runs it, and returns the
// process exit code. Results go to stdout; a failed command's error and any
// --debug logging go to stderr. The caller owns calling os.Exit.
func Main(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	state := new(syntheticCLIOutcome)
	ctx = context.WithValue(ctx, syntheticOutcomeContextKey{}, state)
	root := newRootCmd(stdin, stdout, stderr)
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(ctx)
	if op := syntheticCommandOperation(cmd); op != "" && err != nil {
		finishSyntheticCLI(stdout, stderr, cmd, op, state, err)
		return exitCode(err)
	}
	if err != nil {
		printError(stderr, err)
	}
	return exitCode(err)
}

// This state belongs to one invocation, including failures before SDK entry.
type syntheticOutcomeContextKey struct{}
type syntheticCLIOutcome struct {
	output   any
	format   string
	rendered bool // Any stdout write attempt forbids fallback, even on error.
}

func syntheticMutationOperation(service, command string) string {
	if service != "monitor" {
		return ""
	}
	switch command {
	case "create-synthetic-quota":
		return "monitor.CreateSyntheticQuota"
	case "delete-synthetic-quota":
		return "monitor.DeleteSyntheticQuota"
	case "recover-synthetic-quota":
		return "monitor.RecoverSyntheticQuota"
	case "renew-synthetic-quota":
		return "monitor.RenewSyntheticQuota"
	default:
		return ""
	}
}
func syntheticCommandOperation(cmd *cobra.Command) string {
	if cmd == nil || cmd.Parent() == nil {
		return ""
	}
	return syntheticMutationOperation(cmd.Parent().Name(), cmd.Name())
}
func syntheticOutcomeOf(out any) *monitor.QuotaMutationOutcome {
	switch value := out.(type) {
	case *monitor.CreateSyntheticQuotaOutput:
		return &value.QuotaMutationOutcome
	case *monitor.RecoverSyntheticQuotaOutput:
		return &value.QuotaMutationOutcome
	case *monitor.RenewSyntheticQuotaOutput:
		return &value.QuotaMutationOutcome
	default:
		return nil
	}
}
func finishSyntheticCLI(stdout, stderr io.Writer, cmd *cobra.Command, op string, state *syntheticCLIOutcome, err error) {
	if op == "monitor.DeleteSyntheticQuota" {
		if !state.rendered {
			format := state.format
			if !validOutputFormats[format] {
				format, _ = cmd.Flags().GetString("output")
			}
			if !validOutputFormats[format] {
				format = outputJSON
			}
			out, _ := state.output.(*monitor.DeleteSyntheticQuotaOutput)
			if out == nil {
				out = new(monitor.DeleteSyntheticQuotaOutput)
			}
			safe := *out
			safe.Quota = nil
			if safe.Stage == "sent" || safe.Stage == "settled" {
				safe.Stage = "sent"
				err = fmt.Errorf("quota output unavailable; reconcile with reads: %w: %w", dns.ErrNotSettled, err)
			}
			safe.Purged = false
			_ = renderSyntheticCommand(stdout, format, &safe)
		}
		printError(stderr, err)
		return
	}
	outcome := syntheticOutcomeOf(state.output)
	if outcome == nil {
		env := classify(err)
		outcome = &monitor.QuotaMutationOutcome{Sent: "no", Stage: "preflight", Error: &monitor.QuotaOutcomeError{Code: env.Code, Operation: op, Status: env.Status}}
		switch op {
		case "monitor.CreateSyntheticQuota":
			state.output = &monitor.CreateSyntheticQuotaOutput{QuotaMutationOutcome: *outcome}
		case "monitor.RecoverSyntheticQuota":
			state.output = &monitor.RecoverSyntheticQuotaOutput{QuotaMutationOutcome: *outcome}
		default:
			state.output = &monitor.RenewSyntheticQuotaOutput{QuotaMutationOutcome: *outcome}
		}
	}
	if !state.rendered {
		format := state.format
		if !validOutputFormats[format] {
			format, _ = cmd.Flags().GetString("output")
		}
		if !validOutputFormats[format] {
			format = outputJSON
		}
		state.output = syntheticFallbackOutput(state.output, op)
		outcome = syntheticOutcomeOf(state.output)
		_ = renderOutput(stdout, format, "", state.output, true)
	}
	meta := outcome.Error
	if meta == nil {
		meta = &monitor.QuotaOutcomeError{Code: "RequestFailed", Operation: op}
	}
	// The safe fixed envelope also covers invalid argv, config, and rendering.
	envelope := struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Operation string `json:"operation"`
			Status    int    `json:"status"`
		} `json:"error"`
	}{}
	envelope.Error.Code = meta.Code
	envelope.Error.Message = "quota command failed; inspect the outcome and reconcile with reads before another write"
	envelope.Error.Operation = op
	envelope.Error.Status = meta.Status
	_ = json.NewEncoder(stderr).Encode(envelope)
}
func monitorSyntheticMutationOp(op Op[monitor.Client]) Op[monitor.Client] {
	op.short = "Send one free Synthetic quota mutation; explicit identity and --max-price consent required; pay: true may debit the balance"
	op.guard = func(cmd *cobra.Command, in any) error {
		query, _ := cmd.Flags().GetString("query")
		if query != "" || cmd.Flags().Changed("query") {
			return newUsageError("--query is unavailable for quota mutation outcomes")
		}
		raw, _ := cmd.Flags().GetString("cli-input-json")
		values, err := cliInputJSONBytes(raw)
		if err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if len(values) > 0 {
			if err := json.Unmarshal(values, &fields); err != nil {
				return newUsageError("invalid JSON input")
			}
		}
		explicit := cmd.Flags().Changed("max-price")
		if value, ok := fields["MaxPrice"]; ok && string(value) != "null" {
			explicit = true
		}
		if !explicit {
			return newUsageError("explicit --max-price or JSON MaxPrice is required, including zero")
		}
		price := 0.0
		switch value := in.(type) {
		case *monitor.CreateSyntheticQuotaInput:
			price = value.MaxPrice
		case *monitor.RecoverSyntheticQuotaInput:
			price = value.MaxPrice
		case *monitor.RenewSyntheticQuotaInput:
			price = value.MaxPrice
		}
		if price > 0 && !math.IsInf(price, 0) && !math.IsNaN(price) {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Warning: pay: true may debit the balance; MaxPrice is a local guard, not a server cap.")
		}
		return nil
	}
	return op
}

// An encoding error permits fallback; a failed or partial Write does not.
type syntheticOutputWriter struct {
	io.Writer
	attempted bool
}

func (w *syntheticOutputWriter) Write(data []byte) (int, error) {
	w.attempted = true
	return w.Writer.Write(data)
}

func syntheticFallbackOutput(out any, op string) any {
	outcome := *syntheticOutcomeOf(out)
	if outcome.Sent != "no" {
		status := 0
		if outcome.Error != nil {
			status = outcome.Error.Status
		}
		outcome.Stage = "unconfirmed"
		outcome.Error = &monitor.QuotaOutcomeError{Code: "NotSettled", Operation: op, Status: status}
	} else if outcome.Error == nil {
		outcome.Stage = "preflight"
		outcome.Error = &monitor.QuotaOutcomeError{Code: "RequestFailed", Operation: op}
	}
	switch value := out.(type) {
	case *monitor.CreateSyntheticQuotaOutput:
		safe := *value
		safe.QuotaMutationOutcome = outcome
		safe.Quota = nil
		safe.Created = false
		if safe.SentAt != nil {
			if _, err := safe.SentAt.MarshalJSON(); err != nil {
				safe.SentAt = nil
			}
		}
		return &safe
	case *monitor.RecoverSyntheticQuotaOutput:
		safe := *value
		safe.QuotaMutationOutcome = outcome
		safe.Quota = nil
		safe.Recovered = false
		if safe.PreviousEndTime != nil {
			if _, err := safe.PreviousEndTime.MarshalJSON(); err != nil {
				safe.PreviousEndTime = nil
			}
		}
		return &safe
	case *monitor.RenewSyntheticQuotaOutput:
		safe := *value
		safe.QuotaMutationOutcome = outcome
		safe.Quota = nil
		safe.Renewed = false
		if safe.PreviousEndTime != nil {
			if _, err := safe.PreviousEndTime.MarshalJSON(); err != nil {
				safe.PreviousEndTime = nil
			}
		}
		return &safe
	default:
		return out
	}
}

const syntheticDeleteEffect = "Deleting this quota permanently deletes every synthetic check."

func monitorSyntheticDeleteOp() Op[monitor.Client] {
	op := Write[monitor.Client, monitor.DeleteSyntheticQuotaInput, monitor.DeleteSyntheticQuotaOutput]("delete-synthetic-quota", (*monitor.Client).DeleteSyntheticQuota, Destructive())
	op.short = syntheticDeleteEffect + " Requires --yes and --expected-quota-id"
	op.guard = func(cmd *cobra.Command, _ any) error {
		if cmd.Flags().Changed("query") {
			return newUsageError("--query is unavailable for synthetic quota deletion effects")
		}
		return nil
	}
	return op
}
func renderSyntheticCommand(w io.Writer, format string, out any) error {
	if value, ok := out.(*monitor.DeleteSyntheticQuotaOutput); ok {
		if value.Quota != nil {
			q := value.Quota
			for _, stamp := range []time.Time{q.StartTime, q.EndTime, q.CreatedAt, q.UpdatedAt} {
				if _, err := stamp.MarshalJSON(); err != nil {
					return err
				}
			}
		}
		return renderOutput(w, format, "", struct {
			Quota  *monitorQuotaDisplay
			Stage  string
			Purged bool
			Effect string
		}{monitorLifecycleQuotaDisplay(value.Quota), value.Stage, value.Purged, syntheticDeleteEffect}, true)
	}
	return renderOutput(w, format, "", out, true)
}
func renderSyntheticClassPackages(w io.Writer, format string, out *monitor.ListSyntheticPackagesOutput) error {
	if _, err := fmt.Fprintln(w, "Class unit packages (monthly VND per unit)"); err != nil {
		return err
	}
	rows := []any{}
	for _, class := range out.Items {
		for _, p := range class.Packages {
			rows = append(rows, map[string]any{"Class": class.Name, "ClassID": class.ID, "Status": class.Status, "ID": p.ID, "Name": p.Name, "API": p.API, "Location": p.Location, "TierID": p.TierID, "TierName": p.TierName, "UnitPriceVND": p.Price})
		}
	}
	return monitorCoverageQuery(w, format, "", rows)
}
