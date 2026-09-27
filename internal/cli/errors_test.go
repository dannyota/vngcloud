package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/containerregistry"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/loadbalancer"
	"danny.vn/vngcloud/monitor"
	"danny.vn/vngcloud/network"
)

// realStatusUnconfirmedErr drives one real monitor.PauseCheck call to the
// error TestExitCode and TestClassify assert on below, instead of only a
// synthetic fmt.Errorf: the pre-read returns StatusEnabled, and the toggle
// PUT's own fixture handler cancels the call's context, mirroring the
// monitor package's own canceled-context toggle test.
//
// The monitor package exposes no way to inject a fake clock from outside
// it (Client.sleep is unexported), so a 502-then-lagging-reads case would
// need the confirm reads' real 1, 2, and 4 second waits to elapse. Canceling
// the context during the PUT avoids that: contextSleep sees the context
// already done and returns at once, so every confirm-read wait ends
// immediately and this case stays fast.
func realStatusUnconfirmedErr(t *testing.T) error {
	t.Helper()
	withCleanEnv(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/vmonitor-uptime-manager/v1/uptimes/chk-1": jsonHandler(http.StatusOK, monitorUptimesJSON("chk-1", monitor.StatusEnabled)),
		"/vmonitor-uptime-manager/v1/uptimes/status/chk-1": func(_ http.ResponseWriter, _ *http.Request) {
			cancel()
			// The client's context is now canceled; whatever this handler
			// writes next may never reach it, so nothing more is written.
		},
	})
	opts := newFakeServer(t, fixture.mux)
	opts = append(opts, vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("test-token"))
	cfg, err := vngcloud.LoadConfig(ctx, opts...)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	_, err = monitor.New(cfg).PauseCheck(ctx, &monitor.PauseCheckInput{CheckID: "chk-1"})
	if err == nil {
		t.Fatal("PauseCheck: expected an error")
	}
	return err
}

func TestExitCode(t *testing.T) {
	realUnconfirmed := realStatusUnconfirmedErr(t)
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"context canceled bare", context.Canceled, 1},
		{"context deadline exceeded bare", context.DeadlineExceeded, 1},
		{
			"context canceled wrapped in APIError",
			&vngcloud.APIError{Operation: "compute.ListServers", Err: context.Canceled},
			1,
		},
		{
			"context canceled wrapped in LoginError",
			&vngcloud.LoginError{Reason: "sign-in request failed", Err: context.Canceled},
			1,
		},
		{"no credentials", vngcloud.ErrNoCredentials, 3},
		{"credentials file", vngcloud.ErrCredentialsFile, 3},
		{"login error", &vngcloud.LoginError{Reason: "sign-in failed", Status: 401, Err: vngcloud.ErrAuth}, 3},
		{
			"401 after retry",
			&vngcloud.APIError{Operation: "compute.ListServers", StatusCode: 401, Code: "Unauthorized", Err: vngcloud.ErrAuth},
			3,
		},
		{
			"not found",
			&vngcloud.APIError{Operation: "compute.GetServer", StatusCode: 404, Code: "NotFound", Err: vngcloud.ErrNotFound},
			4,
		},
		{
			// monitor.GetChannel has no get-by-ID call: a missing ID is
			// discovered by paging through the list and never reaches an
			// *APIError at all, unlike every other service's not-found result.
			"not found via a bare sentinel, not an APIError",
			fmt.Errorf("%w: channel %s", vngcloud.ErrNotFound, "missing"),
			4,
		},
		{"usage error", usageError{msg: "bad flag"}, 2},
		{"read only error", readOnlyError{source: "--read-only"}, 2},
		{"invalid input", fmt.Errorf("%w: Name is required", vngcloud.ErrInvalidInput), 2},
		{"invalid config", fmt.Errorf("%w: region is required", vngcloud.ErrInvalidConfig), 2},
		{"project ambiguous", fmt.Errorf("%w: hcm-3", vngcloud.ErrProjectAmbiguous), 2},
		{
			"query failed after write succeeded falls through to 1",
			&queryFailedError{err: errors.New("bad projection"), writeSucceeded: true},
			1,
		},
		{
			"server error",
			&vngcloud.APIError{Operation: "billing.ListBudgets", StatusCode: 500, Code: "ServerError"},
			1,
		},
		{"plain error", errors.New("boom"), 1},
		{"unexpected check status", fmt.Errorf("%w: chk-1 is %q", monitor.ErrUnexpectedStatus, "UNKNOWN"), 1},
		{"status unconfirmed", fmt.Errorf("%w: toggle sent, check still ENABLED", monitor.ErrStatusUnconfirmed), 1},
		{
			// A Ctrl-C during the toggle PUT or a confirm read leaves the
			// real error wrapping both ErrStatusUnconfirmed and a canceled
			// context; it must still exit like every other unconfirmed
			// toggle (1), checked ahead of the context-canceled rule above.
			"status unconfirmed after a canceled context",
			fmt.Errorf("%w: %w", monitor.ErrStatusUnconfirmed, context.Canceled),
			1,
		},
		{"status unconfirmed via a real SDK toggle canceled mid-PUT", realUnconfirmed, 1},
		{"dns zone busy", fmt.Errorf("%w: zone-1 did not leave CREATING", dns.ErrZoneBusy), 1},
		{"dns write failed", fmt.Errorf("%w: zone-1 is ERROR", dns.ErrFailed), 1},
		{"dns not settled", fmt.Errorf("%w: zone-1 was accepted", dns.ErrNotSettled), 1},
		{
			// A Ctrl-C during a vDNS post-write wait must still exit like
			// every other not-settled write (1), checked ahead of the
			// context-canceled rule above, per the vDNS design.
			"dns not settled after a canceled context",
			fmt.Errorf("%w: %w", dns.ErrNotSettled, context.Canceled),
			1,
		},
		{"network write failed", fmt.Errorf("%w: secg-1 is ERROR", network.ErrFailed), 1},
		{"network not settled", fmt.Errorf("%w: secg-1 was accepted", network.ErrNotSettled), 1},
		{
			// A Ctrl-C during network's post-create wait must exit the same
			// way (1), checked ahead of the context-canceled rule above, the
			// same rule the vDNS case above follows.
			"network not settled after a canceled context",
			fmt.Errorf("%w: %w", network.ErrNotSettled, context.Canceled),
			1,
		},
		{"network system group", fmt.Errorf("%w: security group secg-1 is a system group", network.ErrSystemGroup), 1},
		{
			"network security group in use",
			fmt.Errorf("%w: security group secg-1 has 1 server(s) attached", network.ErrSecurityGroupInUse),
			1,
		},
		{"network resource in use (VPC)", fmt.Errorf("%w: VPC vpc-1 has 1 subnet(s); delete them first", network.ErrInUse), 1},
		{"network resource in use (route table)", fmt.Errorf("%w: route table rt-1 is named by a subnet", network.ErrInUse), 1},
		{
			"loadbalancer certificate in use",
			fmt.Errorf("%w: loadbalancer.DeleteCertificate: certificate cert-1 is in use", loadbalancer.ErrCertificateInUse),
			1,
		},
		{"network default resource", fmt.Errorf("%w: route table rt-1 is the VPC's main route table", network.ErrDefaultResource), 1},
		{"network resource busy", fmt.Errorf("%w: route table rt-1 is not ACTIVE", network.ErrBusy), 1},
		{
			"network unexpected status",
			fmt.Errorf("%w: VPC vpc-1 dnsStatus is %q", network.ErrUnexpectedStatus, "UNKNOWN"),
			1,
		},
		{
			// A Ctrl-C right after EnableVPCPrivateDNS's pre-read observes an
			// unrecognized dnsStatus must still exit like every other
			// unexpected-status case (1), checked ahead of the
			// context-canceled rule above, per network's design.
			"network unexpected status after a canceled context",
			fmt.Errorf("%w: %w", network.ErrUnexpectedStatus, context.Canceled),
			1,
		},
		{
			"compute server group in use",
			fmt.Errorf("%w: server group sg-1 has 1 server(s) attached", compute.ErrServerGroupInUse),
			1,
		},
		{"compute not settled", fmt.Errorf("%w: server group sg-1 was accepted", compute.ErrNotSettled), 1},
		{
			// A Ctrl-C during UpdateServerGroup's confirm read must exit the
			// same way (1), checked ahead of the context-canceled rule above,
			// the same rule the vDNS and network cases above follow.
			"compute not settled after a canceled context",
			fmt.Errorf("%w: %w", compute.ErrNotSettled, context.Canceled),
			1,
		},
		{
			"containerregistry repository not empty",
			fmt.Errorf("%w: repository repo-1 has 1 image(s); delete them first", containerregistry.ErrRepositoryNotEmpty),
			1,
		},
		{"containerregistry not settled", fmt.Errorf("%w: repository repo-1 was accepted", containerregistry.ErrNotSettled), 1},
		{
			// A Ctrl-C during a vCR repository create's or delete's
			// post-write wait must exit the same way (1), checked ahead of
			// the context-canceled rule above, the same rule the vDNS,
			// network, and compute cases above follow.
			"containerregistry not settled after a canceled context",
			fmt.Errorf("%w: %w", containerregistry.ErrNotSettled, context.Canceled),
			1,
		},
		{
			"monitor log project price above max",
			fmt.Errorf("%w: monitor.CreateLogProject: quote 917000 VND exceeds MaxPrice 0 VND", monitor.ErrPriceAboveMax),
			1,
		},
		{
			"monitor otp rejected",
			fmt.Errorf("%w: monitor.CreateChannel: the otp for Email was wrong or expired", monitor.ErrOTPRejected),
			1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCode(tt.err); got != tt.want {
				t.Fatalf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	realUnconfirmed := realStatusUnconfirmedErr(t)
	tests := []struct {
		name       string
		err        error
		wantCode   string
		wantStatus int
		wantOp     string
	}{
		{"usage error", usageError{msg: "bad flag"}, "InvalidUsage", 0, ""},
		{"read only error", readOnlyError{source: "--read-only"}, "ReadOnly", 0, ""},
		{"no credentials", vngcloud.ErrNoCredentials, "NoCredentials", 0, ""},
		{"credentials file", vngcloud.ErrCredentialsFile, "NoCredentials", 0, ""},
		{"login error", &vngcloud.LoginError{Reason: "sign-in failed", Err: vngcloud.ErrAuth}, "LoginFailed", 0, ""},
		{"invalid input", fmt.Errorf("%w: Name is required", vngcloud.ErrInvalidInput), "InvalidUsage", 0, ""},
		{"invalid config", fmt.Errorf("%w: region is required", vngcloud.ErrInvalidConfig), "InvalidConfig", 0, ""},
		{"plain error falls back", errors.New("boom"), "RequestFailed", 0, ""},
		{
			"query failed",
			&queryFailedError{err: errors.New("bad projection")},
			"QueryFailed", 0, "",
		},
		{
			"api error with resolved code and status",
			&vngcloud.APIError{Operation: "compute.GetServer", StatusCode: 404, Code: "NotFound", Message: "server not found"},
			"NotFound", 404, "compute.GetServer",
		},
		{
			"api error with no code and no status falls back to RequestFailed",
			&vngcloud.APIError{Operation: "compute.GetServer", Err: errors.New("decode failed")},
			"RequestFailed", 0, "compute.GetServer",
		},
		{
			// See the matching case in TestExitCode: monitor.GetChannel's
			// not-found result is a bare sentinel, never an *APIError, so it
			// must reach "NotFound" through the check after the *APIError
			// branch rather than falling through to RequestFailed.
			"not found via a bare sentinel, not an APIError",
			fmt.Errorf("%w: channel %s", vngcloud.ErrNotFound, "missing"),
			"NotFound", 0, "",
		},
		{
			// monitor.DeleteChannel maps a 400 whose message says the
			// channel is already gone to vngcloud.ErrNotFound while keeping
			// the original *APIError (Code "BadRequest", Status 400) in the
			// same chain, so errors.As(err, &apiErr) finds it. Code must
			// still be "NotFound", not that APIError's own "BadRequest",
			// while Status and Operation are still filled from it.
			"not found wrapping an APIError",
			fmt.Errorf("%w: monitor.DeleteChannel: channel channel-1: %w", vngcloud.ErrNotFound,
				&vngcloud.APIError{
					Operation: "monitor.DeleteChannel", StatusCode: 400, Code: "BadRequest",
					Message: "Notification with id channel-1 is not found",
				}),
			"NotFound", 400, "monitor.DeleteChannel",
		},
		{
			"unexpected check status",
			fmt.Errorf("%w: chk-1 is %q", monitor.ErrUnexpectedStatus, "UNKNOWN"),
			"UnexpectedStatus", 0, "",
		},
		{
			"status unconfirmed",
			fmt.Errorf("%w: toggle sent, check still ENABLED", monitor.ErrStatusUnconfirmed),
			"StatusUnconfirmed", 0, "",
		},
		{
			"status unconfirmed after a canceled context",
			fmt.Errorf("%w: %w", monitor.ErrStatusUnconfirmed, context.Canceled),
			"StatusUnconfirmed", 0, "",
		},
		{
			// The real error PauseCheck/ResumeCheck return also wraps the
			// toggle PUT's own *APIError (here a 502) alongside
			// ErrStatusUnconfirmed. classify must still report
			// StatusUnconfirmed, not follow errors.As into that inner
			// APIError and report ServerError/502 instead.
			"status unconfirmed wrapping an inner APIError",
			fmt.Errorf("%w: %w", monitor.ErrStatusUnconfirmed,
				&vngcloud.APIError{Operation: "monitor.PauseCheck", StatusCode: 502, Code: "ServerError"}),
			"StatusUnconfirmed", 0, "",
		},
		{
			"status unconfirmed via a real SDK toggle canceled mid-PUT",
			realUnconfirmed,
			"StatusUnconfirmed", 0, "",
		},
		{
			"dns zone busy",
			fmt.Errorf("%w: zone-1 did not leave CREATING", dns.ErrZoneBusy),
			"ZoneBusy", 0, "",
		},
		{
			"dns write failed",
			fmt.Errorf("%w: zone-1 is ERROR", dns.ErrFailed),
			"WriteFailed", 0, "",
		},
		{
			"dns not settled",
			fmt.Errorf("%w: zone-1 was accepted", dns.ErrNotSettled),
			"NotSettled", 0, "",
		},
		{
			"network write failed",
			fmt.Errorf("%w: secg-1 is ERROR", network.ErrFailed),
			"WriteFailed", 0, "",
		},
		{
			"network not settled",
			fmt.Errorf("%w: secg-1 was accepted", network.ErrNotSettled),
			"NotSettled", 0, "",
		},
		{
			"network system group",
			fmt.Errorf("%w: security group secg-1 is a system group", network.ErrSystemGroup),
			"SystemSecurityGroup", 0, "",
		},
		{
			"network security group in use",
			fmt.Errorf("%w: security group secg-1 has 1 server(s) attached", network.ErrSecurityGroupInUse),
			"SecurityGroupInUse", 0, "",
		},
		{
			// wrapSecurityGroupInUse (network/security_groups_write.go)
			// rewraps the server's own refusal, an *APIError, alongside
			// ErrSecurityGroupInUse; Code must still be SecurityGroupInUse,
			// not that inner APIError's own status-derived code, the same
			// way monitor.ErrStatusUnconfirmed wins over its own inner
			// APIError above.
			"network security group in use wrapping an inner APIError",
			fmt.Errorf("%w: %w", network.ErrSecurityGroupInUse,
				&vngcloud.APIError{Operation: "network.DeleteSecurityGroup", StatusCode: 409, Code: "Conflict", Message: "SecurityGroupInUse"}),
			"SecurityGroupInUse", 0, "",
		},
		{
			"compute server group in use",
			fmt.Errorf("%w: server group sg-1 has 1 server(s) attached", compute.ErrServerGroupInUse),
			"ServerGroupInUse", 0, "",
		},
		{
			// wrapServerGroupInUse (compute/server_groups_write.go) rewraps
			// the server's own refusal, an *APIError, alongside
			// ErrServerGroupInUse; Code must still be ServerGroupInUse, not
			// that inner APIError's own status-derived code, the same way
			// network.ErrSecurityGroupInUse's equivalent case above does.
			"compute server group in use wrapping an inner APIError",
			fmt.Errorf("%w: %w", compute.ErrServerGroupInUse,
				&vngcloud.APIError{Operation: "compute.DeleteServerGroup", StatusCode: 409, Code: "Conflict", Message: "server group is in use"}),
			"ServerGroupInUse", 0, "",
		},
		{
			"compute not settled",
			fmt.Errorf("%w: server group sg-1 was accepted", compute.ErrNotSettled),
			"NotSettled", 0, "",
		},
		{
			"containerregistry repository not empty",
			fmt.Errorf("%w: repository repo-1 has 1 image(s); delete them first", containerregistry.ErrRepositoryNotEmpty),
			"RepositoryNotEmpty", 0, "",
		},
		{
			"containerregistry not settled",
			fmt.Errorf("%w: repository repo-1 was accepted", containerregistry.ErrNotSettled),
			"NotSettled", 0, "",
		},
		{
			"network resource in use (VPC)",
			fmt.Errorf("%w: VPC vpc-1 has 1 subnet(s); delete them first", network.ErrInUse),
			"ResourceInUse", 0, "",
		},
		{
			// wrapVPCContainsSubnet (network/vpcs_write.go) rewraps the
			// server's own refusal, an *APIError, alongside ErrInUse; Code
			// must still be ResourceInUse, not that inner APIError's own
			// status-derived code, the same way network.ErrSecurityGroupInUse
			// wins over its own inner APIError above.
			"network resource in use wrapping an inner APIError",
			fmt.Errorf("%w: %w", network.ErrInUse,
				&vngcloud.APIError{Operation: "network.DeleteVPC", StatusCode: 400, Code: "BadRequest", Message: "Cannot delete this VPC because it contains the subnet."}),
			"ResourceInUse", 0, "",
		},
		{
			// A route table delete's ErrInUse is only ever returned bare
			// today (network/route_tables_write.go sends nothing before
			// building it), but the sentinel is shared, and the wrapping
			// case just above already confirms Code stays ResourceInUse
			// ahead of the generic *APIError branch regardless of which
			// resource produced the wrap.
			"network resource in use (route table)",
			fmt.Errorf("%w: route table rt-1 is named by a subnet of its VPC", network.ErrInUse),
			"ResourceInUse", 0, "",
		},
		{
			"loadbalancer certificate in use (pre-delete read)",
			fmt.Errorf("%w: loadbalancer.DeleteCertificate: certificate cert-1 is in use", loadbalancer.ErrCertificateInUse),
			"ResourceInUse", 0, "",
		},
		{
			// wrapCertificateDeleteErr (loadbalancer/certificates_write.go)
			// rewraps the server's own refusal of the DELETE itself, an
			// *APIError, alongside ErrCertificateInUse; Code must still be
			// ResourceInUse, not that inner APIError's own status-derived code,
			// the same way network.ErrInUse wins over its own inner APIError
			// above.
			"loadbalancer certificate in use wrapping an inner APIError",
			fmt.Errorf("%w: loadbalancer.DeleteCertificate: certificate cert-1: %w", loadbalancer.ErrCertificateInUse,
				&vngcloud.APIError{Operation: "loadbalancer.DeleteCertificate", StatusCode: 409, Code: "Conflict", Message: "certificate is used by a listener"}),
			"ResourceInUse", 0, "",
		},
		{
			"network default resource",
			fmt.Errorf("%w: route table rt-1 is the VPC's main route table and a subnet relies on it", network.ErrDefaultResource),
			"DefaultResource", 0, "",
		},
		{
			"network resource busy",
			fmt.Errorf("%w: route table rt-1 is not ACTIVE within 1m0s; nothing sent", network.ErrBusy),
			"ResourceBusy", 0, "",
		},
		{
			"network unexpected status",
			fmt.Errorf("%w: VPC vpc-1 dnsStatus is %q", network.ErrUnexpectedStatus, "UNKNOWN"),
			"UnexpectedStatus", 0, "",
		},
		{
			"monitor log project price above max",
			fmt.Errorf("%w: monitor.CreateLogProject: quote 917000 VND exceeds MaxPrice 0 VND", monitor.ErrPriceAboveMax),
			"PriceAboveMax", 0, "",
		},
		{
			"monitor otp rejected",
			fmt.Errorf("%w: monitor.CreateChannel: the otp for Email was wrong or expired", monitor.ErrOTPRejected),
			"OTPRejected", 0, "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := classify(tt.err)
			if env.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", env.Code, tt.wantCode)
			}
			if env.Status != tt.wantStatus {
				t.Errorf("Status = %d, want %d", env.Status, tt.wantStatus)
			}
			if env.Operation != tt.wantOp {
				t.Errorf("Operation = %q, want %q", env.Operation, tt.wantOp)
			}
			if env.Message == "" {
				t.Errorf("Message is empty")
			}
		})
	}
}

func TestPrintErrorShape(t *testing.T) {
	var buf bytes.Buffer
	printError(&buf, &vngcloud.APIError{Operation: "compute.GetServer", StatusCode: 404, Code: "NotFound", Message: "server not found"})

	out := buf.String()
	if n := bytes.Count(buf.Bytes(), []byte("\n")); n != 1 {
		t.Fatalf("expected exactly one line, got %d newlines in %q", n, out)
	}

	var decoded struct {
		Error errorEnvelope `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, out)
	}
	if decoded.Error.Code != "NotFound" || decoded.Error.Status != 404 || decoded.Error.Operation != "compute.GetServer" {
		t.Fatalf("unexpected envelope: %+v", decoded.Error)
	}
	if decoded.Error.Message != "server not found" {
		t.Fatalf("Message = %q", decoded.Error.Message)
	}
}

func TestPrintErrorOmitsStatusAndOperationForNonAPIError(t *testing.T) {
	var buf bytes.Buffer
	printError(&buf, usageError{msg: "unknown flag --nope"})

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	var inner map[string]json.RawMessage
	if err := json.Unmarshal(decoded["error"], &inner); err != nil {
		t.Fatalf("error field is not an object: %v", err)
	}
	if _, ok := inner["status"]; ok {
		t.Fatalf("status should be omitted for a non-API error, got %s", decoded["error"])
	}
	if _, ok := inner["operation"]; ok {
		t.Fatalf("operation should be omitted for a non-API error, got %s", decoded["error"])
	}
}
