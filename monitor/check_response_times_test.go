package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
)

func TestCheckResponseTimesValidation(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	for _, in := range []*ListCheckResponseTimesInput{nil, {}, {CheckID: "../x"}, {CheckID: "chk-1", Start: "bad"}, {CheckID: "chk-1", Start: "2000-01-01T00:00:00Z", End: "2000-01-02T00:00:00.001Z"}} {
		out, err := c.ListCheckResponseTimes(context.Background(), in)
		if out != nil || !errors.Is(err, core.ErrInvalidInput) {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
}

const responseSnapshot = `{"id":"chk-1","subtype":"HTTP","history_alarm":"alarm-1","locations":["loc-1","loc-2"]}`
const responseGroup = `{"name":"syntt.http.response_time","dimensions":{"location_id":"loc-1"},"statistics":[{"key":"2000-01-01T01:00:00Z","value":"12.5"}]}`

func responseClient(t *testing.T, snapshot, body string, check func(*http.Request)) *Client {
	t.Helper()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vmonitor-uptime-manager/v1/uptimes/chk-1" {
			_, _ = w.Write([]byte(snapshot))
			return
		}
		if r.URL.Path != "/vmonitor-api/api/v1/statistics/synthetics" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if check != nil {
			check(r)
		}
		_, _ = w.Write([]byte(body))
	}))
	c.now = func() time.Time { return time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC) }
	return c
}

func TestCheckResponseTimesWindowQueryAndSorting(t *testing.T) {
	body := `[` + strings.ReplaceAll(responseGroup, "loc-1", "loc-2") + `,` + strings.Replace(responseGroup, `[{"key"`, `[{"key":"2000-01-01T02:00:00Z","value":"3"},{"key"`, 1) + `]`
	c := responseClient(t, responseSnapshot, body, func(r *http.Request) {
		want := url.Values{"name": {responseMetric}, "dimensions": {"syntt_id:chk-1"}, "statistics": {"avg"}, "start_time": {"946684800000"}, "end_time": {"946771200000"}, "group_by": {"location_id"}, "alarm": {"alarm-1"}}
		if r.URL.RawQuery != want.Encode() {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
	})
	for _, in := range []*ListCheckResponseTimesInput{{CheckID: "chk-1"}, {CheckID: "chk-1", Start: "2000-01-01T07:00:00+07:00", End: "2000-01-02T07:00:00+07:00"}} {
		out, err := c.ListCheckResponseTimes(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Items) != 2 || out.Items[0].LocationID != "loc-1" || len(out.Items[0].Points) != 2 || !out.Items[0].Points[0].Timestamp.Before(out.Items[0].Points[1].Timestamp) || out.Start.Location() != time.UTC || out.End.Sub(out.Start) != 24*time.Hour {
			t.Fatalf("output = %+v", out)
		}
	}
}

func TestCheckResponseTimesRejectsMalformed(t *testing.T) {
	tests := map[string]string{
		"null": "null", "object": "{}", "missing-name": `[{"dimensions":{"location_id":"loc-1"},"statistics":[]}]`,
		"wrong-name":         `[` + strings.Replace(responseGroup, responseMetric, "wrong", 1) + `]`,
		"duplicate-name":     `[` + strings.Replace(responseGroup, `"name":`, `"name":"other","name":`, 1) + `]`,
		"foreign":            `[` + strings.ReplaceAll(responseGroup, "loc-1", "foreign") + `]`,
		"duplicate-location": `[` + responseGroup + `,` + responseGroup + `]`,
		"duplicate-bucket":   `[` + strings.Replace(responseGroup, `}]}`, `},{"key":"2000-01-01T01:00:00Z","value":"1"}]}`, 1) + `]`,
		"bad-time":           `[` + strings.Replace(responseGroup, "2000-01-01T01:00:00Z", "bad", 1) + `]`,
		"bad-value":          `[` + strings.Replace(responseGroup, "12.5", "-1", 1) + `]`,
		"number-value":       `[` + strings.Replace(responseGroup, `"12.5"`, `12.5`, 1) + `]`,
		"null-statistics":    `[` + strings.Replace(responseGroup, `[{"key":"2000-01-01T01:00:00Z","value":"12.5"}]`, `null`, 1) + `]`,
		"after-end":          `[` + strings.Replace(responseGroup, "2000-01-01T01:00:00Z", "2000-01-02T00:00:01Z", 1) + `]`,
		"too-early":          `[` + strings.Replace(responseGroup, "2000-01-01T01:00:00Z", "1999-12-31T23:54:59Z", 1) + `]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			out, err := responseClient(t, responseSnapshot, body, nil).ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("out=%v err=%v", out, err)
			}
		})
	}
}

func TestCheckResponseTimesSnapshotAndEmpty(t *testing.T) {
	tests := []struct{ snapshot, code string }{{strings.Replace(responseSnapshot, "HTTP", "PING", 1), "InvalidInput"}, {strings.Replace(responseSnapshot, "alarm-1", "", 1), "NotFound"}, {strings.Replace(responseSnapshot, "alarm-1", "../bad", 1), "InvalidResponse"}, {strings.Replace(responseSnapshot, `"loc-2"`, `"loc-1"`, 1), "InvalidResponse"}}
	for _, tc := range tests {
		out, err := responseClient(t, tc.snapshot, `[]`, nil).ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
		var api *core.APIError
		if out != nil || !errors.As(err, &api) || api.Code != tc.code {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
	out, err := responseClient(t, responseSnapshot, `[]`, nil).ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
	if err != nil || out.Items == nil || len(out.Items) != 0 {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestCheckResponseTimesEarlyBucket(t *testing.T) {
	early := strings.Replace(responseGroup, "2000-01-01T01:00:00Z", "1999-12-31T23:55:00Z", 1)
	for _, tc := range []struct {
		body, start string
		good        bool
	}{{`[` + early + `]`, "", true}, {`[` + early + `]`, "2000-01-01T00:00:01Z", false}, {`[` + strings.Replace(early, `}]}`, `},{"key":"1999-12-31T23:56:00Z","value":"1"}]}`, 1) + `]`, "", false}} {
		out, err := responseClient(t, responseSnapshot, tc.body, nil).ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1", Start: tc.start})
		if (err == nil) != tc.good || (out != nil) != tc.good {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
}

func TestCheckResponseTimesLimits(t *testing.T) {
	locations := make([]string, 65)
	groups := make([]string, 65)
	for i := range locations {
		locations[i] = fmt.Sprintf(`"loc-%d"`, i)
		groups[i] = fmt.Sprintf(`{"name":"syntt.http.response_time","dimensions":{"location_id":"loc-%d"},"statistics":[]}`, i)
	}
	snapshot := `{"id":"chk-1","subtype":"HTTP","history_alarm":"alarm-1","locations":[` + strings.Join(locations, ",") + `]}`
	var points strings.Builder
	for i := range 100000 {
		fmt.Fprintf(&points, `{"key":"2000-01-01T01:00:00.%09dZ","value":"1"},`, i)
	}
	tests := []struct{ snapshot, body string }{{snapshot, `[` + strings.Join(groups, ",") + `]`}, {responseSnapshot, `[` + responseGroup + `]` + strings.Repeat(" ", 16<<20)}, {responseSnapshot, `[{"name":"syntt.http.response_time","dimensions":{"location_id":"loc-1"},"statistics":[` + points.String() + `{}]}]`}}
	for _, tc := range tests {
		out, err := responseClient(t, tc.snapshot, tc.body, nil).ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
		var api *core.APIError
		if out != nil || !errors.As(err, &api) || api.Code != "ResponseLimitExceeded" {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
}

func TestCheckResponseTimesFixture(t *testing.T) {
	c := responseClient(t, responseSnapshot, checkAlarmFixture(t, "ListCheckResponseTimes"), nil)
	out, err := c.ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
	if err != nil || len(out.Items) != 1 || len(out.Items[0].Points) != 1 || out.Items[0].Points[0].ValueMillis != 12.5 {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestCheckResponseTimesRefreshReflection(t *testing.T) {
	for _, pair := range [][2]string{{"12.5", "synthetic-new"}, {"synthetic-old", "loc-2"}} {
		t.Run(pair[0], func(t *testing.T) {
			target := "/vmonitor-api/api/v1/statistics/synthetics"
			c := monitorRefreshClient(t, pair[0], pair[1], target, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == target {
					_, _ = w.Write([]byte(`[` + responseGroup + `]`))
					return
				}
				_, _ = w.Write([]byte(responseSnapshot))
			}))
			c.now = func() time.Time { return time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC) }
			out, err := c.ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("out=%v err=%v", out, err)
			}
			assertMonitorCredentialsAbsent(t, out, err, pair[0], pair[1])
		})
	}
}
