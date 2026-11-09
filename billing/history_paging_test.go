package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/transport"
)

func TestHistoryWalk(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		calls := 0
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			page, _ := strconv.Atoi(r.URL.Query().Get("pageNumber"))
			if page != calls {
				t.Error("walk skipped or repeated a page")
			}
			ids := []int{9, 7}
			if page == 2 {
				ids = []int{3}
			}
			_, _ = w.Write([]byte(historyBody(payments, page, 2, 3, ids...)))
		}))
		out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", size: 10, all: true})
		if err != nil || out == nil || calls != 2 || out.Page != 1 || out.PageSize != 3 || out.TotalPage != 1 || out.TotalItem != 3 || len(out.Items) != 3 {
			t.Fatal("clamped complete walk failed")
		}
		for i, id := range []float64{9, 7, 3} {
			if out.Items[i]["ID"] != id {
				t.Fatal("walk changed API order")
			}
		}
		c = newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(historyBody(payments, 1, 50, 0))) }))
		empty, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", all: true})
		if err != nil || empty == nil || empty.Items == nil || empty.PageSize != 0 || empty.TotalPage != 1 {
			t.Fatal("empty aggregate metadata failed")
		}
	})
}

func TestHistoryWalkFailures(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, tc := range []struct {
			name, second string
			code         string
			status       int
		}{
			{"duplicate", historyBody(payments, 2, 2, 3, 1), "HistoryIncomplete", 200},
			{"repeated page", historyBody(payments, 1, 2, 3, 3, 4), "HistoryIncomplete", 200},
			{"changed total", historyBody(payments, 2, 2, 4, 3, 4), "HistoryIncomplete", 200},
			{"changed size", historyBody(payments, 2, 1, 3, 3), "HistoryIncomplete", 200},
			{"short final", historyBody(payments, 2, 2, 3), "HistoryIncomplete", 200},
			{"malformed row", strings.Replace(historyBody(payments, 2, 2, 3, 3), `"description":null`, `"description":12`, 1), "InvalidResponse", 200},
			{"late HTTP", `{"message":"synthetic-account"}`, "Forbidden", 403},
		} {
			t.Run(tc.name, func(t *testing.T) {
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("pageNumber") == "1" {
						_, _ = w.Write([]byte(historyBody(payments, 1, 2, 3, 1, 2)))
						return
					}
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.second))
				}))
				out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", all: true})
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || api.Code != tc.code || errors.Is(err, ErrHistoryIncomplete) != (tc.code == "HistoryIncomplete") {
					t.Fatal("walk failure lost identity or returned partial output")
				}
				if tc.code == "HistoryIncomplete" && (api.Retryable || !errors.Is(err, vngcloud.ErrHistoryIncomplete)) {
					t.Fatal("incomplete sentinel aliases diverged")
				}
			})
		}
	})
}

func TestHistoryPageStructure(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, body := range []string{historyBody(payments, 2, 50, 1, 1), historyBody(payments, 1, 51, 1, 1), historyBody(payments, 1, 2, 3, 1), historyBody(payments, 1, 2, 2, 1, 1), historyBody(payments, 1, 1, 1, 1, 2), strings.Replace(historyBody(payments, 1, 50, 1, 1), `"totalPages":1`, `"totalPages":2`, 1)} {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
			if out != nil || vngcloud.ErrorCode(err) != "InvalidResponse" {
				t.Fatal("malformed page accepted")
			}
		}
	})
}

func TestHistoryPageAndRowBounds(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, tc := range []struct {
			total, size int
			all         bool
			want        string
			calls       int
		}{{101, 1, false, "", 1}, {101, 1, true, "HistoryIncomplete", 1}, {100, 1, true, "", 100}, {5000, 50, true, "", 100}, {5001, 50, true, "HistoryIncomplete", 1}, {int(^uint(0) >> 1), 50, true, "HistoryIncomplete", 1}} {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				page, _ := strconv.Atoi(r.URL.Query().Get("pageNumber"))
				ids := make([]int, tc.size)
				for i := range ids {
					ids[i] = (page-1)*tc.size + i + 1
				}
				_, _ = w.Write([]byte(historyBody(payments, page, tc.size, tc.total, ids...)))
			}))
			out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", size: tc.size, all: tc.all})
			if calls != tc.calls || vngcloud.ErrorCode(err) != tc.want || (out == nil) != (err != nil) {
				t.Fatal("page/row bound failed")
			}
		}
	})
}

// Padding lives inside the JSON value, so it counts toward the walk budget.
func historyPadded(body string, size int) string {
	const suffix = `,"syntheticPadding":""}`
	base := strings.TrimSuffix(body, "}") + suffix
	if size < len(base) {
		panic("synthetic padding size too small")
	}
	return strings.TrimSuffix(base, `"}`) + strings.Repeat("x", size-len(base)) + `"}`
}

func TestHistoryResponseByteBounds(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, status := range []int{200, 403, 503} {
			for _, extra := range []int{0, 1} {
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(historyPadded(historyBody(payments, 1, 50, 0), (8<<20)+extra)))
				}))
				out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
				if status == 200 && extra == 0 {
					if err != nil || out == nil {
						t.Fatal("exact response limit rejected")
					}
					continue
				}
				var api *vngcloud.APIError
				want := "InvalidResponse"
				if status == 403 {
					want = "Forbidden"
				}
				if status == 503 {
					want = "ServerError"
				}
				if out != nil || !errors.As(err, &api) || api.StatusCode != status || api.Code != want || errors.Is(err, transport.ErrBodyTooLarge) != (status == 200) {
					t.Fatal("response byte limit classification failed")
				}
			}
		}
		for _, extra := range []int{0, 1} {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				page, _ := strconv.Atoi(r.URL.Query().Get("pageNumber"))
				// Five successful pages total exactly 32 MiB, or one byte more.
				size := 7 << 20
				if page == 5 {
					size = (4 << 20) + extra
				}
				_, _ = fmt.Fprint(w, " \n", historyPadded(historyBody(payments, page, 1, 5, page), size), "\n ")
			}))
			out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", size: 1, all: true})
			if extra == 0 {
				if err != nil || out == nil || len(out.Items) != 5 {
					t.Fatal("exact cumulative JSON limit rejected")
				}
			} else if out != nil || !errors.Is(err, ErrHistoryIncomplete) {
				t.Fatal("cumulative JSON limit exceeded without failure")
			}
			if calls != 5 {
				t.Fatal("unexpected byte-bound request count")
			}
		}
		// Surrounding whitespace consumes transport bytes, but not the JSON-value budget.
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, " \n", historyBody(payments, 1, 50, 0), "\n ")
		}))
		if _, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", all: true}); err != nil {
			t.Fatal("surrounding whitespace failed")
		}
	})
}

func TestHistoryCanceledBetweenPages(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 2 {
				cancel()
				<-r.Context().Done()
				return
			}
			_, _ = w.Write([]byte(historyBody(payments, 1, 1, 2, 1)))
		}))
		out, err := historyRead(t, ctx, c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", all: true, size: 1})
		if out != nil || !errors.Is(err, context.Canceled) || errors.Is(err, ErrHistoryIncomplete) || calls != 2 {
			t.Fatal("paging cancellation lost identity")
		}
	})
}

func TestHistoryLateEnvelopeErrorKeepsIdentity(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page, _ := strconv.Atoi(r.URL.Query().Get("pageNumber"))
			if page == 5 {
				_, _ = w.Write([]byte(`{"code":400,"data":[{"errorMsg":"synthetic-account"}]}`))
				return
			}
			_, _ = w.Write([]byte(historyPadded(historyBody(payments, page, 1, 5, page), 8<<20)))
		}))
		out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", size: 1, all: true})
		if out != nil || vngcloud.ErrorCode(err) != "400" || errors.Is(err, ErrHistoryIncomplete) {
			t.Fatal("walk byte limit concealed a late API failure")
		}
	})
}

func TestHistoryDuplicatePageIsMalformed(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page, _ := strconv.Atoi(r.URL.Query().Get("pageNumber"))
			_, _ = w.Write([]byte(historyBody(payments, page, 2, 4, 1, 1+map[bool]int{true: 1, false: 0}[page == 1])))
		}))
		out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01", size: 2, all: true})
		if out != nil || vngcloud.ErrorCode(err) != "InvalidResponse" || errors.Is(err, ErrHistoryIncomplete) {
			t.Fatal("duplicate within a page was not malformed")
		}
	})
}
