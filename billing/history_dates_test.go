package billing

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestHistoryDateBoundaries(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, tc := range []struct{ start, end, wire string }{{"2024-02-28", "2024-02-29", "01/03/2024"}, {"2024-01-31", "2024-01-31", "01/02/2024"}, {"2024-12-31", "2024-12-31", "01/01/2025"}} {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("dateEnd") != tc.wire {
					t.Error("incorrect exclusive date bound")
				}
				_, _ = w.Write([]byte(historyBody(payments, 1, 50, 0)))
			}))
			in := historyOptions{start: tc.start, end: tc.end}
			before := in
			if _, err := historyRead(t, context.Background(), c, payments, &in); err != nil {
				t.Fatal("date transition failed")
			}
			if !reflect.DeepEqual(before, in) {
				t.Fatal("caller input mutated")
			}
		}
	})
}

func TestHistoryTodayResolvedOnce(t *testing.T) {
	previousNow, previousLocal := historyNow, time.Local
	t.Cleanup(func() { historyNow = previousNow; time.Local = previousLocal })
	time.Local = time.FixedZone("synthetic host", -11*60*60)
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		reads := 0
		historyNow = func() time.Time {
			reads++
			return time.Date(2024, 12, 31, 17, 0, 0, 0, time.UTC).Add(time.Duration(reads-1) * 24 * time.Hour)
		}
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("dateEnd") != "02/01/2025" {
				t.Error("today used host time or changed between pages")
			}
			page := 1
			if r.URL.Query().Get("pageNumber") == "2" {
				page = 2
			}
			_, _ = w.Write([]byte(historyBody(payments, page, 1, 2, page)))
		}))
		out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-12-31", all: true, size: 1})
		if err != nil || out == nil || reads != 1 || len(out.Items) != 2 {
			t.Fatal("date range was not frozen for the invocation")
		}
	})
}
