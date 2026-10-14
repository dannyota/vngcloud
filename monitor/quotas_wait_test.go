package monitor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func emailFakeClock(c *Client) *time.Time {
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		now = now.Add(d)
		return nil
	}
	return &now
}

func TestCreateNotificationQuotaSettlement404CreatingActive(t *testing.T) {
	s := &emailOrderServer{send: freeEmailSend(t), quota: func(n int32) string {
		if n <= 3 {
			return ""
		}
		if n == 4 {
			return emailSnapshot(t, "CREATING", "p")
		}
		return emailSnapshot(t, "ACTIVE", "p")
	}}
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	emailFakeClock(c)
	out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
	if err != nil || out == nil || out.Quota == nil || out.Quota.Status != "ACTIVE" || s.mutations.Load() != 1 || s.reads.Load() != 5 {
		t.Fatalf("out=%+v err=%v reads=%d", out, err, s.reads.Load())
	}
}

func TestCreateNotificationQuotaSettlementBoundAndPackage(t *testing.T) {
	for _, status := range []string{"CREATING", "ACTIVE"} {
		t.Run(status, func(t *testing.T) {
			s := &emailOrderServer{send: freeEmailSend(t), quota: func(n int32) string {
				if n <= 2 {
					return ""
				}
				return emailSnapshot(t, status, "other")
			}}
			c := New(testutil.NewRetryConfig(t, s.handler(t)))
			now := emailFakeClock(c)
			out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
			if out == nil || out.Quota != nil || !errors.Is(err, dns.ErrNotSettled) || now.Sub(time.Unix(0, 0)) > 120*time.Second || s.mutations.Load() != 1 {
				t.Fatalf("out=%+v err=%v elapsed=%v", out, err, *now)
			}
		})
	}
}

func TestCreateNotificationQuotaSettlementReadFailureAndCancellation(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		t.Run(map[bool]string{false: "read error", true: "cancel"}[cancelWait], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &emailOrderServer{send: freeEmailSend(t)}
			handler := s.handler(t)
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == emailQuotaPath && s.mutations.Load() > 0 {
					if cancelWait {
						cancel()
						<-r.Context().Done()
					} else {
						w.WriteHeader(403)
						_, _ = io.WriteString(w, `{"message":"private-canary"}`)
					}
					return
				}
				handler(w, r)
			})))
			out, err := c.CreateNotificationQuota(ctx, &CreateNotificationQuotaInput{Kind: "Email"})
			if out == nil || !errors.Is(err, dns.ErrNotSettled) {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if cancelWait && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if !cancelWait {
				var api *core.APIError
				if !errors.As(err, &api) || api.StatusCode != 403 {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestQuotaSettlementHardDeadlineDuringRead(t *testing.T) {
	c := New(testutil.NewConfig(t, http.NotFoundHandler()))
	seen := false
	_, err := c.waitQuotaAttempt(context.Background(), "monitor.test", func(ctx context.Context) (quotaAttemptResolution, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 120*time.Second {
			t.Error("missing hard deadline")
		}
		seen = true
		// A short injected bound proves that a blocked read is interrupted.
		<-ctx.Done()
		return quotaAttemptResolution{}, ctx.Err()
	}, 20*time.Millisecond)
	if !seen || !errors.Is(err, dns.ErrNotSettled) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("seen=%v err=%v", seen, err)
	}
}

func TestQuotaAttemptMatchingChangedAndDifferentOperation(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching", true: "different"}[different], func(t *testing.T) {
			active := atomic.Bool{}
			s := &emailOrderServer{send: freeEmailSend(t), quota: func(n int32) string {
				if active.Load() {
					return emailSnapshot(t, "ACTIVE", "p")
				}
				return ""
			}}
			c := New(testutil.NewRetryConfig(t, s.handler(t)))
			_, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", NoWait: true})
			if err != nil {
				t.Fatal(err)
			}
			active.Store(true)
			pkg := "p"
			if different {
				pkg = "other"
			}
			out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", PackageID: pkg})
			if different {
				if out != nil || !errors.Is(err, core.ErrInvalidInput) {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			} else if err != nil || out == nil || out.Quota == nil {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if s.mutations.Load() != 1 || len(c.quotaAttempts) != 0 {
				t.Fatal("attempt not reconciled")
			}
		})
	}
	c := New(testutil.NewConfig(t, http.NotFoundHandler()))
	prior := quotaAttemptKey{operation: "monitor.RenewNotificationQuota", kind: "email", packageID: "p", quotaID: "quota-1", monthPeriod: 1, expectedEndTime: time.Unix(1, 0)}
	c.quotaAttempts = map[string]*quotaAttempt{"email": {key: prior, reconcile: func(context.Context) (quotaAttemptResolution, error) {
		return quotaAttemptResolution{confirmed: true}, nil
	}}}
	result, err, handled := c.reconcileQuotaAttempt(context.Background(), quotaAttemptKey{operation: "monitor.CreateNotificationQuota", kind: "email"})
	if !handled || result != nil || !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("result=%v err=%v handled=%v", result, err, handled)
	}
}

func TestQuotaAttemptInconclusiveAndKnownRejection(t *testing.T) {
	c := New(testutil.NewConfig(t, http.NotFoundHandler()))
	key := quotaAttemptKey{operation: "monitor.test", kind: "email", packageID: "p", monthPeriod: 1}
	rejection := errors.New("known rejection")
	for _, rejected := range []error{nil, rejection} {
		c.quotaAttempts = map[string]*quotaAttempt{"email": {key: key, reconcile: func(context.Context) (quotaAttemptResolution, error) {
			return quotaAttemptResolution{rejected: rejected}, nil
		}}}
		_, err, handled := c.reconcileQuotaAttempt(context.Background(), key)
		if !handled {
			t.Fatal("attempt ignored")
		}
		if rejected == nil {
			if !errors.Is(err, dns.ErrNotSettled) || len(c.quotaAttempts) != 1 {
				t.Fatal(err)
			}
		} else if !errors.Is(err, rejection) || len(c.quotaAttempts) != 0 {
			t.Fatal(err)
		}
	}
}

func TestQuotaWriteLockCancellationAndHeldThroughSettlement(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s := &emailOrderServer{send: freeEmailSend(t), quota: func(n int32) string {
		if n <= 2 {
			return ""
		}
		close(entered)
		<-release
		return emailSnapshot(t, "ACTIVE", "p")
	}}
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	finished := make(chan error, 1)
	go func() {
		_, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
		finished <- err
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := c.CreateNotificationQuota(ctx, &CreateNotificationQuotaInput{Kind: "Email"})
	if out != nil || !errors.Is(err, context.Canceled) || s.reads.Load() != 3 || s.mutations.Load() != 1 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestQuotaAttemptExpiryIdentityUsesInstants(t *testing.T) {
	c := New(testutil.NewConfig(t, http.NotFoundHandler()))
	end := time.Unix(1, 0).UTC()
	key := quotaAttemptKey{operation: "monitor.RenewNotificationQuota", kind: "email", packageID: "p", monthPeriod: 1, quotaID: "quota-1", expectedEndTime: end}
	c.quotaAttempts = map[string]*quotaAttempt{"email": {key: key, reconcile: func(context.Context) (quotaAttemptResolution, error) {
		return quotaAttemptResolution{confirmed: true}, nil
	}}}
	key.expectedEndTime = end.In(time.FixedZone("offset", 3600))
	_, err, handled := c.reconcileQuotaAttempt(context.Background(), key)
	if err != nil || !handled {
		t.Fatalf("err=%v handled=%v", err, handled)
	}
}

func TestQuotaWriteConcurrentCallsSendOneMutation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s := &emailOrderServer{send: freeEmailSend(t), quota: func(n int32) string {
		if n <= 2 {
			return ""
		}
		if n == 3 {
			close(entered)
			<-release
		}
		return emailSnapshot(t, "ACTIVE", "p")
	}}
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() {
		_, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
		first <- err
	}()
	<-entered
	go func() {
		_, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
		second <- err
	}()
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; !errors.Is(err, core.ErrInvalidInput) {
		t.Fatal(err)
	}
	if s.mutations.Load() != 1 {
		t.Fatalf("sends=%d", s.mutations.Load())
	}
}

func TestQuotaWriteLockWaitCancellation(t *testing.T) {
	var lock quotaWriteLock
	if err := lock.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() { close(started); done <- lock.acquire(ctx) }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	lock.release()
	if err := lock.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	lock.release()
}

func TestQuotaAttemptReadErrorNeverClearsRecord(t *testing.T) {
	c := New(testutil.NewConfig(t, http.NotFoundHandler()))
	key := quotaAttemptKey{operation: "monitor.test", kind: "email", packageID: "p", monthPeriod: 1}
	c.quotaAttempts = map[string]*quotaAttempt{"email": {key: key, reconcile: func(context.Context) (quotaAttemptResolution, error) {
		return quotaAttemptResolution{}, core.ErrNotFound
	}}}
	_, err, handled := c.reconcileQuotaAttempt(context.Background(), key)
	if !handled || !errors.Is(err, dns.ErrNotSettled) || !errors.Is(err, core.ErrNotFound) || len(c.quotaAttempts) != 1 {
		t.Fatalf("err=%v handled=%v", err, handled)
	}
}

func TestQuotaAttemptRenewalBaselineAndOperationReconciliation(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		for _, operation := range []string{"monitor.RenewNotificationQuota", "monitor.CreateNotificationQuota"} {
			t.Run(operation+map[bool]string{false: " inconclusive", true: " confirmed"}[confirmed], func(t *testing.T) {
				c := New(testutil.NewConfig(t, http.NotFoundHandler()))
				prior := quotaAttemptKey{operation: "monitor.RenewNotificationQuota", kind: "email", packageID: "p", monthPeriod: 1, quotaID: "quota-1", expectedEndTime: time.Unix(1, 0)}
				reads := 0
				c.recordQuotaAttempt(&quotaAttempt{key: prior, reconcile: func(context.Context) (quotaAttemptResolution, error) {
					reads++
					return quotaAttemptResolution{confirmed: confirmed}, nil
				}})
				changed := prior
				changed.operation = operation
				changed.expectedEndTime = time.Unix(2, 0)
				_, err, handled := c.reconcileQuotaAttempt(context.Background(), changed)
				if !handled || reads != 1 {
					t.Fatal("prior attempt was not read first")
				}
				if confirmed {
					if !errors.Is(err, core.ErrInvalidInput) || len(c.quotaAttempts) != 0 {
						t.Fatal(err)
					}
				} else if !errors.Is(err, dns.ErrNotSettled) || len(c.quotaAttempts) != 1 {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestQuotaSettlementContextHasHardBoundAndCallerLimit(t *testing.T) {
	for _, callerLimited := range []bool{false, true} {
		ctx := context.Background()
		cancel := func() {}
		if callerLimited {
			ctx, cancel = context.WithTimeout(ctx, time.Second)
		}
		c := New(testutil.NewConfig(t, http.NotFoundHandler()))
		_, err := c.waitQuotaAttempt(ctx, "monitor.test", func(child context.Context) (quotaAttemptResolution, error) {
			deadline, ok := child.Deadline()
			if !ok {
				t.Fatal("read has no deadline")
			}
			remaining := time.Until(deadline)
			if callerLimited {
				if remaining > time.Second {
					t.Fatal("caller deadline not honored")
				}
			} else if remaining > 120*time.Second || remaining < 119*time.Second {
				t.Fatalf("wait bound=%v", remaining)
			}
			return quotaAttemptResolution{confirmed: true}, nil
		}, quotaSettlementBound)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCreateNotificationQuotaSettlementRefreshScrubsRetainedOrder(t *testing.T) {
	const first, second = "synthetic-token-A", "synthetic-token-B"
	for _, noWait := range []bool{false, true} {
		t.Run(map[bool]string{false: "wait", true: "reconcile"}[noWait], func(t *testing.T) {
			s := &emailOrderServer{send: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"orderId":`+jsonString(second)+`,"paymentUrl":`+jsonString(emailRedirect)+`}`)
			}, quota: func(n int32) string {
				if n <= 2 {
					return ""
				}
				return changedEmailFixture(t, map[string]any{"name": first + " " + second, "packageId": "p"})
			}}
			handler := s.handler(t)
			refreshes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if s.mutations.Load() > 0 && r.Method == http.MethodGet {
					refreshes++
					if refreshes == 1 {
						w.WriteHeader(401)
						return
					}
				}
				handler(w, r)
			}))
			defer server.Close()
			source := &monitorRefreshTokens{first: first, second: second}
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source})))
			emailFakeClock(c)
			in := &CreateNotificationQuotaInput{Kind: "Email", NoWait: noWait}
			out, err := c.CreateNotificationQuota(context.Background(), in)
			if noWait {
				if err != nil {
					t.Fatal(err)
				}
				assertMonitorCredentialsAbsent(t, out, err, first)
				in.NoWait = false
				out, err = c.CreateNotificationQuota(context.Background(), in)
			}
			if err != nil || out == nil || out.Quota == nil || s.mutations.Load() != 1 || refreshes != 2 || source.calls != 2 {
				t.Fatalf("out=%v err=%v sends=%d refreshes=%d token calls=%d", out, err, s.mutations.Load(), refreshes, source.calls)
			}
			assertMonitorCredentialsAbsent(t, out, err, first, second)
		})
	}
}

func TestQuotaAttemptCredentialLimitWithholdsRetainedOutput(t *testing.T) {
	const first, second = "synthetic-token-A", "synthetic-token-B"
	c := monitorRefreshClient(t, first, second, emailQuotaPath, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, emailSnapshot(t, "ACTIVE", "p"))
	}))
	credentials := make(monitorReadCredentials, 256)
	for i := range credentials {
		credentials[i] = jsonNumber(float64(i)) + "-synthetic-credential"
	}
	terms := notificationCreateTerms{kind: "email", packageID: "p", monthPeriod: 1}
	attempt := &quotaAttempt{
		key:         quotaAttemptKey{operation: "monitor.CreateNotificationQuota", kind: "email", packageID: "p", monthPeriod: 1},
		credentials: &credentials,
		result:      quotaWriteResult{orderID: first},
		reconcile:   c.notificationCreateReconciler("monitor.CreateNotificationQuota", terms, &credentials),
	}
	c.recordQuotaAttempt(attempt)
	out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", PackageID: "p"})
	if out == nil || out.Quota != nil || !errors.Is(err, dns.ErrNotSettled) || len(credentials) != 256 {
		t.Fatalf("out=%v err=%v credentials=%d", out, err, len(credentials))
	}
	assertMonitorCredentialsAbsent(t, out, err, first, second)
	assertMonitorCredentialsAbsent(t, createQuotaOutput(&attempt.result), nil, first, second)
}
