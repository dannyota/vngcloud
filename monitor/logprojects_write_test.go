package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// withInstantSleep replaces client's sleep and now with fakes that never
// really wait, so a test exercising a wait's full bound runs in
// milliseconds rather than the real pollInterval and bound. The fake clock
// advances by exactly the duration each sleep call is asked to wait, so a
// wait's bound is still reached after the same number of iterations a real
// clock would take.
func withInstantSleep(client *Client) *Client {
	clock := time.Now()
	client.now = func() time.Time { return clock }
	client.sleep = func(ctx context.Context, d time.Duration) error {
		clock = clock.Add(d)
		return ctx.Err()
	}
	return client
}

func decodeLogProjectBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(data) == 0 {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode body: %v, raw = %s", err, data)
	}
	return body
}

// freeQuoteBody is a created-price response priced at 0, standing in for a
// live Basic quote (the design records a live 0 VND Basic quote).
const freeQuoteBody = `{"optimumPrice":0,"originalPrice":0,"discountPrice":0,"discountPercent":null,"propertiesPrice":[]}`

// --- CreateLogProject ---

// TestCreateLogProjectOrderUsesSharedBuilderBody checks the order POST's
// body is exactly what buildLogProjectOrderBody produces for the same
// Input, per ADR 0002 rule 8.
func TestCreateLogProjectOrderUsesSharedBuilderBody(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v2/log/quota-class":
			testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjectClasses.json")
		case "/billing-api/v2/log/prices/created-price":
			testutil.WriteFixture(t, w, "../testdata/monitor/QuoteCreateLogProject.json")
		case "/billing-api/v2/log/quotas":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			body := decodeLogProjectBody(t, r)
			if body["packageId"] != "pkg-pro-7d" || body["quantity"] != 140.0 {
				t.Fatalf("unexpected order body: %+v", body)
			}
			if body["projectName"] != "app" || body["pay"] != true || body["monthPeriod"] != 1.0 {
				t.Fatalf("unexpected order body: %+v", body)
			}
			// The order endpoint refuses any other redirectUrl with a 400.
			if body["redirectUrl"] != "https://vmonitor.console.vngcloud.vn/quota-usages/log" {
				t.Fatalf("redirectUrl = %v", body["redirectUrl"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"amount":917000,"orderId":"order-1","paymentUrl":""}`))
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	}))

	out, err := client.CreateLogProject(context.Background(), &CreateLogProjectInput{
		Name: "app", Class: LogProjectClassPro, RetentionDays: 7, GBPerDay: 20,
		MaxPrice: 917000, NoWait: true,
	})
	if err != nil {
		t.Fatalf("CreateLogProject() error = %v", err)
	}
	if out.OrderID != "order-1" {
		t.Fatalf("OrderID = %q, want order-1", out.OrderID)
	}
}

// TestCreateLogProjectRefusesAboveMaxPrice checks the quote runs first, and
// an order priced above MaxPrice is refused with ErrPriceAboveMax and no
// order sent.
func TestCreateLogProjectRefusesAboveMaxPrice(t *testing.T) {
	var orderCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v2/log/quota-class":
			testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjectClasses.json")
		case "/billing-api/v2/log/prices/created-price":
			testutil.WriteFixture(t, w, "../testdata/monitor/QuoteCreateLogProject.json")
		case "/billing-api/v2/log/quotas":
			orderCalls.Add(1)
			t.Fatal("order must not be sent when the quote exceeds MaxPrice")
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	}))

	_, err := client.CreateLogProject(context.Background(), &CreateLogProjectInput{
		Name: "app", Class: LogProjectClassPro, RetentionDays: 7, GBPerDay: 20,
	})
	if !errors.Is(err, ErrPriceAboveMax) {
		t.Fatalf("CreateLogProject() error = %v, want ErrPriceAboveMax", err)
	}
	if orderCalls.Load() != 0 {
		t.Fatalf("order calls = %d, want 0", orderCalls.Load())
	}
}

// TestCreateLogProjectDefaultMaxPriceOrdersOnlyFree checks
// CreateLogProjectInput{Name: "app"}'s default MaxPrice of 0 orders a quote
// priced at 0, per the design.
func TestCreateLogProjectDefaultMaxPriceOrdersOnlyFree(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v2/log/quota-class":
			testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjectClasses.json")
		case "/billing-api/v2/log/prices/created-price":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(freeQuoteBody))
		case "/billing-api/v2/log/quotas":
			body := decodeLogProjectBody(t, r)
			if body["packageId"] != "pkg-basic-1d" || body["quantity"] != 10.0 {
				t.Fatalf("unexpected order body: %+v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"amount":0,"orderId":"order-1","paymentUrl":""}`))
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	}))

	out, err := client.CreateLogProject(context.Background(), &CreateLogProjectInput{Name: "app", NoWait: true})
	if err != nil {
		t.Fatalf("CreateLogProject() error = %v", err)
	}
	if out.OrderID != "order-1" {
		t.Fatalf("OrderID = %q, want order-1", out.OrderID)
	}
}

// TestCreateLogProjectOrderNotRetriedAfter502 checks the order POST is not
// retried after a 5xx, per ADR 0002 rule 2: a POST is not idempotent unless
// marked so, and CreateLogProject never marks its order idempotent.
func TestCreateLogProjectOrderNotRetriedAfter502(t *testing.T) {
	var orderCalls atomic.Int64
	client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v2/log/quota-class":
			testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjectClasses.json")
		case "/billing-api/v2/log/prices/created-price":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(freeQuoteBody))
		case "/billing-api/v2/log/quotas":
			orderCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	})))

	_, err := client.CreateLogProject(context.Background(), &CreateLogProjectInput{Name: "app", NoWait: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if orderCalls.Load() != 1 {
		t.Fatalf("order calls = %d, want 1 (no retry after the 502)", orderCalls.Load())
	}
}

// TestCreateLogProjectNoWaitSkipsWait checks NoWait returns the order
// response's OrderID at once, with no ListLogProjects call to find a
// project by name: the order response itself carries no project id, name,
// or status (see CreateLogProject's doc comment), so LogProject stays at
// its zero value.
func TestCreateLogProjectNoWaitSkipsWait(t *testing.T) {
	var listCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v2/log/quota-class":
			testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjectClasses.json")
		case "/billing-api/v2/log/prices/created-price":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(freeQuoteBody))
		case "/billing-api/v2/log/quotas":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"amount":0,"orderId":"order-1","paymentUrl":""}`))
		case "/log-api/v1/projects":
			listCalls.Add(1)
			t.Fatal("NoWait must not list projects")
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	}))

	out, err := client.CreateLogProject(context.Background(), &CreateLogProjectInput{Name: "app", NoWait: true})
	if err != nil {
		t.Fatalf("CreateLogProject() error = %v", err)
	}
	if out.OrderID != "order-1" {
		t.Fatalf("OrderID = %q, want order-1", out.OrderID)
	}
	if out.LogProject != (LogProject{}) {
		t.Fatalf("LogProject = %+v, want zero value: NoWait never fills it", out.LogProject)
	}
	if listCalls.Load() != 0 {
		t.Fatalf("list calls = %d, want 0", listCalls.Load())
	}
}

// logProjectListPage builds one ListLogProjects page envelope holding a
// single project, or none when name is "".
func logProjectListPage(name, status string) string {
	if name == "" {
		return `{"content":[],"currentPage":0,"pageSize":100,"totalElements":0,"totalPages":0}`
	}
	return fmt.Sprintf(
		`{"content":[{"id":"proj-1","name":%q,"status":%q}],"currentPage":0,"pageSize":100,"totalElements":1,"totalPages":1}`,
		name, status)
}

// TestCreateLogProjectWaitFindsActiveByName checks the post-order wait
// looks the new project up by name and settles once it reads ACTIVE.
func TestCreateLogProjectWaitFindsActiveByName(t *testing.T) {
	var listCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v2/log/quota-class":
			testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjectClasses.json")
		case "/billing-api/v2/log/prices/created-price":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(freeQuoteBody))
		case "/billing-api/v2/log/quotas":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"amount":0,"orderId":"order-9","paymentUrl":""}`))
		case "/log-api/v1/projects":
			n := listCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			switch n {
			case 1:
				_, _ = w.Write([]byte(logProjectListPage("", "")))
			case 2:
				_, _ = w.Write([]byte(logProjectListPage("app", "CREATING")))
			default:
				_, _ = w.Write([]byte(logProjectListPage("app", LogProjectStatusActive)))
			}
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	})))

	out, err := client.CreateLogProject(context.Background(), &CreateLogProjectInput{Name: "app"})
	if err != nil {
		t.Fatalf("CreateLogProject() error = %v", err)
	}
	if out.LogProject.ID != "proj-1" || out.LogProject.Status != LogProjectStatusActive {
		t.Fatalf("unexpected project: %+v", out.LogProject)
	}
	if out.OrderID != "order-9" {
		t.Fatalf("OrderID = %q, want order-9", out.OrderID)
	}
	if listCalls.Load() != 3 {
		t.Fatalf("list calls = %d, want 3", listCalls.Load())
	}
}

// TestCreateLogProjectWaitTimesOut checks the wait gives up after its bound
// and wraps dns.ErrNotSettled, per the design's reuse of the vDNS
// sentinels, when the project never reaches ACTIVE.
func TestCreateLogProjectWaitTimesOut(t *testing.T) {
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v2/log/quota-class":
			testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjectClasses.json")
		case "/billing-api/v2/log/prices/created-price":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(freeQuoteBody))
		case "/billing-api/v2/log/quotas":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case "/log-api/v1/projects":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(logProjectListPage("app", "CREATING")))
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	})))

	_, err := client.CreateLogProject(context.Background(), &CreateLogProjectInput{Name: "app"})
	if !errors.Is(err, dns.ErrNotSettled) {
		t.Fatalf("CreateLogProject() error = %v, want dns.ErrNotSettled", err)
	}
}

// TestLogProjectOrderResponseOrderIDDecodesStringOrNumber checks OrderID
// accepts either shape an unconfirmed field might arrive in, the same as
// Alarm.ID: a live free order returned orderId empty or null, and if the
// API ever sends a numeric orderId instead, it must not fail the whole
// response's decode.
func TestLogProjectOrderResponseOrderIDDecodesStringOrNumber(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"string", `{"amount":0,"orderId":"order-1","paymentUrl":""}`, "order-1"},
		{"number", `{"amount":0,"orderId":42,"paymentUrl":""}`, "42"},
		{"null", `{"amount":0,"orderId":null,"paymentUrl":""}`, ""},
		{"empty string", `{"amount":0,"orderId":"","paymentUrl":""}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp logProjectOrderResponse
			if err := json.Unmarshal([]byte(tt.raw), &resp); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if resp.OrderID != tt.want {
				t.Fatalf("OrderID = %q, want %q", resp.OrderID, tt.want)
			}
		})
	}
}

func TestCreateLogProjectMissingName(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a missing Name")
	}))
	if _, err := client.CreateLogProject(context.Background(), &CreateLogProjectInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("CreateLogProject() error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.CreateLogProject(context.Background(), nil); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("CreateLogProject(nil) error = %v, want ErrInvalidInput", err)
	}
}

// --- DeleteLogProject ---

// TestDeleteLogProjectSendsDeleteThenPurge checks Purge sends the trash
// delete before the purge delete, never the reverse, and never the purge
// alone.
func TestDeleteLogProjectSendsDeleteThenPurge(t *testing.T) {
	var order []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s", r.Method)
		}
		order = append(order, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))

	_, err := client.DeleteLogProject(context.Background(), &DeleteLogProjectInput{LogProjectID: "proj-1", Purge: true, NoWait: true})
	if err != nil {
		t.Fatalf("DeleteLogProject() error = %v", err)
	}
	want := []string{"/billing-api/v1/log/quotas/proj-1", "/billing-api/v1/trash/log/quotas/proj-1"}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
		t.Fatalf("request order = %v, want %v", order, want)
	}
}

// TestDeleteLogProjectPurgeSkippedWhenDeleteFails checks a failed delete
// never sends the purge.
func TestDeleteLogProjectPurgeSkippedWhenDeleteFails(t *testing.T) {
	var purgeCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/billing-api/v1/trash/log/quotas/proj-1" {
			purgeCalls.Add(1)
			t.Fatal("purge must not be sent when the delete failed")
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))

	_, err := client.DeleteLogProject(context.Background(), &DeleteLogProjectInput{LogProjectID: "proj-1", Purge: true, NoWait: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if purgeCalls.Load() != 0 {
		t.Fatalf("purge calls = %d, want 0", purgeCalls.Load())
	}
}

// TestDeleteLogProjectPurgeTolerantOfAlreadyTrashed checks Purge still
// purges when the trash delete 404s, since that most likely means the
// project already sits in trash from an earlier call.
func TestDeleteLogProjectPurgeTolerantOfAlreadyTrashed(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v1/log/quotas/proj-1":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case "/billing-api/v1/trash/log/quotas/proj-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	}))

	_, err := client.DeleteLogProject(context.Background(), &DeleteLogProjectInput{LogProjectID: "proj-1", Purge: true, NoWait: true})
	if err != nil {
		t.Fatalf("DeleteLogProject() error = %v", err)
	}
}

// TestDeleteLogProjectPlainDeleteSurfacesNotFound checks a plain delete (no
// Purge) still returns NotFound as usual when the project is already gone,
// unlike the Purge-tolerant case above.
func TestDeleteLogProjectPlainDeleteSurfacesNotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := client.DeleteLogProject(context.Background(), &DeleteLogProjectInput{LogProjectID: "proj-1", NoWait: true})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("DeleteLogProject() error = %v, want ErrNotFound", err)
	}
}

func TestDeleteLogProjectMissingID(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a missing LogProjectID")
	}))
	if _, err := client.DeleteLogProject(context.Background(), &DeleteLogProjectInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("DeleteLogProject() error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.DeleteLogProject(context.Background(), nil); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("DeleteLogProject(nil) error = %v, want ErrInvalidInput", err)
	}
}

func TestDeleteLogProjectPathIDRejection(t *testing.T) {
	for _, id := range []string{"..", ".", "/", ""} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("unexpected request for a rejected LogProjectID")
			}))
			_, err := client.DeleteLogProject(context.Background(), &DeleteLogProjectInput{LogProjectID: id})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("DeleteLogProject(%q) error = %v, want ErrInvalidInput", id, err)
			}
		})
	}
}

// TestDeleteLogProjectNoWaitSendsNoBaselineRead checks NoWait skips the
// pre-delete baseline read entirely: only the delete (and, with Purge, the
// purge) DELETE requests are sent.
func TestDeleteLogProjectNoWaitSendsNoBaselineRead(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("unexpected %s request to %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	_, err := client.DeleteLogProject(context.Background(), &DeleteLogProjectInput{LogProjectID: "proj-1", NoWait: true})
	if err != nil {
		t.Fatalf("DeleteLogProject() error = %v", err)
	}
}

// TestDeleteLogProjectWaitSettlesOnChangedStatus checks the post-write wait
// settles once a read's Status or BillingStatus differs from the
// pre-delete baseline, one of the two ways the design's settle condition
// ("Get is 404, or the project is in trash") can be observed.
func TestDeleteLogProjectWaitSettlesOnChangedStatus(t *testing.T) {
	var getCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := getCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if n == 1 {
				// Pre-delete baseline read.
				_, _ = w.Write([]byte(`{"id":"proj-1","status":"ACTIVE","billingStatus":"PAID"}`))
				return
			}
			if n == 2 {
				// Still unchanged: not yet trashed.
				_, _ = w.Write([]byte(`{"id":"proj-1","status":"ACTIVE","billingStatus":"PAID"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"proj-1","status":"ACTIVE","billingStatus":"TRASHED"}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected %s request", r.Method)
		}
	})))

	_, err := client.DeleteLogProject(context.Background(), &DeleteLogProjectInput{LogProjectID: "proj-1"})
	if err != nil {
		t.Fatalf("DeleteLogProject() error = %v", err)
	}
	if getCalls.Load() != 3 {
		t.Fatalf("get calls = %d, want 3 (baseline, one unsettled poll, the settling poll)", getCalls.Load())
	}
}

// TestDeleteLogProjectWaitSettlesOnNotFound checks the wait also settles
// when a poll read 404s, the other half of the design's settle condition.
func TestDeleteLogProjectWaitSettlesOnNotFound(t *testing.T) {
	var getCalls atomic.Int64
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			n := getCalls.Add(1)
			if n == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"proj-1","status":"ACTIVE","billingStatus":"PAID"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected %s request", r.Method)
		}
	})))

	_, err := client.DeleteLogProject(context.Background(), &DeleteLogProjectInput{LogProjectID: "proj-1"})
	if err != nil {
		t.Fatalf("DeleteLogProject() error = %v", err)
	}
}

// TestDeleteLogProjectWaitTimesOut checks the wait wraps dns.ErrNotSettled
// once its 60-second bound elapses without a 404 or a changed read.
func TestDeleteLogProjectWaitTimesOut(t *testing.T) {
	client := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"proj-1","status":"ACTIVE","billingStatus":"PAID"}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected %s request", r.Method)
		}
	})))

	_, err := client.DeleteLogProject(context.Background(), &DeleteLogProjectInput{LogProjectID: "proj-1"})
	if !errors.Is(err, dns.ErrNotSettled) {
		t.Fatalf("DeleteLogProject() error = %v, want dns.ErrNotSettled", err)
	}
}

// TestDeleteLogProjectBaselineNotFoundWithoutPurgeStaysError checks a
// baseline read that 404s without Purge still returns the ordinary
// not-found error, and sends no delete request: unlike the Purge case
// below, there is no later request to tolerate the 404 for.
func TestDeleteLogProjectBaselineNotFoundWithoutPurgeStaysError(t *testing.T) {
	var deleteCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case http.MethodDelete:
			deleteCalls.Add(1)
			t.Fatal("delete must not be sent when the baseline read 404s without Purge")
		default:
			t.Fatalf("unexpected %s request", r.Method)
		}
	}))

	_, err := client.DeleteLogProject(context.Background(), &DeleteLogProjectInput{LogProjectID: "proj-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("DeleteLogProject() error = %v, want ErrNotFound", err)
	}
	if deleteCalls.Load() != 0 {
		t.Fatalf("delete calls = %d, want 0", deleteCalls.Load())
	}
}

// TestDeleteLogProjectPurgeToleratesGoneBaseline checks that when Purge is
// set and the pre-delete baseline read itself 404s, the project is already
// gone from the live list (seen live for a free project, gone from trash
// within about a second of an earlier delete): DeleteLogProject still
// sends the delete and the purge, tolerating a 404 from either, and
// returns success at once, with no settle wait, since there is no baseline
// left to wait against.
func TestDeleteLogProjectPurgeToleratesGoneBaseline(t *testing.T) {
	var getCalls, deleteCalls, purgeCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			getCalls.Add(1)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/billing-api/v1/log/quotas/proj-1":
			deleteCalls.Add(1)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/billing-api/v1/trash/log/quotas/proj-1":
			purgeCalls.Add(1)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		default:
			t.Fatalf("unexpected %s request to %s", r.Method, r.URL.Path)
		}
	}))

	_, err := client.DeleteLogProject(context.Background(), &DeleteLogProjectInput{LogProjectID: "proj-1", Purge: true})
	if err != nil {
		t.Fatalf("DeleteLogProject() error = %v", err)
	}
	if getCalls.Load() != 1 {
		t.Fatalf("get calls = %d, want 1 (baseline only, no settle wait)", getCalls.Load())
	}
	if deleteCalls.Load() != 1 || purgeCalls.Load() != 1 {
		t.Fatalf("delete calls = %d, purge calls = %d, want 1 each", deleteCalls.Load(), purgeCalls.Load())
	}
}
