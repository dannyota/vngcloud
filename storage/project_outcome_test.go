package storage

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestProjectOrderRefusalClassification(t *testing.T) {
	for _, tc := range []struct {
		status    int
		body      string
		uncertain bool
	}{
		{503, `{"success":false,"code":503}`, true},
		{401, `{}`, true}, {429, `{}`, true},
		{401, `not JSON`, true},
		{401, `{"success":false}`, true},
		{429, `{"success":false,"code":""}`, true},
		{200, `{"success":false,"code":""}`, true},
		{400, `{"success":false}`, true},
		{400, `{"success":false,"code":403}`, false},
		{200, `{"success":false,"code":403}`, false},
		{200, `{"success":false}`, true},
	} {
		s := &projectWriteServer{status: tc.status, order: tc.body}
		c := newTestClient(t, s.handler(t))
		_, err := c.CreateProject(context.Background(), validProjectCreate())
		var api *core.APIError
		if !errors.As(err, &api) || api.StatusCode != tc.status || errors.Is(err, ErrNotSettled) != tc.uncertain || s.orders != 1 {
			t.Fatalf("status %d: error %v orders %d", tc.status, err, s.orders)
		}
		if tc.status == 401 && !errors.Is(err, core.ErrAuth) {
			t.Fatal("HTTP auth sentinel lost")
		}
		if tc.status == 429 && !errors.Is(err, core.ErrRateLimited) {
			t.Fatal("HTTP rate sentinel lost")
		}
		if tc.uncertain && !strings.Contains(err.Error(), projectOrderRecovery) {
			t.Fatal("missing recovery")
		}
	}
}

func TestProjectUnknownOrderCannotConfirm(t *testing.T) {
	for _, data := range []string{`{}`, `{"redirectUrl":""}`, `{"unexpected":true}`} {
		s := &projectWriteServer{order: `{"success":true,"data":` + data + `}`, after: projectList(newProjectJSON)}
		c := newTestClient(t, s.handler(t))
		out, err := c.CreateProject(context.Background(), validProjectCreate())
		if !errors.Is(err, ErrNotSettled) || errors.Is(err, ErrPaymentRequired) || out.Project != nil || s.lists != 2 {
			t.Fatalf("output %+v error %v", out, err)
		}
	}
}

func TestProjectConfirmationRejectsLateRead(t *testing.T) {
	for _, bound := range []time.Duration{projectCreateBound, projectDeleteBound} {
		c := newTestClient(t, (&projectWriteServer{}).handler(t))
		elapsed := fakeProjectClock(c)
		err := c.pollProject(context.Background(), bound, func(ctx context.Context) (bool, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("confirmation read has no deadline")
			}
			*elapsed += bound + time.Second
			return true, nil
		}, func() error { return context.DeadlineExceeded })
		if err == nil {
			t.Fatal("late read returned success")
		}
	}
}

func TestProjectDeleteDebugRedactsID(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := &projectWriteServer{before: projectList(newProjectJSON)}
	c := New(testutil.NewConfigWithLogger(t, s.handler(t), logger))
	_, err := c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
	if err != nil || strings.Contains(log.String(), "new-1") {
		t.Fatalf("delete error %v; debug leaked identity: %v", err, strings.Contains(log.String(), "new-1"))
	}
	if !strings.Contains(log.String(), "DELETE") || strings.Count(log.String(), "path=[redacted]") != 2 {
		t.Fatal("debug omitted redacted requests")
	}
}

func TestProjectSlowConfirmationReads(t *testing.T) {
	for _, create := range []bool{true, false} {
		s := &projectWriteServer{after: projectList(newProjectJSON)}
		bound := projectCreateBound
		if !create {
			s.before = projectList(newProjectJSON)
			s.after = projectList("")
			bound = projectDeleteBound
		}
		h := s.handler(t)
		var elapsed time.Duration
		now := time.Unix(0, 0)
		tr := transport.New(transport.Config{HTTPClient: &http.Client{Transport: projectRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "storage.invalid" {
				t.Fatal("unexpected network host")
			}
			if r.Body != nil {
				defer func() { _ = r.Body.Close() }()
			}
			if r.URL.Path == "/internal/v1/projects" && s.orders+s.deletes > 0 {
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > bound+time.Second {
					t.Fatal("confirmation deadline missing or too long")
				}
				elapsed += bound + time.Second
			}
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, r)
			return recorder.Result(), nil
		})}})
		c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Storage: "https://storage.invalid/"}, tr))
		c.now = func() time.Time { return now.Add(elapsed) }
		c.sleep = func(context.Context, time.Duration) error { t.Fatal("late confirmation should stop"); return nil }
		var err error
		if create {
			_, err = c.CreateProject(context.Background(), validProjectCreate())
		} else {
			_, err = c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
		}
		if !errors.Is(err, ErrNotSettled) || s.orders+s.deletes != 1 {
			t.Fatalf("error %v", err)
		}
	}
}

func TestProjectNoWaitPendingCheckout(t *testing.T) {
	s := &projectWriteServer{after: projectList(strings.Replace(newProjectJSON, `"status":1`, `"status":2`, 1))}
	c := newTestClient(t, s.handler(t))
	in := validProjectCreate()
	in.NoWait = true
	out, err := c.CreateProject(context.Background(), in)
	if err != nil || out.Project == nil || out.Project.Status != 2 || s.lists != 2 || s.orders != 1 {
		t.Fatalf("output %+v error %v", out, err)
	}
}
