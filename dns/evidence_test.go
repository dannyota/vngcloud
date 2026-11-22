package dns

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestReplacementRequiresCompleteZone(t *testing.T) {
	for _, body := range []string{
		`{"data":{"hostedZoneId":"zone-1","status":"ACTIVE","description":"old"}}`,
		`{"data":{"hostedZoneId":"zone-1","status":"ACTIVE","description":"old","assocVpcIds":null}}`,
		`{"data":{"hostedZoneId":"zone-1","status":"ACTIVE","assocVpcIds":[]}}`,
		`{"data":{"hostedZoneId":"zone-2","status":"ACTIVE","description":"old","assocVpcIds":[]}}`,
		`{"data":{"hostedZoneId":"zone-1","status":"ACTIVE","description":"old","assocVpcIds":[],"AssocVpcIds":["vpc-2"]}}`,
	} {
		t.Run(body, func(t *testing.T) {
			var writes atomic.Int32
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
					w.WriteHeader(204)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			_, err := c.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{HostedZoneID: "zone-1", Description: vngcloud.Ptr("new"), NoWait: true})
			if err == nil || writes.Load() != 0 {
				t.Fatalf("unsafe replacement: error=%v writes=%d", err, writes.Load())
			}
		})
	}
}

func TestPollRejectsLateSuccess(t *testing.T) {
	start := time.Now()
	now := start
	err := poll(context.Background(), func() time.Time { return now }, contextSleep, func(ctx context.Context) (bool, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("request context has no operation deadline")
		}
		now = start.Add(pollBound)
		return true, nil
	}, func() error { return ErrZoneBusy })
	if !errors.Is(err, ErrZoneBusy) {
		t.Fatalf("late success=%v", err)
	}
}

func TestPollRejectsCanceledCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := poll(ctx, time.Now, contextSleep, func(context.Context) (bool, error) { called = true; return true, nil }, func() error { return ErrZoneBusy })
	if called || !errors.Is(err, context.Canceled) {
		t.Fatalf("called=%v error=%v", called, err)
	}
}

func TestTXTConfirmationIsExact(t *testing.T) {
	in := &UpdateRecordInput{Values: &[]RecordValue{{Value: "OLD"}}}
	if recordMatchesUpdate(&Record{Type: "TXT", Value: []RecordValue{{Value: "old"}}}, &HostedZone{}, in) {
		t.Fatal("TXT payload case change falsely confirmed")
	}
}

func TestRecordsReportIncompletePaging(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"listData":[{"recordId":"record-1"}],"page":1,"pageSize":1,"totalPage":2,"totalItem":2}`))
	}))
	out, err := c.ListRecords(context.Background(), &ListRecordsInput{HostedZoneID: "zone-1"})
	if err == nil || out == nil || out.TotalPage != 2 {
		t.Fatalf("incomplete paging: output=%v error=%v", out, err)
	}
}

func TestZoneReplacementSendsOnceAndRetainsUncertainty(t *testing.T) {
	for _, status := range []int{307, 308, 401, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var writes atomic.Int32
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, zoneBody(StatusActive, "old", []string{"vpc-1"}))
					return
				}
				writes.Add(1)
				w.Header().Set("Location", "/replayed")
				w.WriteHeader(status)
			})))
			out, err := c.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{HostedZoneID: "zone-1", Description: vngcloud.Ptr("new")})
			if writes.Load() != 1 || err == nil {
				t.Fatalf("replacement replayed: writes=%d error=%v", writes.Load(), err)
			}
			if status != 401 && (out == nil || !errors.Is(err, ErrNotSettled)) {
				t.Fatalf("uncertain replacement lost: output=%v error=%v", out, err)
			}
		})
	}
}

func TestPollingRequestStopsOnCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- poll(ctx, time.Now, contextSleep, func(ctx context.Context) (bool, error) { close(started); <-ctx.Done(); return true, nil }, func() error { return ErrZoneBusy })
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled read succeeded: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled polling request ignored cancellation")
	}
}
