package cdn

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

const cdnID = "cdn-1"

type writeCall struct {
	name   string
	run    func(h *vcdnHarness) error
	method string
	path   string
}

func writeCalls() []writeCall {
	ctx := context.Background()
	return []writeCall{
		{"update", func(h *vcdnHarness) error {
			_, err := h.UpdateWebAccelerator(ctx, &UpdateWebAcceleratorInput{CDNID: cdnID, SetRuleActions: []RuleActionInput{{Name: "browserCache", Value: "1d"}}, NoWait: true})
			return err
		}, http.MethodPut, "PUT cdn/update"},
		{"delete", func(h *vcdnHarness) error {
			_, err := h.DeleteWebAccelerator(ctx, &DeleteWebAcceleratorInput{CDNID: cdnID})
			return err
		}, http.MethodDelete, "DELETE cdn/delete/" + cdnID},
		{"enable", func(h *vcdnHarness) error {
			_, err := h.EnableWebAccelerator(ctx, &EnableWebAcceleratorInput{CDNID: cdnID, NoWait: true})
			return err
		}, http.MethodPut, "PUT cdn/status/change/" + cdnID},
		{"disable", func(h *vcdnHarness) error {
			_, err := h.DisableWebAccelerator(ctx, &DisableWebAcceleratorInput{CDNID: cdnID, NoWait: true})
			return err
		}, http.MethodPut, "PUT cdn/status/change/" + cdnID},
	}
}

func TestWritesRefuseBadInputWithoutARequest(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(h *vcdnHarness) error{}
	for _, id := range []string{"", "..", "a/b", "a?b", "a b", "../x"} {
		cases["update "+id] = func(h *vcdnHarness) error {
			_, err := h.UpdateWebAccelerator(ctx, &UpdateWebAcceleratorInput{CDNID: id, CNames: []string{}})
			return err
		}
		cases["delete "+id] = func(h *vcdnHarness) error {
			_, err := h.DeleteWebAccelerator(ctx, &DeleteWebAcceleratorInput{CDNID: id})
			return err
		}
		cases["enable "+id] = func(h *vcdnHarness) error {
			_, err := h.EnableWebAccelerator(ctx, &EnableWebAcceleratorInput{CDNID: id})
			return err
		}
		cases["disable "+id] = func(h *vcdnHarness) error {
			_, err := h.DisableWebAccelerator(ctx, &DisableWebAcceleratorInput{CDNID: id})
			return err
		}
		cases["get "+id] = func(h *vcdnHarness) error {
			_, err := h.GetWebAccelerator(ctx, &GetWebAcceleratorInput{CDNID: id})
			return err
		}
	}
	set := func(in UpdateWebAcceleratorInput) func(h *vcdnHarness) error {
		in.CDNID = cdnID
		return func(h *vcdnHarness) error { _, err := h.UpdateWebAccelerator(ctx, &in); return err }
	}
	cases["update nil"] = func(h *vcdnHarness) error { _, err := h.UpdateWebAccelerator(ctx, nil); return err }
	cases["update only NoWait"] = set(UpdateWebAcceleratorInput{NoWait: true})
	cases["update empty lists"] = set(UpdateWebAcceleratorInput{SetRuleActions: []RuleActionInput{}, RemoveRuleActions: []string{}, Upstreams: []UpstreamInput{}})
	cases["update name twice"] = set(UpdateWebAcceleratorInput{SetRuleActions: []RuleActionInput{{Name: "a"}, {Name: "a"}}})
	cases["update set and remove"] = set(UpdateWebAcceleratorInput{SetRuleActions: []RuleActionInput{{Name: "a"}}, RemoveRuleActions: []string{"a"}})
	cases["update remove twice"] = set(UpdateWebAcceleratorInput{RemoveRuleActions: []string{"a", "a"}})
	cases["update empty name"] = set(UpdateWebAcceleratorInput{SetRuleActions: []RuleActionInput{{Name: ""}}})
	cases["update empty remove name"] = set(UpdateWebAcceleratorInput{RemoveRuleActions: []string{""}})
	cases["update upstream no address"] = set(UpdateWebAcceleratorInput{Upstreams: []UpstreamInput{{Priority: 1}}})
	cases["update upstream bad id"] = set(UpdateWebAcceleratorInput{Upstreams: []UpstreamInput{{ID: "../x", IPAddress: "198.51.100.1"}}})
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			h := newVCDN(t, testKey, reply(200, "application/json", `{}`))
			if err := run(h); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
			if h.requests.Load() != 0 {
				t.Errorf("requests = %d, want 0", h.requests.Load())
			}
		})
	}
}

func TestWritesWithoutKeySendNothing(t *testing.T) {
	h := newVCDN(t, "", reply(200, "application/json", `{}`))
	for _, c := range writeCalls() {
		if err := c.run(h); !errors.Is(err, ErrNoAPIKey) {
			t.Errorf("%s err = %v, want ErrNoAPIKey", c.name, err)
		}
	}
	if h.requests.Load() != 0 {
		t.Fatalf("requests = %d", h.requests.Load())
	}
}

// TestStatusGuard runs the whole guard table: a refusal sends nothing but
// the read.
func TestStatusGuard(t *testing.T) {
	type want struct {
		sends bool
		err   error
		// noop is a toggle already on its target.
		noop bool
	}
	table := map[int]map[string]want{
		StatusActive:    {"update": {sends: true}, "delete": {sends: true}, "enable": {noop: true}, "disable": {sends: true}},
		StatusDisabled:  {"update": {err: vngcloud.ErrInvalidInput}, "delete": {sends: true}, "enable": {sends: true}, "disable": {noop: true}},
		StatusDeploying: {"update": {err: ErrBusy}, "delete": {err: ErrBusy}, "enable": {err: ErrBusy}, "disable": {err: ErrBusy}},
		StatusDisabling: {"update": {err: ErrBusy}, "delete": {err: ErrBusy}, "enable": {err: ErrBusy}, "disable": {err: ErrBusy}},
		StatusDeleting:  {"update": {err: ErrBusy}, "delete": {err: ErrBusy}, "enable": {err: ErrBusy}, "disable": {err: ErrBusy}},
		2:               {"update": {err: ErrUnexpectedStatus}, "delete": {err: ErrUnexpectedStatus}, "enable": {err: ErrUnexpectedStatus}, "disable": {err: ErrUnexpectedStatus}},
		7:               {"update": {err: ErrUnexpectedStatus}, "delete": {err: ErrUnexpectedStatus}, "enable": {err: ErrUnexpectedStatus}, "disable": {err: ErrUnexpectedStatus}},
	}
	for status, row := range table {
		for _, c := range writeCalls() {
			w := row[c.name]
			t.Run(StatusName(status)+" "+c.name, func(t *testing.T) {
				s := newSim(t, status, StatusDeploying, StatusDisabling)
				// After a toggle the sim must show a status the confirm read accepts.
				switch c.name {
				case "enable":
					s.seq = []int{StatusDeploying}
				case "disable":
					s.seq = []int{StatusDisabling}
				default:
					s.seq = []int{StatusDeploying}
				}
				h, _ := s.harness(t)
				err := c.run(h)
				if w.err != nil {
					if !errors.Is(err, w.err) {
						t.Fatalf("err = %v, want %v", err, w.err)
					}
				} else if err != nil {
					t.Fatalf("err = %v", err)
				}
				writes := s.log.count(c.method)
				if (w.sends && writes != 1) || (!w.sends && writes != 0) {
					t.Fatalf("writes = %d, sends = %v; log %v", writes, w.sends, s.log.lines)
				}
				if !w.sends && s.log.total() != 1 {
					t.Fatalf("requests = %d, want only the read", s.log.total())
				}
			})
		}
	}
}

func TestToggleChangedFlag(t *testing.T) {
	ctx := context.Background()
	s := newSim(t, StatusActive)
	h, _ := s.harness(t)
	out, err := h.EnableWebAccelerator(ctx, &EnableWebAcceleratorInput{CDNID: cdnID})
	if err != nil || out.Changed || out.WebAccelerator.Status != StatusActive || out.WebAccelerator.StatusName != "ACTIVE" {
		t.Fatalf("enable on active: out = %+v err = %v", out, err)
	}
	s = newSim(t, StatusDisabled)
	h, _ = s.harness(t)
	out2, err := h.DisableWebAccelerator(ctx, &DisableWebAcceleratorInput{CDNID: cdnID})
	if err != nil || out2.Changed {
		t.Fatalf("disable on disabled: out = %+v err = %v", out2, err)
	}
}

func TestDisableWaitsForDisabled(t *testing.T) {
	s := newSim(t, StatusActive, StatusDisabling, StatusDisabling, StatusDisabled)
	h, clock := s.harness(t)
	out, err := h.DisableWebAccelerator(context.Background(), &DisableWebAcceleratorInput{CDNID: cdnID})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Changed || out.WebAccelerator.Status != StatusDisabled || out.WebAccelerator.StatusName != "DISABLED" {
		t.Fatalf("out = %+v", out)
	}
	if got := s.log.count("PUT cdn/status/change/"); got != 1 {
		t.Fatalf("toggles = %d", got)
	}
	// The first post-toggle read shows 5 and is the confirm read; two more
	// polls 10 seconds apart reach 0.
	if clock.slept() != 20*time.Second {
		t.Fatalf("slept %v, want 20s", clock.slept())
	}
}

func TestEnableWaitsForActive(t *testing.T) {
	s := newSim(t, StatusDisabled, StatusDeploying, StatusDeploying, StatusActive)
	h, _ := s.harness(t)
	out, err := h.EnableWebAccelerator(context.Background(), &EnableWebAcceleratorInput{CDNID: cdnID})
	if err != nil || !out.Changed || out.WebAccelerator.Status != StatusActive {
		t.Fatalf("out = %+v err = %v", out, err)
	}
}

func TestNoWaitReturnsAfterOneConfirmRead(t *testing.T) {
	for _, c := range []struct {
		name  string
		start int
		seq   []int
		run   func(h *vcdnHarness) (*WebAccelerator, error)
	}{
		{"disable", StatusActive, []int{StatusDisabling}, func(h *vcdnHarness) (*WebAccelerator, error) {
			o, err := h.DisableWebAccelerator(context.Background(), &DisableWebAcceleratorInput{CDNID: cdnID, NoWait: true})
			return &o.WebAccelerator, err
		}},
		{"enable", StatusDisabled, []int{StatusDeploying}, func(h *vcdnHarness) (*WebAccelerator, error) {
			o, err := h.EnableWebAccelerator(context.Background(), &EnableWebAcceleratorInput{CDNID: cdnID, NoWait: true})
			return &o.WebAccelerator, err
		}},
		{"update", StatusActive, []int{StatusDeploying}, func(h *vcdnHarness) (*WebAccelerator, error) {
			o, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{CDNID: cdnID, CNames: []string{"x.example.test"}, NoWait: true})
			return &o.WebAccelerator, err
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSim(t, c.start, c.seq...)
			h, clock := s.harness(t)
			wa, err := c.run(h)
			if err != nil || wa.Status != c.seq[0] {
				t.Fatalf("wa = %+v err = %v", wa, err)
			}
			if clock.slept() != 0 || s.reads != 2 {
				t.Fatalf("slept %v, reads %d, want 0 and 2", clock.slept(), s.reads)
			}
		})
	}
}

func TestSettleBoundReturnsNotSettledWithOutput(t *testing.T) {
	s := newSim(t, StatusActive, StatusDisabling)
	h, clock := s.harness(t)
	out, err := h.DisableWebAccelerator(context.Background(), &DisableWebAcceleratorInput{CDNID: cdnID})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || out.WebAccelerator.Status != StatusDisabling || !out.Changed {
		t.Fatalf("out = %+v, want the last read", out)
	}
	if !strings.Contains(err.Error(), "do not repeat") {
		t.Fatalf("message = %q", err)
	}
	if clock.slept() < 6*time.Minute || clock.slept() > 6*time.Minute+10*time.Second {
		t.Fatalf("slept %v, want the 6 minute bound", clock.slept())
	}
	if got := s.log.count("PUT"); got != 1 {
		t.Fatalf("toggles = %d", got)
	}
}

func TestSettleAnotherStatusEndsTheWait(t *testing.T) {
	s := newSim(t, StatusActive, StatusDeploying, 2)
	h, _ := s.harness(t)
	out, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{CDNID: cdnID, CNames: []string{"x.example.test"}})
	if !errors.Is(err, ErrUnexpectedStatus) || out == nil || out.WebAccelerator.Status != 2 {
		t.Fatalf("out = %+v err = %v", out, err)
	}
	if !strings.Contains(err.Error(), "UNKNOWN(2)") {
		t.Fatalf("message = %q, want the status named", err)
	}
}

func TestSettleCDNDeletedEndsWithNotFound(t *testing.T) {
	s := newSim(t, StatusActive, StatusDeploying)
	s.detailOverride = func(n int) (string, bool) {
		if n >= 3 {
			return `{"success":false,"code":500,"message":null,"data":""}`, true
		}
		return "", false
	}
	h, _ := s.harness(t)
	out, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{CDNID: cdnID, CNames: []string{"x.example.test"}})
	if !errors.Is(err, vngcloud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if out == nil || out.WebAccelerator.Status != StatusDeploying {
		t.Fatalf("out = %+v, want the last good read", out)
	}
}

func TestSettleRetriesAReadError(t *testing.T) {
	s := newSim(t, StatusActive, StatusActive)
	s.detailOverride = func(n int) (string, bool) {
		if n == 2 {
			return `{"success":false,"code":500,"message":"try later","data":{"x":1}}`, true
		}
		if n == 3 {
			return detailWithStatus(t, StatusDeploying), true
		}
		return "", false
	}
	h, _ := s.harness(t)
	out, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{CDNID: cdnID, CNames: []string{"x.example.test"}})
	if err != nil || out.WebAccelerator.Status != StatusActive {
		t.Fatalf("out = %+v err = %v", out, err)
	}
	if s.reads != 4 {
		t.Fatalf("reads = %d, want 4", s.reads)
	}
}

func TestSettleCancelledContext(t *testing.T) {
	s := newSim(t, StatusActive, StatusDisabling)
	h, _ := s.harness(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.sleep = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
	out, err := h.DisableWebAccelerator(ctx, &DisableWebAcceleratorInput{CDNID: cdnID})
	if !errors.Is(err, ErrNotSettled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want ErrNotSettled wrapping Canceled", err)
	}
	if out == nil || out.WebAccelerator.Status != StatusDisabling {
		t.Fatalf("out = %+v", out)
	}
}

func TestToggleConfirmReadsAtZeroTwoFourEight(t *testing.T) {
	// The CDN shows its old status for three confirm reads, then DISABLING.
	s := newSim(t, StatusActive)
	s.seq = []int{StatusActive, StatusActive, StatusActive, StatusDisabling}
	h, clock := s.harness(t)
	out, err := h.DisableWebAccelerator(context.Background(), &DisableWebAcceleratorInput{CDNID: cdnID, NoWait: true})
	if err != nil || out.WebAccelerator.Status != StatusDisabling {
		t.Fatalf("out = %+v err = %v", out, err)
	}
	want := []time.Duration{2 * time.Second, 2 * time.Second, 4 * time.Second}
	if len(clock.sleeps) != 3 || clock.sleeps[0] != want[0] || clock.sleeps[1] != want[1] || clock.sleeps[2] != want[2] {
		t.Fatalf("sleeps = %v, want %v", clock.sleeps, want)
	}
}

func TestToggleConfirmAccountsForReadDuration(t *testing.T) {
	s := newSim(t, StatusActive)
	s.seq = []int{StatusActive, StatusActive, StatusActive, StatusDisabling}
	var clock *fakeClock
	var start time.Time
	var reads []time.Duration
	s.beforeDetail = func(n int) {
		if n >= 2 {
			reads = append(reads, clock.time().Sub(start))
			clock.advance(time.Second)
		}
	}
	h, clock := s.harness(t)
	start = clock.time()
	out, err := h.DisableWebAccelerator(context.Background(), &DisableWebAcceleratorInput{CDNID: cdnID, NoWait: true})
	if err != nil || out.WebAccelerator.Status != StatusDisabling {
		t.Fatalf("out = %+v err = %v", out, err)
	}
	want := []time.Duration{0, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	if len(reads) != len(want) {
		t.Fatalf("reads = %v, want %v", reads, want)
	}
	for i := range want {
		if reads[i] != want[i] {
			t.Fatalf("reads = %v, want %v", reads, want)
		}
	}
}

func TestToggleSettleUsesWriteDeadline(t *testing.T) {
	s := newSim(t, StatusActive)
	s.seq = []int{StatusActive, StatusActive, StatusActive, StatusDisabling}
	h, clock := s.harness(t)
	_, err := h.DisableWebAccelerator(context.Background(), &DisableWebAcceleratorInput{CDNID: cdnID})
	if !errors.Is(err, ErrNotSettled) || clock.slept() != settleBound {
		t.Fatalf("err = %v slept = %v", err, clock.slept())
	}
}

func TestUpdateRedactsUserUUIDFromWriteErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"envelope", 0, `{"success":false,"code":"<user-id>","message":"bad <user-id>","data":{}}`},
		{"HTTP", http.StatusBadRequest, `{"code":"<user-id>","message":"bad <user-id>"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSim(t, StatusActive)
			s.writeHTTP, s.writeBody = tc.status, tc.body
			h, _ := s.harness(t)
			_, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{
				CDNID: cdnID, CNames: []string{"a.example.test"}, NoWait: true,
			})
			if err == nil || strings.Contains(err.Error(), "<user-id>") || strings.Contains(h.logs.String(), "<user-id>") || strings.Contains(h.writes.String(), "<user-id>") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestUpdateNoWaitReadFailureReturnsPreWriteOutput(t *testing.T) {
	s := newSim(t, StatusActive)
	s.detailOverride = func(n int) (string, bool) {
		if n >= 2 {
			return `{"success":false,"code":500,"message":"try later","data":{}}`, true
		}
		return "", false
	}
	h, _ := s.harness(t)
	out, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{
		CDNID: cdnID, CNames: []string{"a.example.test"}, NoWait: true,
	})
	if !errors.Is(err, ErrNotSettled) || out == nil || out.WebAccelerator.Status != StatusActive {
		t.Fatalf("out = %+v err = %v", out, err)
	}
	if s.log.count("PUT cdn/update") != 1 || s.reads != 2 {
		t.Fatalf("writes = %d reads = %d", s.log.count("PUT cdn/update"), s.reads)
	}
}

func TestUpdateCancelledFollowUpReturnsPreWriteOutput(t *testing.T) {
	s := newSim(t, StatusActive)
	h, _ := s.harness(t)
	ctx, cancel := context.WithCancel(context.Background())
	s.beforeDetail = func(n int) {
		if n == 2 {
			cancel()
		}
	}
	out, err := h.UpdateWebAccelerator(ctx, &UpdateWebAcceleratorInput{
		CDNID: cdnID, CNames: []string{"a.example.test"}, NoWait: true,
	})
	if !errors.Is(err, ErrNotSettled) || !errors.Is(err, context.Canceled) || out == nil || out.WebAccelerator.Status != StatusActive {
		t.Fatalf("out = %+v err = %v", out, err)
	}
}

func TestUpdateRepeatedFollowUpErrorsReturnPreWriteOutput(t *testing.T) {
	s := newSim(t, StatusActive)
	s.detailOverride = func(n int) (string, bool) {
		if n >= 2 {
			return `{"success":false,"code":500,"message":"try later","data":{}}`, true
		}
		return "", false
	}
	h, clock := s.harness(t)
	out, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{
		CDNID: cdnID, CNames: []string{"a.example.test"},
	})
	if !errors.Is(err, ErrNotSettled) || out == nil || out.WebAccelerator.Status != StatusActive || clock.slept() != settleBound {
		t.Fatalf("out = %+v err = %v slept = %v", out, err, clock.slept())
	}
	if s.log.count("PUT cdn/update") != 1 {
		t.Fatalf("writes = %d", s.log.count("PUT cdn/update"))
	}
}

func TestUpdateDoesNotReadAfterDeadline(t *testing.T) {
	s := newSim(t, StatusActive, StatusDeploying)
	var clock *fakeClock
	s.beforeDetail = func(n int) {
		if n == 2 {
			clock.advance(settleBound)
		}
	}
	h, clock := s.harness(t)
	out, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{
		CDNID: cdnID, CNames: []string{"a.example.test"},
	})
	if !errors.Is(err, ErrNotSettled) || out == nil || out.WebAccelerator.Status != StatusActive || s.reads != 2 {
		t.Fatalf("out = %+v err = %v reads = %d", out, err, s.reads)
	}
}

func TestToggleUnconfirmed(t *testing.T) {
	s := newSim(t, StatusActive, StatusActive)
	s.writeHTTP = http.StatusBadGateway
	h, _ := s.harness(t)
	_, err := h.DisableWebAccelerator(context.Background(), &DisableWebAcceleratorInput{CDNID: cdnID})
	if !errors.Is(err, ErrStatusUnconfirmed) {
		t.Fatalf("err = %v, want ErrStatusUnconfirmed", err)
	}
	var apiErr interface{ Error() string }
	if !errors.As(err, &apiErr) || !strings.Contains(err.Error(), "read the CDN") {
		t.Fatalf("err = %v", err)
	}
	if got := s.log.count("PUT"); got != 1 {
		t.Fatalf("toggles = %d, want 1: a 502 is never resent", got)
	}
	if got := apiError(t, err).StatusCode; got != http.StatusBadGateway {
		t.Fatalf("wrapped status = %d, want the toggle's 502", got)
	}
}

func TestToggleLostResponseButChanged(t *testing.T) {
	s := newSim(t, StatusDisabled, StatusDeploying, StatusActive)
	s.writeHTTP = http.StatusBadGateway
	h, _ := s.harness(t)
	out, err := h.EnableWebAccelerator(context.Background(), &EnableWebAcceleratorInput{CDNID: cdnID})
	if err != nil || !out.Changed || out.WebAccelerator.Status != StatusActive {
		t.Fatalf("out = %+v err = %v", out, err)
	}
	if got := s.log.count("PUT"); got != 1 {
		t.Fatalf("toggles = %d", got)
	}
}

// A refusal the server gave before acting returns at once, with no confirm
// read.
func TestToggleRefusalSkipsConfirm(t *testing.T) {
	for name, tc := range map[string]struct {
		http int
		body string
		is   error
	}{
		"busy 500":     {0, busyEnvelope, ErrBusy},
		"busy 400":     {0, `{"success":false,"code":400,"message":"Current cdn status is not allow to update or delete","data":""}`, ErrBusy},
		"400":          {400, `{"detail":"bad"}`, vngcloud.ErrInvalidInput},
		"401":          {401, ``, vngcloud.ErrAuth},
		"403":          {403, ``, vngcloud.ErrPermission},
		"404":          {404, `{"detail":"x"}`, vngcloud.ErrNotFound},
		"envelope 500": {0, `{"success":false,"code":500,"message":"nope","data":""}`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSim(t, StatusActive, StatusDisabling)
			s.writeHTTP, s.writeBody = tc.http, tc.body
			h, _ := s.harness(t)
			_, err := h.DisableWebAccelerator(context.Background(), &DisableWebAcceleratorInput{CDNID: cdnID})
			if err == nil || errors.Is(err, ErrStatusUnconfirmed) {
				t.Fatalf("err = %v, want the refusal", err)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("err = %v, want %v", err, tc.is)
			}
			wantReads := 1
			if tc.http == http.StatusUnauthorized {
				// A 401 costs one more read, to tell a gone CDN from a bad key.
				wantReads = 2
			}
			if s.reads != wantReads || s.log.count("PUT") != 1 {
				t.Fatalf("reads = %d, toggles = %d, want %d and 1", s.reads, s.log.count("PUT"), wantReads)
			}
		})
	}
}

func TestDeleteSendsOnceAndDoesNotWait(t *testing.T) {
	s := newSim(t, StatusActive)
	h, clock := s.harness(t)
	if _, err := h.DeleteWebAccelerator(context.Background(), &DeleteWebAcceleratorInput{CDNID: cdnID}); err != nil {
		t.Fatal(err)
	}
	if s.log.count("DELETE cdn/delete/"+cdnID) != 1 || s.reads != 1 || clock.slept() != 0 {
		t.Fatalf("log %v, reads %d, slept %v", s.log.lines, s.reads, clock.slept())
	}
}

func TestDeleteUnknownIDIsNotFoundWithoutADelete(t *testing.T) {
	h := newVCDN(t, testKey, reply(200, "application/json", `{"success":false,"code":500,"message":null,"data":""}`))
	_, err := h.DeleteWebAccelerator(context.Background(), &DeleteWebAcceleratorInput{CDNID: cdnID})
	if !errors.Is(err, vngcloud.ErrNotFound) || h.requests.Load() != 1 {
		t.Fatalf("err = %v, requests = %d", err, h.requests.Load())
	}
}

func TestDeleteAfterServerErrorIsNotResent(t *testing.T) {
	s := newSim(t, StatusActive)
	s.writeHTTP = http.StatusBadGateway
	h, _ := s.harness(t)
	_, err := h.DeleteWebAccelerator(context.Background(), &DeleteWebAcceleratorInput{CDNID: cdnID})
	if apiError(t, err).StatusCode != 502 || !strings.Contains(err.Error(), "may have been applied") {
		t.Fatalf("err = %v", err)
	}
	if got := s.log.count("DELETE"); got != 1 {
		t.Fatalf("deletes = %d, want 1", got)
	}
}

// Every write maps every error status and the envelope failure.
func TestWriteErrorStatuses(t *testing.T) {
	for _, c := range writeCalls() {
		for _, tc := range []struct {
			name string
			http int
			body string
			is   error
		}{
			{"400", 400, `{"detail":"bad"}`, vngcloud.ErrInvalidInput},
			{"401", 401, ``, vngcloud.ErrAuth},
			{"403", 403, ``, vngcloud.ErrPermission},
			{"404", 404, `{"detail":"x"}`, vngcloud.ErrNotFound},
			{"429", 429, ``, vngcloud.ErrRateLimited},
			{"500", 500, ``, nil},
			{"envelope failure", 0, `{"success":false,"code":500,"message":"limit reached","data":""}`, nil},
			{"envelope not found", 0, `{"success":false,"code":null,"message":"Not found cdn","data":""}`, vngcloud.ErrNotFound},
			{"envelope 202", 0, `{"success":false,"code":202,"message":"refused","data":""}`, vngcloud.ErrInvalidInput},
			{"envelope null code", 0, `{"success":false,"code":null,"message":"odd","data":""}`, nil},
		} {
			t.Run(c.name+" "+tc.name, func(t *testing.T) {
				start := StatusActive
				if c.name == "enable" {
					start = StatusDisabled
				}
				s := newSim(t, start, StatusDeploying, StatusDisabling)
				if tc.http >= 500 {
					// The toggle may have landed: only a read that shows no change
					// leaves the call failed.
					s.seq = []int{start}
				}
				s.writeHTTP, s.writeBody = tc.http, tc.body
				h, _ := s.harness(t)
				err := c.run(h)
				if err == nil {
					t.Fatal("err = nil")
				}
				if tc.is != nil && !errors.Is(err, tc.is) {
					t.Fatalf("err = %v, want %v", err, tc.is)
				}
				if (tc.http == 429 || tc.http >= 500) && s.log.count(c.method) != 1 {
					t.Fatalf("writes = %d, want 1: a 429 or 5xx is never resent", s.log.count(c.method))
				}
				if strings.Contains(err.Error(), testKey) {
					t.Fatal("error holds the key")
				}
			})
		}
	}
}

func TestEnvelopeRowsOnReadsAndWrites(t *testing.T) {
	type row struct {
		name, body string
		is         error
		code       string
		only       bool
	}
	rows := []row{
		{"busy 500", busyEnvelope, ErrBusy, "500", true},
		{"busy 400", `{"success":false,"code":400,"message":"Current cdn status is not allow to update or delete","data":""}`, ErrBusy, "400", true},
		{"not found null code", `{"success":false,"code":null,"message":"Not found cdn","data":""}`, vngcloud.ErrNotFound, "NotFound", false},
		{"not found 500", `{"success":false,"code":500,"message":"Not found cdn with id x","data":""}`, vngcloud.ErrNotFound, "NotFound", false},
		{"202", `{"success":false,"code":202,"message":"bad input","data":""}`, vngcloud.ErrInvalidInput, "202", false},
		{"null code", `{"success":false,"code":null,"message":"odd","data":null}`, nil, "EnvelopeError", false},
		{"absent code", `{"success":false,"message":"odd","data":null}`, nil, "EnvelopeError", false},
	}
	for _, r := range rows {
		for name, run := range map[string]func(h *vcdnHarness) error{
			"read": func(h *vcdnHarness) error {
				_, err := h.ListWebAccelerators(context.Background(), nil)
				return err
			},
			"write": func(h *vcdnHarness) error {
				_, err := h.DeleteWebAccelerator(context.Background(), &DeleteWebAcceleratorInput{CDNID: cdnID})
				return err
			},
		} {
			t.Run(r.name+" "+name, func(t *testing.T) {
				s := newSim(t, StatusActive)
				h := newVCDN(t, testKey, s.log.wrap(func(w http.ResponseWriter, req *http.Request) {
					if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "detail") {
						jsonReply(w, detailWithStatus(t, StatusActive))
						return
					}
					jsonReply(w, r.body)
				}))
				err := run(h)
				apiErr := apiError(t, err)
				if apiErr.Code != r.code {
					t.Errorf("code = %q, want %q", apiErr.Code, r.code)
				}
				if r.is != nil && !errors.Is(err, r.is) {
					t.Errorf("err = %v, want %v", err, r.is)
				}
				if r.only {
					for _, other := range []error{vngcloud.ErrInvalidInput, vngcloud.ErrNotFound, vngcloud.ErrAuth, vngcloud.ErrPermission} {
						if errors.Is(err, other) {
							t.Errorf("busy error also matches %v", other)
						}
					}
				}
			})
		}
	}
}

// A server that echoes the key in a write refusal shows [redacted], and the
// request body never holds the key.
func TestWriteNeverCarriesOrEchoesTheKey(t *testing.T) {
	s := newSim(t, StatusActive)
	s.writeBody = `{"success":false,"code":500,"message":"bad ` + testKey + `","data":""}`
	h, _ := s.harness(t)
	_, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{CDNID: cdnID, CNames: []string{"a.example.test"}})
	if err == nil || strings.Contains(err.Error(), testKey) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(s.log.updateBodyText(), testKey) || strings.Contains(h.logs.String(), testKey) {
		t.Fatal("the key reached the body or the log")
	}
}

// The server answers a toggle on a CDN that is gone with a 401 and an empty
// body. The SDK reads once more to tell that from a rejected key.
func TestToggleOnVanishedCDNIsNotFoundNotAuth(t *testing.T) {
	s := newSim(t, StatusActive)
	s.writeHTTP = http.StatusUnauthorized
	s.detailOverride = func(n int) (string, bool) {
		if n >= 2 {
			return readFixture(t, "webaccelerator-not-found.json"), true
		}
		return "", false
	}
	h, _ := s.harness(t)
	_, err := h.DisableWebAccelerator(context.Background(), &DisableWebAcceleratorInput{CDNID: cdnID})
	if !errors.Is(err, vngcloud.ErrNotFound) || errors.Is(err, vngcloud.ErrAuth) {
		t.Fatalf("err = %v, want ErrNotFound only", err)
	}
	if s.log.count("PUT") != 1 {
		t.Fatalf("toggles = %d", s.log.count("PUT"))
	}

	// With the CDN still there, a 401 stays a key error.
	s = newSim(t, StatusActive)
	s.writeHTTP = http.StatusUnauthorized
	h, _ = s.harness(t)
	_, err = h.DisableWebAccelerator(context.Background(), &DisableWebAcceleratorInput{CDNID: cdnID})
	if !errors.Is(err, vngcloud.ErrAuth) || errors.Is(err, vngcloud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrAuth only", err)
	}
}

func TestUpdateAndDelete401ReadOnceMore(t *testing.T) {
	for _, write := range []writeCall{writeCalls()[0], writeCalls()[1]} {
		for _, tc := range []struct {
			name       string
			detailBody string
			detailHTTP int
			want       error
		}{
			{"still exists", "", 0, vngcloud.ErrAuth},
			{"gone", readFixture(t, "webaccelerator-not-found.json"), 0, vngcloud.ErrNotFound},
			{"second read is unauthorized", "", http.StatusUnauthorized, vngcloud.ErrAuth},
			{"second read rejected", `{"success":false,"code":500,"message":"` + testKey + `","data":{}}`, 0, vngcloud.ErrAuth},
		} {
			t.Run(write.name+" "+tc.name, func(t *testing.T) {
				s := newSim(t, StatusActive)
				s.writeHTTP = http.StatusUnauthorized
				if tc.detailHTTP != 0 {
					s.detailHTTP = func(n int) int {
						if n == 2 {
							return tc.detailHTTP
						}
						return 0
					}
				}
				if tc.detailBody != "" {
					s.detailOverride = func(n int) (string, bool) {
						return tc.detailBody, n == 2
					}
				}
				h, _ := s.harness(t)
				err := write.run(h)
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
				if strings.Contains(err.Error(), testKey) || strings.Contains(h.logs.String(), testKey) {
					t.Fatal("the key reached an error or log")
				}
				if s.reads != 2 || s.log.count(write.method) != 1 {
					t.Fatalf("reads = %d, writes = %d, want 2 and 1", s.reads, s.log.count(write.method))
				}
			})
		}
	}
}

// The recorded refusals map as the design's envelope table says.
func TestRecordedRefusals(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, fixture, code string
		is                  error
		run                 func(h *vcdnHarness) error
	}{
		{"toggle busy", "write-busy.json", "500", ErrBusy, func(h *vcdnHarness) error {
			_, err := h.DisableWebAccelerator(ctx, &DisableWebAcceleratorInput{CDNID: cdnID})
			return err
		}},
		{"delete busy", "write-busy-delete.json", "400", ErrBusy, func(h *vcdnHarness) error {
			_, err := h.DeleteWebAccelerator(ctx, &DeleteWebAcceleratorInput{CDNID: cdnID})
			return err
		}},
		{"update busy", "write-busy-update.json", "500", ErrBusy, func(h *vcdnHarness) error {
			_, err := h.UpdateWebAccelerator(ctx, &UpdateWebAcceleratorInput{CDNID: cdnID, CNames: []string{"a.example.test"}})
			return err
		}},
		{"delete lost a race", "write-not-found.json", "NotFound", vngcloud.ErrNotFound, func(h *vcdnHarness) error {
			_, err := h.DeleteWebAccelerator(ctx, &DeleteWebAcceleratorInput{CDNID: cdnID})
			return err
		}},
		{"package limit", "write-package-refused.json", "500", nil, func(h *vcdnHarness) error {
			_, err := h.UpdateWebAccelerator(ctx, &UpdateWebAcceleratorInput{CDNID: cdnID, CNames: []string{"a.example.test"}})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSim(t, StatusActive)
			s.writeBody = readFixture(t, tc.fixture)
			h, _ := s.harness(t)
			err := tc.run(h)
			if apiError(t, err).Code != tc.code {
				t.Fatalf("err = %v, want code %s", err, tc.code)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("err = %v, want %v", err, tc.is)
			}
		})
	}
}

// The update answer repeats an origin and lacks alwaysHttps, so the Output
// comes from a read of the CDN.
func TestUpdateOutputComesFromADetailRead(t *testing.T) {
	s := newSim(t, StatusActive, StatusDeploying, StatusActive)
	s.writeBody = readFixture(t, "write-update.json")
	h, _ := s.harness(t)
	out, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{
		CDNID: cdnID, SetRuleActions: []RuleActionInput{{Name: "browserCache", Value: "1d"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.WebAccelerator.Upstreams) != 1 || len(out.WebAccelerator.DefaultRuleActions) != 12 {
		t.Fatalf("origins = %d, actions = %d, want 1 and 12", len(out.WebAccelerator.Upstreams), len(out.WebAccelerator.DefaultRuleActions))
	}
}
