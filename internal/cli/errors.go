package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/cdn"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/containerregistry"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/iam"
	"danny.vn/vngcloud/loadbalancer"
	"danny.vn/vngcloud/monitor"
	"danny.vn/vngcloud/network"
	"danny.vn/vngcloud/volume"
)

// usageError marks a bad flag, argument, unknown command, or missing
// required input as the caller's mistake. It always exits 2 with JSON class
// InvalidUsage. Every command sets SilenceUsage, so cobra never prints its
// own usage text alongside the JSON error line.
type usageError struct{ msg string }

func newUsageError(format string, args ...any) usageError {
	return usageError{msg: fmt.Sprintf(format, args...)}
}

func (e usageError) Error() string { return e.msg }

// readOnlyError reports a write command refused because read-only is on.
// source names the setting that turned it on, for example "--read-only" or
// `read_only in profile "agent"`.
type readOnlyError struct{ source string }

func (e readOnlyError) Error() string {
	return "read-only: refused by " + e.source
}

// queryFailedError reports a --query that failed to run after the operation
// itself succeeded. writeSucceeded marks a write, so the message tells the
// caller not to retry it.
type queryFailedError struct {
	err            error
	writeSucceeded bool
}

func (e *queryFailedError) Error() string {
	if e.writeSucceeded {
		return fmt.Sprintf("query failed after the write succeeded: %s", e.err)
	}
	return fmt.Sprintf("query failed: %s", e.err)
}

func (e *queryFailedError) Unwrap() error { return e.err }

// errorEnvelope is the one JSON line the CLI prints to stderr for a failed
// command, per the CLI design's error shape.
type errorEnvelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Status    int    `json:"status,omitempty"`
	Operation string `json:"operation,omitempty"`
}

// classify turns err into the stderr JSON shape. NotFound is checked before
// the generic *APIError branch below, and always wins over whatever Code an
// embedded *APIError itself carries: vngcloud.IsNotFound(err) is true both
// for a real 404 and for a non-404 error such as monitor.DeleteChannel's
// that a service maps to core.ErrNotFound by its message rather than its
// status, and both must classify the same way rather than let the second
// kind fall through with the underlying status's own Code (a 400 channel
// delete's "BadRequest", say) instead. For an *vngcloud.APIError that is not
// a not-found, code is APIError.Code (already resolved to the
// status-derived fallback by the SDK), or "RequestFailed" when the error
// carries neither a code nor an HTTP status. Every other error is named by
// one of the CLI's own classes: InvalidUsage, ReadOnly, InvalidConfig,
// NoCredentials, LoginFailed, RequestFailed, QueryFailed, PageFormat (a
// public page, such as the CDN IP range FAQ, no longer matches the shape
// its parser expects), UnexpectedStatus (a vMonitor check had a status
// PauseCheck or ResumeCheck does not recognize, or a network
// enable-vpc-private-dns read a VPC dnsStatus it does not know how to act
// on), StatusUnconfirmed (a
// vMonitor pause or resume may have landed but no confirm read showed it),
// ZoneBusy (a vDNS zone stayed busy past the pre-write wait, so nothing was
// sent), WriteFailed (a vDNS write reached status ERROR, a network
// security group create's post-create wait saw the group reach ERROR, or
// a vServer volume write's post-write wait, such as create-volume's or
// delete-volume's, saw the volume reach ERROR),
// NotSettled (a vDNS write was accepted but did not settle within the
// post-write wait, or a network create-security-group's or
// update-security-group's wait ran out of time: a create must not be sent
// again, since a repeat risks a second group, but an update may be sent
// again, since its PUT always resends the whole resolved group rather than
// making a new one, or a compute update-server-group's confirm read after a
// successful PUT failed to come back, whose update may be sent again the
// same way, or a containerregistry create-repository's or
// delete-repository's wait ran out of time: a create must not be sent
// again, since the repository exists, but a delete already reads first, so
// a rerun is safe, or an iam create-policy or update-policy whose write
// reached the server but its own confirm read failed: create-policy must
// not be sent again, since a repeat risks a second policy, but
// update-policy may be sent again the same way, or a volume create-volume
// or delete-volume whose wait ran out of time or otherwise failed to read
// back: create-volume must not be sent again, since the volume exists, but
// delete-volume already reads first and is safe to run again),
// VolumeInUse (a volume delete-volume was refused because a pre-delete
// read showed the volume attached to a server, before any request),
// RepositoryNotEmpty (a
// containerregistry delete-repository was refused because a pre-delete
// read showed the repository still holds images), UserNotFound (a
// containerregistry create-user's own create succeeded but a follow-up
// list could not confirm the new user by name; the new secret is still
// written to --secret-file either way), OTPRejected (a channel OTP
// create-channel or update-channel
// sent to SendChannelOTP's Validate OTP step was wrong or expired, so no
// create or update was sent), PriceAboveMax (a paid write's own quote priced
// the order above --max-price, so nothing was sent; create-log-project and
// volume create-volume both reach this),
// SelfChange (an iam write refused because its target is the caller
// itself, before any request), PrivilegedChange (an iam write refused
// because its target holds, or would gain, an IAM write right, before any
// request), ManagedPolicy (an iam update-policy or delete-policy targeted a
// GreenNode-managed policy, before any request), SystemSecurityGroup (a
// network update-security-group or
// delete-security-group targeted a project's system group, so nothing was
// sent), SecurityGroupInUse (a network delete-security-group was refused
// because the group has servers attached, found by a pre-delete read, or
// because the server's own refusal named the group in use for some other
// reason), ServerGroupInUse (a compute delete-server-group was refused
// because the group has servers attached, found by a pre-delete list scan,
// or because the server's own refusal named the group in use for some other
// reason), ResourceInUse (a network VPC, subnet, or route table write
// was refused because a pre-write read showed it still in use, such as a
// VPC with subnets, a subnet with servers, or a route table a subnet still
// names, or because the server's own refusal named it in use, including a
// VPC delete the server keeps refusing with "contains the subnet" for
// several minutes after that subnet's own delete, or a loadbalancer
// delete-certificate refused because a pre-delete read showed the
// certificate still in use by a listener, or because the server's own
// refusal named it in use; or an iam delete-policy targeted a policy still
// attached to a group, an IAM user, or a service account, before any
// request), DefaultResource (a network delete-route-table
// targeted a VPC's main route table while a subnet names no route table of
// its own and so relies on it; the server itself deletes a main table once
// no subnet relies on it), ResourceBusy (a network add-route or
// remove-route read a route table that was not ACTIVE and stayed that way
// past the wait before the write), or SecretFileFailed
// (create-ssh-key's own create succeeded but writing --secret-file failed
// afterward, so the CLI deleted the new key).
func classify(err error) errorEnvelope {
	// Checked before errors.As(err, &apiErr) below: the real
	// ErrStatusUnconfirmed error also wraps the toggle PUT's own *APIError
	// (see monitor's design), and errors.As would otherwise find that inner
	// APIError first and report its status and code instead of
	// StatusUnconfirmed.
	if errors.Is(err, monitor.ErrStatusUnconfirmed) {
		return errorEnvelope{Code: "StatusUnconfirmed", Message: err.Error()}
	}
	if errors.Is(err, monitor.ErrUnexpectedStatus) || errors.Is(err, network.ErrUnexpectedStatus) {
		return errorEnvelope{Code: "UnexpectedStatus", Message: err.Error()}
	}
	// monitor.ErrOTPRejected, like dns.ErrZoneBusy, dns.ErrFailed,
	// dns.ErrNotSettled, and monitor.ErrPriceAboveMax below, is always
	// wrapped alone (never alongside an *APIError): CreateChannel and
	// UpdateChannel return it directly, after Validate OTP's own APIError
	// path (if any) already returned. Checking it before errors.As(err,
	// &apiErr) below is only for grouping every early, non-APIError class
	// together.
	if errors.Is(err, monitor.ErrOTPRejected) {
		return errorEnvelope{Code: "OTPRejected", Message: err.Error()}
	}
	if errors.Is(err, dns.ErrZoneBusy) {
		return errorEnvelope{Code: "ZoneBusy", Message: err.Error()}
	}
	// volume.ErrFailed joins dns.ErrFailed and network.ErrFailed here: a
	// vServer paid write's own post-write wait (create, delete, resize,
	// attach, or detach) reaching ERROR reports the same WriteFailed class.
	if errors.Is(err, dns.ErrFailed) || errors.Is(err, network.ErrFailed) || errors.Is(err, volume.ErrFailed) {
		return errorEnvelope{Code: "WriteFailed", Message: err.Error()}
	}
	// compute.ErrNotSettled, containerregistry.ErrNotSettled, and
	// iam.ErrNotSettled join dns.ErrNotSettled and network.ErrNotSettled
	// here for the same reason they all do: UpdateServerGroup's confirm
	// read, GetRepository's own 5xx-confirm path inside the
	// containerregistry wait, and CreatePolicy's and UpdatePolicy's own
	// confirm GetPolicy read, can each wrap an inner *core.APIError or a
	// canceled context, and this check must win over the generic *APIError
	// branch below. volume.ErrNotSettled joins them for the same vServer
	// paid write wait bound reason ErrFailed does above.
	if errors.Is(err, dns.ErrNotSettled) || errors.Is(err, network.ErrNotSettled) || errors.Is(err, compute.ErrNotSettled) ||
		errors.Is(err, containerregistry.ErrNotSettled) || errors.Is(err, iam.ErrNotSettled) || errors.Is(err, volume.ErrNotSettled) {
		return errorEnvelope{Code: "NotSettled", Message: err.Error()}
	}
	// volume.ErrVolumeInUse is always returned bare, from DeleteVolume's own
	// pre-delete read, never wrapping a server response; it joins this early
	// group anyway for the same reason containerregistry.ErrRepositoryNotEmpty
	// does just below: consistent placement ahead of the generic *APIError
	// branch.
	if errors.Is(err, volume.ErrVolumeInUse) {
		return errorEnvelope{Code: "VolumeInUse", Message: err.Error()}
	}
	// containerregistry.ErrRepositoryNotEmpty is always returned bare, from
	// delete-repository's own pre-delete image count check, never wrapping a
	// server response; it is still checked this early, alongside the other
	// pre-write and pre-delete guards below, for the same grouping reason.
	if errors.Is(err, containerregistry.ErrRepositoryNotEmpty) {
		return errorEnvelope{Code: "RepositoryNotEmpty", Message: err.Error()}
	}
	// containerregistry.ErrUserNotFound joins this same early group for the
	// same reason ErrNotSettled above does: findCreatedUser's own lookup
	// failure (containerregistry/users_write.go) can wrap an inner
	// *core.APIError when the confirm list itself failed, and this check
	// must win over the generic *APIError branch below.
	if errors.Is(err, containerregistry.ErrUserNotFound) {
		return errorEnvelope{Code: "UserNotFound", Message: err.Error()}
	}
	// network.ErrSystemGroup, network.ErrSecurityGroupInUse, and
	// network.ErrInUse join this same early group for the same reason
	// monitor.ErrOTPRejected's comment above gives: ErrSecurityGroupInUse and
	// ErrInUse can each wrap an inner *core.APIError (see
	// wrapSecurityGroupInUse in network/security_groups_write.go and
	// wrapVPCContainsSubnet in network/vpcs_write.go), and this check must win
	// over the generic *APIError branch below rather than let errors.As find
	// that inner error first and report its own status-derived code instead.
	// compute.ErrServerGroupInUse joins it for the same reason:
	// wrapServerGroupInUse (compute/server_groups_write.go) can wrap the
	// server's own refusal the same way. loadbalancer.ErrCertificateInUse
	// joins it too: wrapCertificateDeleteErr
	// (loadbalancer/certificates_write.go) can wrap the server's own refusal
	// of a delete the same way, on top of the plain sentinel
	// DeleteCertificate itself returns from its own pre-delete read.
	if errors.Is(err, network.ErrSystemGroup) {
		return errorEnvelope{Code: "SystemSecurityGroup", Message: err.Error()}
	}
	if errors.Is(err, network.ErrSecurityGroupInUse) {
		return errorEnvelope{Code: "SecurityGroupInUse", Message: err.Error()}
	}
	if errors.Is(err, compute.ErrServerGroupInUse) {
		return errorEnvelope{Code: "ServerGroupInUse", Message: err.Error()}
	}
	if errors.Is(err, network.ErrInUse) || errors.Is(err, loadbalancer.ErrCertificateInUse) {
		return errorEnvelope{Code: "ResourceInUse", Message: err.Error()}
	}
	// network.ErrDefaultResource and network.ErrBusy join this same early
	// group too: both come from DeleteRouteTable's own pre-delete reads or
	// from AddRoute's and RemoveRoute's pre-write wait, never from wrapping
	// the server's own response, so checking them here costs nothing extra
	// today, but keeps every network sentinel error classified in the same
	// place ahead of the generic *APIError branch below.
	if errors.Is(err, network.ErrDefaultResource) {
		return errorEnvelope{Code: "DefaultResource", Message: err.Error()}
	}
	if errors.Is(err, network.ErrBusy) {
		return errorEnvelope{Code: "ResourceBusy", Message: err.Error()}
	}
	// vngcloud.ErrPriceAboveMax is the root sentinel a compute or volume paid
	// write's own quote guard wraps; monitor.ErrPriceAboveMax is the same
	// value (see the root package), so checking the root name alone still
	// classifies monitor's create-log-project refusal the same way.
	if errors.Is(err, vngcloud.ErrPriceAboveMax) {
		return errorEnvelope{Code: "PriceAboveMax", Message: err.Error()}
	}
	// iam.ErrSelfChange and iam.ErrPrivilegedChange are always returned bare,
	// never wrapping an inner *APIError: the guard in iam/guard.go refuses a
	// write before any request ever reaches the server. They still join this
	// early group, ahead of the generic *APIError branch below, for the same
	// reason network.ErrInUse and the others above do: consistent placement
	// for every sentinel this file classifies by errors.Is rather than by an
	// *APIError's own status-derived code.
	if errors.Is(err, iam.ErrSelfChange) {
		return errorEnvelope{Code: "SelfChange", Message: err.Error()}
	}
	if errors.Is(err, iam.ErrPrivilegedChange) {
		return errorEnvelope{Code: "PrivilegedChange", Message: err.Error()}
	}
	// iam.ErrManagedPolicy and iam.ErrInUse join the same early group, for the
	// same reason: update-policy, delete-policy, and the guard.go sentinels
	// above are always returned bare, never wrapping an inner *APIError, so
	// placement relative to the generic *APIError branch below does not
	// matter for correctness, but consistent placement keeps every iam
	// sentinel in one place. ErrInUse reuses network.ErrInUse's own
	// ResourceInUse code, since both mean the same thing: a delete was
	// refused because something else still depends on the target.
	if errors.Is(err, iam.ErrManagedPolicy) {
		return errorEnvelope{Code: "ManagedPolicy", Message: err.Error()}
	}
	if errors.Is(err, iam.ErrInUse) {
		return errorEnvelope{Code: "ResourceInUse", Message: err.Error()}
	}

	if vngcloud.IsNotFound(err) {
		env := errorEnvelope{Code: "NotFound", Message: err.Error()}
		var apiErr *vngcloud.APIError
		if errors.As(err, &apiErr) {
			fillEnvelopeFromAPIError(&env, apiErr)
		}
		return env
	}

	var apiErr *vngcloud.APIError
	if errors.As(err, &apiErr) {
		env := errorEnvelope{Code: apiErr.Code}
		fillEnvelopeFromAPIError(&env, apiErr)
		if env.Code == "" {
			env.Code = "RequestFailed"
		}
		return env
	}

	var usageErr usageError
	if errors.As(err, &usageErr) {
		return errorEnvelope{Code: "InvalidUsage", Message: err.Error()}
	}
	var roErr readOnlyError
	if errors.As(err, &roErr) {
		return errorEnvelope{Code: "ReadOnly", Message: err.Error()}
	}
	var sfErr secretFileFailedError
	if errors.As(err, &sfErr) {
		return errorEnvelope{Code: "SecretFileFailed", Message: err.Error()}
	}
	var queryErr *queryFailedError
	if errors.As(err, &queryErr) {
		return errorEnvelope{Code: "QueryFailed", Message: err.Error()}
	}
	if errors.Is(err, vngcloud.ErrNoCredentials) || errors.Is(err, vngcloud.ErrCredentialsFile) {
		return errorEnvelope{Code: "NoCredentials", Message: err.Error()}
	}
	var loginErr *vngcloud.LoginError
	if errors.As(err, &loginErr) {
		return errorEnvelope{Code: "LoginFailed", Message: err.Error()}
	}
	if errors.Is(err, vngcloud.ErrInvalidInput) {
		return errorEnvelope{Code: "InvalidUsage", Message: err.Error()}
	}
	if errors.Is(err, vngcloud.ErrInvalidConfig) {
		return errorEnvelope{Code: "InvalidConfig", Message: err.Error()}
	}
	if errors.Is(err, cdn.ErrPageFormat) {
		return errorEnvelope{Code: "PageFormat", Message: err.Error()}
	}
	return errorEnvelope{Code: "RequestFailed", Message: err.Error()}
}

// fillEnvelopeFromAPIError copies apiErr's own Message (or, if empty, its
// full Error() text, which repeats the operation and status Message alone
// would lack), Operation, and StatusCode into env. Both of classify's
// *APIError-aware branches call this, so a wrapped *APIError's detail
// reaches the envelope the same way whether or not the NotFound branch also
// forces Code to "NotFound" ahead of it.
func fillEnvelopeFromAPIError(env *errorEnvelope, apiErr *vngcloud.APIError) {
	message := apiErr.Message
	if message == "" {
		message = apiErr.Error()
	}
	env.Message = message
	env.Operation = apiErr.Operation
	if apiErr.StatusCode > 0 {
		env.Status = apiErr.StatusCode
	}
}

// exitCode maps err to the CLI's process exit code, checked in this fixed
// order: a canceled or expired context anywhere in the chain always exits 1,
// even for a *LoginError or an *APIError that wraps it, because the command
// was interrupted rather than genuinely rejected. Missing or refused
// credentials and every login failure exit 3. A not-found result exits 4.
// Every remaining usage or config problem exits 2. Anything else exits 1.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	// Checked before the canceled-context rule below: a Ctrl-C during the
	// toggle PUT or a confirm read still reports the same exit code (1) as
	// every other unconfirmed toggle, per monitor's design, rather than
	// happening to match the canceled-context rule by coincidence. dns.ErrZoneBusy,
	// dns.ErrFailed, dns.ErrNotSettled, network.ErrFailed, network.ErrNotSettled,
	// network.ErrUnexpectedStatus, compute.ErrNotSettled,
	// containerregistry.ErrNotSettled, containerregistry.ErrUserNotFound,
	// iam.ErrNotSettled, and monitor.ErrOTPRejected join the same early
	// return for the same reason: per the vDNS, network, vCR writes, and
	// iam designs, a not-settled write, and a create-user whose own create
	// already succeeded, must exit the same way even after a canceled
	// context, because the write already landed, and the others join it
	// for consistency.
	if errors.Is(err, monitor.ErrStatusUnconfirmed) || errors.Is(err, monitor.ErrUnexpectedStatus) ||
		errors.Is(err, network.ErrUnexpectedStatus) ||
		errors.Is(err, dns.ErrZoneBusy) || errors.Is(err, dns.ErrFailed) || errors.Is(err, dns.ErrNotSettled) ||
		errors.Is(err, network.ErrFailed) || errors.Is(err, network.ErrNotSettled) ||
		errors.Is(err, compute.ErrNotSettled) || errors.Is(err, containerregistry.ErrNotSettled) ||
		errors.Is(err, containerregistry.ErrUserNotFound) || errors.Is(err, iam.ErrNotSettled) ||
		errors.Is(err, monitor.ErrOTPRejected) ||
		errors.Is(err, volume.ErrFailed) || errors.Is(err, volume.ErrNotSettled) || errors.Is(err, volume.ErrVolumeInUse) {
		return 1
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 1
	}
	if errors.Is(err, vngcloud.ErrNoCredentials) || errors.Is(err, vngcloud.ErrCredentialsFile) {
		return 3
	}
	var loginErr *vngcloud.LoginError
	if errors.As(err, &loginErr) {
		return 3
	}
	if errors.Is(err, vngcloud.ErrAuth) {
		return 3
	}
	if vngcloud.IsNotFound(err) {
		return 4
	}
	// A runtime --query failure (queryFailedError) is deliberately not listed
	// here: it means the operation itself already succeeded, so it falls
	// through to the plain exit 1 below rather than the usage-error exit 2,
	// per the CLI design.
	var usageErr usageError
	var roErr readOnlyError
	switch {
	case errors.As(err, &usageErr),
		errors.As(err, &roErr),
		errors.Is(err, vngcloud.ErrInvalidInput),
		errors.Is(err, vngcloud.ErrInvalidConfig),
		errors.Is(err, vngcloud.ErrProjectAmbiguous):
		return 2
	}
	// vngcloud.ErrPriceAboveMax (and monitor.ErrPriceAboveMax, the same
	// value) also exits 1 here, through this default: a paid write's price
	// guard returns it directly, before any request, never wrapped alongside
	// a canceled context the way dns.ErrNotSettled can be, so it needs no
	// earlier special-case check.
	return 1
}

// printError writes err to w as one JSON line, per the CLI design's error
// shape. A JSON encoding failure (never expected, since errorEnvelope holds
// only strings and an int) falls back to a plain-text line so a command
// never exits silently.
func printError(w io.Writer, err error) {
	env := classify(err)
	data, encErr := json.Marshal(struct {
		Error errorEnvelope `json:"error"`
	}{Error: env})
	// A write failure here has nowhere left to report to (this is already the
	// error-output path), so every error return below is deliberately
	// ignored.
	if encErr != nil {
		_, _ = fmt.Fprintf(w, "{\"error\":{\"code\":%q,\"message\":%q}}\n", "RequestFailed", err.Error())
		return
	}
	_, _ = w.Write(data)
	_, _ = fmt.Fprintln(w)
}
