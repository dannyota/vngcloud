package dns

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

// TestUpdateHostedZoneUnknownZonePropagatesNotFound checks that the
// pre-write wait's own read failure (an unknown zone) returns at once as a
// plain NotFound error, rather than being treated as busy and polled.
func TestUpdateHostedZoneUnknownZonePropagatesNotFound(t *testing.T) {
	var getCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected %s request", r.Method)
		}
		getCalls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	})))

	_, err := client.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{
		HostedZoneID: "zone-x",
		Description:  vngcloud.Ptr("new"),
	})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
	if errors.Is(err, ErrZoneBusy) {
		t.Fatal("err wraps ErrZoneBusy, want the read's own NotFound unwrapped")
	}
	if getCalls.Load() != 1 {
		t.Fatalf("GET calls = %d, want 1: a read failure must not be polled", getCalls.Load())
	}
}

// TestDeleteHostedZoneUnknownZonePropagatesNotFound mirrors
// TestUpdateHostedZoneUnknownZonePropagatesNotFound for DeleteHostedZone.
func TestDeleteHostedZoneUnknownZonePropagatesNotFound(t *testing.T) {
	var getCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected %s request", r.Method)
		}
		getCalls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	})))

	_, err := client.DeleteHostedZone(context.Background(), &DeleteHostedZoneInput{HostedZoneID: "zone-x"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
	if getCalls.Load() != 1 {
		t.Fatalf("GET calls = %d, want 1: a read failure must not be polled", getCalls.Load())
	}
}

// --- CreateHostedZone's post-write wait ---

func TestCreateHostedZoneSettlesToActive(t *testing.T) {
	var getCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, scriptedGets(t, []string{
		zoneBody(StatusCreating, "d", []string{"vpc-1"}),
		zoneBody(StatusCreating, "d", []string{"vpc-1"}),
		zoneBody(StatusActive, "d", []string{"vpc-1"}),
	}, func(w http.ResponseWriter, r *http.Request) {
		getCalls.Add(1)
		_, _ = w.Write([]byte(`{"data":{"hostedZoneId":"zone-1","status":"CREATING"}}`))
	})))

	out, err := client.CreateHostedZone(context.Background(), &CreateHostedZoneInput{
		DomainName: "example.internal",
		VPCIDs:     []string{"vpc-1"},
	})
	if err != nil {
		t.Fatalf("CreateHostedZone() error = %v", err)
	}
	if out.HostedZone.Status != StatusActive {
		t.Fatalf("Status = %q, want %q", out.HostedZone.Status, StatusActive)
	}
}

func TestCreateHostedZoneErrFailed(t *testing.T) {
	client := withInstantSleep(newTestClient(t, scriptedGets(t, []string{
		zoneBody(StatusCreating, "d", []string{"vpc-1"}),
		zoneBody(StatusError, "d", []string{"vpc-1"}),
	}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"hostedZoneId":"zone-1","status":"CREATING"}}`))
	})))

	out, err := client.CreateHostedZone(context.Background(), &CreateHostedZoneInput{
		DomainName: "example.internal",
		VPCIDs:     []string{"vpc-1"},
	})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.HostedZone.Status != StatusError {
		t.Fatalf("out = %+v, want a non-nil Output holding the ERROR zone", out)
	}
}

func TestCreateHostedZoneErrNotSettled(t *testing.T) {
	client := withInstantSleep(newTestClient(t, scriptedGets(t, []string{
		zoneBody(StatusCreating, "d", []string{"vpc-1"}),
	}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"hostedZoneId":"zone-1","status":"CREATING"}}`))
	})))

	out, err := client.CreateHostedZone(context.Background(), &CreateHostedZoneInput{
		DomainName: "example.internal",
		VPCIDs:     []string{"vpc-1"},
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || out.HostedZone.Status != StatusCreating {
		t.Fatalf("out = %+v, want a non-nil Output holding the last CREATING read", out)
	}
}

// --- UpdateHostedZone's pre- and post-write waits ---

// TestUpdateHostedZonePreWriteWaitThroughBusyToActive checks both that the
// pre-write wait carries an update through busy reads to the ready one, and
// that the merge uses that last read, not an earlier busy one: the busy
// reads and the ready read carry different descriptions, the update leaves
// Description unset so the merge must pick it up from whichever read
// waitZoneReady returns, and the PUT handler asserts it is the ready read's
// "ready", not the busy reads' "busy".
func TestUpdateHostedZonePreWriteWaitThroughBusyToActive(t *testing.T) {
	var putCalls atomic.Int64
	handler := scriptedGets(t, []string{
		zoneBody(StatusUpdating, "busy", []string{"vpc-1"}),
		zoneBody(StatusUpdating, "busy", []string{"vpc-1"}),
		zoneBody(StatusActive, "ready", []string{"vpc-1"}),
		// The confirm read after the PUT below settles at once.
		zoneBody(StatusActive, "ready", []string{"vpc-2"}),
	}, func(w http.ResponseWriter, r *http.Request) {
		putCalls.Add(1)
		body := decodeBody(t, r)
		if body["description"] != "ready" {
			t.Fatalf("PUT description = %v, want %q from the pre-write wait's last read, not an earlier busy one", body["description"], "ready")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	client := withInstantSleep(newTestClient(t, handler))

	_, err := client.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{
		HostedZoneID: "zone-1",
		VPCIDs:       vngcloud.Ptr([]string{"vpc-2"}),
	})
	if err != nil {
		t.Fatalf("UpdateHostedZone() error = %v", err)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
}

func TestUpdateHostedZonePreWriteWaitAcceptsError(t *testing.T) {
	var putCalls atomic.Int64
	handler := scriptedGets(t, []string{
		zoneBody(StatusError, "d", []string{"vpc-1"}),
		zoneBody(StatusActive, "new", []string{"vpc-1"}),
	}, func(w http.ResponseWriter, r *http.Request) {
		putCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	client := withInstantSleep(newTestClient(t, handler))

	_, err := client.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{
		HostedZoneID: "zone-1",
		Description:  vngcloud.Ptr("new"),
	})
	if err != nil {
		t.Fatalf("UpdateHostedZone() error = %v", err)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1: ERROR is an accepted pre-write status", putCalls.Load())
	}
}

func TestUpdateHostedZoneErrZoneBusyNoWriteSent(t *testing.T) {
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected %s request: the pre-write wait never settled, nothing should be sent", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(zoneBody(StatusUpdating, "d", []string{"vpc-1"})))
	})))

	_, err := client.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{
		HostedZoneID: "zone-1",
		Description:  vngcloud.Ptr("new"),
	})
	if !errors.Is(err, ErrZoneBusy) {
		t.Fatalf("err = %v, want ErrZoneBusy", err)
	}
}

func TestUpdateHostedZonePostWriteErrFailed(t *testing.T) {
	handler := scriptedGets(t, []string{
		zoneBody(StatusActive, "d", []string{"vpc-1"}),
		zoneBody(StatusError, "d", []string{"vpc-1"}),
	}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	client := withInstantSleep(newTestClient(t, handler))

	out, err := client.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{
		HostedZoneID: "zone-1",
		Description:  vngcloud.Ptr("new"),
	})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.HostedZone.Status != StatusError {
		t.Fatalf("out = %+v, want a non-nil Output holding the ERROR zone", out)
	}
}

func TestUpdateHostedZonePostWriteErrNotSettled(t *testing.T) {
	handler := scriptedGets(t, []string{
		zoneBody(StatusActive, "d", []string{"vpc-1"}),
		// Every read after the PUT shows the old description forever: the
		// sent fields never appear, so the wait can only time out.
		zoneBody(StatusActive, "d", []string{"vpc-1"}),
	}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	client := withInstantSleep(newTestClient(t, handler))

	out, err := client.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{
		HostedZoneID: "zone-1",
		Description:  vngcloud.Ptr("new"),
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil {
		t.Fatal("out is nil, want a non-nil Output holding the last read")
	}
}

// --- DeleteHostedZone's pre- and post-write waits ---

func TestDeleteHostedZonePreWriteWaitThroughBusyToActive(t *testing.T) {
	var deleteCalls atomic.Int64
	handler := scriptedGets(t, []string{
		zoneBody(StatusCreating, "d", []string{"vpc-1"}),
		zoneBody(StatusActive, "d", []string{"vpc-1"}),
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("unexpected method %s", r.Method)
		}
		deleteCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	client := withInstantSleep(newTestClient(t, handler))

	if _, err := client.DeleteHostedZone(context.Background(), &DeleteHostedZoneInput{HostedZoneID: "zone-1", NoWait: true}); err != nil {
		t.Fatalf("DeleteHostedZone() error = %v", err)
	}
	if deleteCalls.Load() != 1 {
		t.Fatalf("DELETE calls = %d, want 1", deleteCalls.Load())
	}
}

func TestDeleteHostedZoneErrZoneBusyNoWriteSent(t *testing.T) {
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected %s request: the pre-write wait never settled, nothing should be sent", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(zoneBody(StatusCreating, "d", []string{"vpc-1"})))
	})))

	_, err := client.DeleteHostedZone(context.Background(), &DeleteHostedZoneInput{HostedZoneID: "zone-1"})
	if !errors.Is(err, ErrZoneBusy) {
		t.Fatalf("err = %v, want ErrZoneBusy", err)
	}
}

func TestDeleteHostedZoneSettlesOnNotFound(t *testing.T) {
	getN := 0
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getN++
			if getN <= 2 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	if _, err := client.DeleteHostedZone(context.Background(), &DeleteHostedZoneInput{HostedZoneID: "zone-1"}); err != nil {
		t.Fatalf("DeleteHostedZone() error = %v", err)
	}
}

func TestDeleteHostedZoneErrNotSettled(t *testing.T) {
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// The zone never goes away within the wait's bound.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	_, err := client.DeleteHostedZone(context.Background(), &DeleteHostedZoneInput{HostedZoneID: "zone-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

// TestZoneWritesSerializeWithinOneClient checks that writeLock keeps two
// goroutines from ever running a write's pre-write-read-through-write
// sequence concurrently on one Client: each DeleteHostedZone call below
// holds the handler busy for a moment, and inFlight would catch either
// call starting while the other is still inside it.
func TestZoneWritesSerializeWithinOneClient(t *testing.T) {
	var inFlight atomic.Int32
	var overlapped atomic.Bool
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inFlight.Add(1) > 1 {
			overlapped.Store(true)
		}
		// A real, short sleep, not the injected wait clock: it gives the
		// other goroutine's request a chance to reach this handler while
		// this one is still "in flight", which is exactly what writeLock must
		// prevent for two writes sharing one Client.
		time.Sleep(5 * time.Millisecond)
		inFlight.Add(-1)

		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	var wg sync.WaitGroup
	for _, id := range []string{"zone-1", "zone-2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := client.DeleteHostedZone(context.Background(), &DeleteHostedZoneInput{HostedZoneID: id, NoWait: true}); err != nil {
				t.Errorf("DeleteHostedZone(%s) error = %v", id, err)
			}
		}(id)
	}
	wg.Wait()

	if overlapped.Load() {
		t.Fatal("two writes overlapped; writeLock did not serialize them")
	}
}

// --- Cancellation after a write already succeeded ---

// TestCreateHostedZoneCancelDuringSettleSleep checks that a ctx canceled
// while the post-create wait sleeps between polls, after the create's own
// POST already succeeded, still returns a non-nil Output carrying the new
// zone's id, and an error wrapping both ErrNotSettled and the
// cancellation itself, never a bare ctx.Err() that would lose the id.
func TestCreateHostedZoneCancelDuringSettleSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			testutil.WriteFixture(t, w, "../testdata/dns/create_hosted_zone.json")
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusCreating, "d", []string{"vpc-1"})))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	client.sleep = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}

	out, err := client.CreateHostedZone(ctx, &CreateHostedZoneInput{
		DomainName: "example.internal",
		VPCIDs:     []string{"vpc-1"},
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if out == nil || out.HostedZone.ID != "zone-1" {
		t.Fatalf("out = %+v, want a non-nil Output carrying the zone id", out)
	}
}

// TestCreateHostedZoneCancelDuringFirstSettleRead checks the same guarantee
// as TestCreateHostedZoneCancelDuringSettleSleep, but for a ctx canceled
// during the very first settle read, before any read after the create ever
// succeeds: the Output must then fall back to the create response itself,
// which still carries the new zone's id, rather than going nil.
func TestCreateHostedZoneCancelDuringFirstSettleRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gotFirstGet := make(chan struct{})
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			testutil.WriteFixture(t, w, "../testdata/dns/create_hosted_zone.json")
		case http.MethodGet:
			close(gotFirstGet)
			<-ctx.Done() // released by the goroutine below, once it cancels ctx
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	go func() {
		<-gotFirstGet
		cancel()
	}()

	out, err := client.CreateHostedZone(ctx, &CreateHostedZoneInput{
		DomainName: "example.internal",
		VPCIDs:     []string{"vpc-1"},
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if out == nil || out.HostedZone.ID != "zone-1" {
		t.Fatalf("out = %+v, want a non-nil Output carrying the zone id from the create response", out)
	}
}

// TestUpdateHostedZoneNoWaitConfirmReadFailure checks that a failure of
// NoWait's own single confirm read, after the PUT has already succeeded,
// wraps ErrNotSettled and still returns a non-nil Output carrying the
// zone's id, rather than the bare read error over a nil Output.
func TestUpdateHostedZoneNoWaitConfirmReadFailure(t *testing.T) {
	putDone := false
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if !putDone {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
				return
			}
			// The confirm read after the PUT fails outright.
			w.WriteHeader(http.StatusInternalServerError)
		case http.MethodPut:
			putDone = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	out, err := client.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{
		HostedZoneID: "zone-1",
		Description:  vngcloud.Ptr("new"),
		NoWait:       true,
	})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || out.HostedZone.ID != "zone-1" {
		t.Fatalf("out = %+v, want a non-nil Output carrying the zone id", out)
	}
}

// TestDeleteHostedZoneReadFailureAfterDeleteWrapsNotSettled checks that a
// read failure other than NotFound, after the DELETE has already
// succeeded, wraps ErrNotSettled rather than propagating the bare read
// error, and still returns a non-nil Output.
func TestDeleteHostedZoneReadFailureAfterDeleteWrapsNotSettled(t *testing.T) {
	// The first read is the pre-write wait, which must succeed with ACTIVE
	// so the DELETE gets sent; every read after that fails outright rather
	// than ever reporting NotFound.
	first := true
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if first {
				first = false
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	out, err := client.DeleteHostedZone(context.Background(), &DeleteHostedZoneInput{HostedZoneID: "zone-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil {
		t.Fatal("out is nil, want a non-nil Output")
	}
}

// --- writeLock honors ctx ---

// TestWriteLockRespectsCanceledContext checks that a caller waiting for
// writeLock gives up as soon as its own ctx ends, instead of blocking until
// the current holder releases it, and that the lock still works normally
// for a later caller once the original holder does release it.
func TestWriteLockRespectsCanceledContext(t *testing.T) {
	gotGet := make(chan struct{})
	release := make(chan struct{})
	var gotGetOnce sync.Once
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			gotGetOnce.Do(func() { close(gotGet) })
			<-release
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	holderDone := make(chan struct{})
	go func() {
		defer close(holderDone)
		if _, err := client.DeleteHostedZone(context.Background(), &DeleteHostedZoneInput{HostedZoneID: "zone-1", NoWait: true}); err != nil {
			t.Errorf("holder DeleteHostedZone() error = %v", err)
		}
	}()

	<-gotGet // the holder now has writeLock and is blocked in its pre-write read.

	waiterCtx, waiterCancel := context.WithCancel(context.Background())
	waiterCancel() // already done before the waiter ever tries to acquire the lock.
	if _, err := client.DeleteHostedZone(waiterCtx, &DeleteHostedZoneInput{HostedZoneID: "zone-2", NoWait: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter err = %v, want context.Canceled: lock acquisition must honor ctx", err)
	}

	close(release)
	<-holderDone

	// The lock must still be usable: a third call proceeds normally now
	// that the holder released it.
	if _, err := client.DeleteHostedZone(context.Background(), &DeleteHostedZoneInput{HostedZoneID: "zone-3", NoWait: true}); err != nil {
		t.Fatalf("third call error = %v", err)
	}
}
