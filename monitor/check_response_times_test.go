package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
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
	}{
		{`[` + early + `]`, "", true},
		{`[` + strings.Replace(early, "23:55:00Z", "23:56:00Z", 1) + `]`, "2000-01-01T00:00:01Z", false},
		{`[` + strings.Replace(early, `}]}`, `},{"key":"1999-12-31T23:56:00Z","value":"1"}]}`, 1) + `]`, "", false},
	} {
		out, err := responseClient(t, responseSnapshot, tc.body, nil).ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1", Start: tc.start})
		if tc.good {
			if err != nil || out == nil {
				t.Fatalf("out=%v err=%v", out, err)
			}
		} else {
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("out=%v err=%v", out, err)
			}
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
	for _, pair := range [][2]string{{"12.5", "synthetic-new"}, {"synthetic-old", "12.5"}} {
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

func TestCheckResponseTimesBlocksRefreshedAlarmCredential(t *testing.T) {
	source := &monitorRefreshTokens{first: "synthetic-first", second: "alarm-1"}
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vmonitor-uptime-manager/v1/uptimes/chk-1" {
			_, _ = w.Write([]byte(responseSnapshot))
			return
		}
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source})))
	c.now = func() time.Time { return time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC) }
	out, err := c.ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
	var api *core.APIError
	if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || attempts != 1 || source.calls != 2 {
		t.Fatalf("out=%v err=%v attempts=%d token calls=%d", out, err, attempts, source.calls)
	}
	assertMonitorCredentialsAbsent(t, out, err, "synthetic-first", "alarm-1")
}

func TestCheckResponseTimesStrictInputTime(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	for _, in := range []*ListCheckResponseTimesInput{{CheckID: "chk-1", Start: "2000-01-01T1:00:00Z"}, {CheckID: "chk-1", End: "2000-01-01T1:00:00Z"}, {CheckID: "chk-1", Start: "2000-01-01T00:00:00+24:00"}} {
		out, err := c.ListCheckResponseTimes(context.Background(), in)
		if out != nil || !errors.Is(err, core.ErrInvalidInput) {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
}

func TestCheckResponseTimesStrictBucketTime(t *testing.T) {
	for _, key := range []string{"2000-01-01T1:00:00Z", "2000-01-01T01:00:00,5Z", "2000-01-01T01:00:00+00:60"} {
		body := `[` + strings.Replace(responseGroup, "2000-01-01T01:00:00Z", key, 1) + `]`
		out, err := responseClient(t, responseSnapshot, body, nil).ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
		var api *core.APIError
		if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
}

func TestCheckResponseTimesMalformedNonHTTP(t *testing.T) {
	snapshot := strings.ReplaceAll(strings.ReplaceAll(responseSnapshot, "HTTP", "PING"), "alarm-1", "../bad")
	out, err := responseClient(t, snapshot, `[]`, nil).ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
	var api *core.APIError
	if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestCheckResponseTimesExplicitEnd(t *testing.T) {
	calls := 0
	c := responseClient(t, responseSnapshot, `[]`, func(r *http.Request) {
		calls++
		if r.URL.Query().Get("start_time") != "946728000000" || r.URL.Query().Get("end_time") != "946814400000" {
			t.Errorf("query=%v", r.URL.Query())
		}
	})
	out, err := c.ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1", End: "2000-01-02T12:00:00Z"})
	if err != nil || out == nil || calls != 1 || !out.End.Equal(time.Date(2000, 1, 2, 12, 0, 0, 0, time.UTC)) || !out.Start.Equal(time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("out=%v err=%v calls=%d", out, err, calls)
	}
}

func TestCheckResponseTimesZeroMillisecondWindow(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	out, err := c.ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1", Start: "2000-01-01T00:00:00.0001Z", End: "2000-01-01T00:00:00.0009Z"})
	if out != nil || !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestCheckResponseTimesMalformedSnapshotFields(t *testing.T) {
	cases := map[string]string{"wrong-id": strings.Replace(responseSnapshot, `"chk-1"`, `"chk-2"`, 1), "missing-subtype": strings.Replace(responseSnapshot, `"subtype":"HTTP",`, "", 1)}
	for name, snapshot := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := responseClient(t, snapshot, `[]`, func(*http.Request) { t.Error("unexpected statistics request") }).ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("out=%v err=%v", out, err)
			}
		})
	}
}

func TestCheckResponseTimesRejectsWrongFieldCase(t *testing.T) {
	for _, field := range []string{"name", "statistics"} {
		t.Run(field, func(t *testing.T) {
			wrong := strings.ToUpper(field[:1]) + field[1:]
			body := `[` + strings.Replace(responseGroup, `"`+field+`":`, `"`+wrong+`":`, 1) + `]`
			out, err := responseClient(t, responseSnapshot, body, nil).ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("out=%v err=%v", out, err)
			}
		})
	}
}

func TestCheckResponseTimesHTTPStatus(t *testing.T) {
	for _, target := range []string{"/vmonitor-uptime-manager/v1/uptimes/chk-1", "/vmonitor-api/api/v1/statistics/synthetics"} {
		t.Run(target, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == target {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"message":"withheld-text"}`))
					return
				}
				if r.URL.Path == "/vmonitor-uptime-manager/v1/uptimes/chk-1" {
					_, _ = w.Write([]byte(responseSnapshot))
					return
				}
				t.Errorf("unexpected request %s", r.URL.Path)
			}))
			out, err := c.ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.StatusCode != http.StatusForbidden || api.Code != "Forbidden" || !errors.Is(err, core.ErrPermission) || strings.Contains(err.Error(), "withheld-text") {
				t.Fatalf("out=%v err=%v", out, err)
			}
		})
	}
}

func TestCheckResponseTimesSnapshotBodyLimit(t *testing.T) {
	snapshot := responseSnapshot + strings.Repeat(" ", 1<<20)
	c := responseClient(t, snapshot, `[]`, func(*http.Request) { t.Error("unexpected statistics request") })
	out, err := c.ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
	var api *core.APIError
	if out != nil || !errors.As(err, &api) || api.Code != "ResponseLimitExceeded" {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestCheckResponseTimesPointLimitAcrossSeries(t *testing.T) {
	var body strings.Builder
	body.WriteString(`[{"name":"syntt.http.response_time","dimensions":{"location_id":"loc-1"},"statistics":[`)
	for i := range 50001 {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"key":"2000-01-01T01:00:00.%09dZ","value":"1"}`, i)
	}
	body.WriteString(`]},{"name":"syntt.http.response_time","dimensions":{"location_id":"loc-2"},"statistics":[`)
	for i := range 50000 {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"key":"2000-01-01T01:00:00.%09dZ","value":"1"}`, i)
	}
	body.WriteString(`]}]`)
	out, err := responseClient(t, responseSnapshot, body.String(), nil).ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
	var api *core.APIError
	if out != nil || !errors.As(err, &api) || api.Code != "ResponseLimitExceeded" {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestCheckResponseTimesBlocksSnapshotCredential(t *testing.T) {
	source := &monitorRefreshTokens{first: "alarm-1", second: "alarm-1"}
	stats := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vmonitor-uptime-manager/v1/uptimes/chk-1" {
			_, _ = w.Write([]byte(responseSnapshot))
			return
		}
		stats++
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source})))
	out, err := c.ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
	var api *core.APIError
	if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || stats != 0 {
		t.Fatalf("out=%v err=%v stats=%d", out, err, stats)
	}
	assertMonitorCredentialsAbsent(t, out, err, "alarm-1")
}

func TestCheckResponseTimesLowercaseInput(t *testing.T) {
	c := responseClient(t, responseSnapshot, `[]`, func(r *http.Request) {
		if r.URL.Query().Get("start_time") != "946684800000" || r.URL.Query().Get("end_time") != "946771200000" {
			t.Errorf("query=%v", r.URL.Query())
		}
	})
	out, err := c.ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1", Start: "2000-01-01t00:00:00z", End: "2000-01-02t00:00:00z"})
	if err != nil || out == nil || out.End.Sub(out.Start) != 24*time.Hour {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestCheckResponseTimesLowercaseBucket(t *testing.T) {
	body := `[` + strings.Replace(responseGroup, "2000-01-01T01:00:00Z", "2000-01-01t01:00:00z", 1) + `]`
	out, err := responseClient(t, responseSnapshot, body, nil).ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
	if err != nil || out == nil || len(out.Items) != 1 || len(out.Items[0].Points) != 1 || !out.Items[0].Points[0].Timestamp.Equal(time.Date(2000, 1, 1, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestCheckResponseTimesNullLocations(t *testing.T) {
	snapshot := strings.Replace(responseSnapshot, `["loc-1","loc-2"]`, `null`, 1)
	out, err := responseClient(t, snapshot, `[]`, nil).ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
	var api *core.APIError
	if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestCheckResponseTimesStatisticsRequires200(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vmonitor-uptime-manager/v1/uptimes/chk-1" {
			_, _ = w.Write([]byte(responseSnapshot))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`[]`))
	}))
	out, err := c.ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1"})
	if out != nil || err == nil {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestCheckResponseTimesRejectsOutOfRangeUTCWindow(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	c.now = func() time.Time { return time.Date(1, 1, 1, 12, 0, 0, 0, time.UTC) }
	for _, in := range []*ListCheckResponseTimesInput{
		{CheckID: "chk-1", Start: "9999-12-31T23:00:00-02:00", End: "9999-12-31T23:30:00-02:00"},
		{CheckID: "chk-1", Start: "0001-01-01T00:00:00+02:00", End: "0001-01-01T00:30:00+02:00"},
		{CheckID: "chk-1"},
	} {
		out, err := c.ListCheckResponseTimes(context.Background(), in)
		if out != nil || !errors.Is(err, core.ErrInvalidInput) {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
}

func TestCheckResponseTimesRejectsOutOfRangeUTCPoint(t *testing.T) {
	body := `[` + strings.Replace(responseGroup, "2000-01-01T01:00:00Z", "0001-01-01T00:01:00+00:02", 1) + `]`
	c := responseClient(t, responseSnapshot, body, nil)
	out, err := c.ListCheckResponseTimes(context.Background(), &ListCheckResponseTimesInput{CheckID: "chk-1", Start: "0001-01-01T00:00:00Z", End: "0001-01-02T00:00:00Z"})
	var api *core.APIError
	if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
		t.Fatalf("out=%v err=%v", out, err)
	}
}
