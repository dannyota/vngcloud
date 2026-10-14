package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

const checkAlarmBase = "/vmonitor-api/api/v1/alarms/synthetic/metrics/mona/"

func checkAlarmFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../testdata/monitor/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func checkAlarmHandler(t *testing.T, definition string, history func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		switch r.URL.Path {
		case "/vmonitor-uptime-manager/v1/uptimes/chk-1":
			testutil.WriteFixture(t, w, "../testdata/monitor/GetCheck.json")
		case checkAlarmBase + "alarm-1":
			_, _ = w.Write([]byte(definition))
		default:
			if history == nil {
				t.Errorf("unexpected path = %s", r.URL.Path)
				w.WriteHeader(404)
				return
			}
			history(w, r)
		}
	}
}

func TestGetCheckAlarmDecodesFixture(t *testing.T) {
	c := newTestClient(t, checkAlarmHandler(t, checkAlarmFixture(t, "GetCheckAlarm"), nil))
	out, err := c.GetCheckAlarm(context.Background(), &GetCheckAlarmInput{CheckID: "chk-1"})
	if err != nil {
		t.Fatal(err)
	}
	a := out.Alarm
	if a.ID != "def-1" || a.Status != MonitorStatusOK || len(a.Locations) != 2 || a.StatusUpdatedAt.IsZero() {
		t.Fatalf("unexpected alarm: %+v", a)
	}
	l := a.Locations[0]
	if l.AlarmID != "alarm-loc-1" || l.LocationID != "loc-1" || l.LocationName != "SYNTT-VN-HCM01" || l.State != MonitorStatusOK || l.StateUpdatedAt.IsZero() || l.UpdatedAt.IsZero() || l.CreatedAt.IsZero() {
		t.Fatalf("unexpected location: %+v", l)
	}
	assertCheckAlarmSafe(t, out)
}

func assertCheckAlarmSafe(t *testing.T, out any) {
	t.Helper()
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"user_id", "123456789", "cluster.local", "links", "reason", "expression", "dimensions", "deleted_at"} {
		if strings.Contains(string(b), s) {
			t.Fatalf("projection contains withheld field %q", s)
		}
	}
}

func TestListCheckAlarmHistoryDecodesFixtures(t *testing.T) {
	calls := 0
	c := newTestClient(t, checkAlarmHandler(t, checkAlarmFixture(t, "GetCheckAlarm"), func(w http.ResponseWriter, r *http.Request) {
		calls++
		want := checkAlarmBase + "alarm-loc-1/histories"
		fixture := "ListCheckAlarmHistory"
		if calls == 2 {
			want = checkAlarmBase + "alarm-loc-2/histories"
			fixture = "ListCheckAlarmHistorySecondLocation"
		}
		if r.URL.Path != want {
			t.Errorf("path = %s, want %s", r.URL.Path, want)
		}
		q := r.URL.Query()
		if q.Get("start_time") != "1791936000000" || q.Get("end_time") != "1792022400000" || q.Get("interval") != "60" {
			t.Errorf("query = %v", q)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/"+fixture+".json")
	}))
	c.now = func() time.Time { return time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC) }
	out, err := c.ListCheckAlarmHistory(context.Background(), &ListCheckAlarmHistoryInput{CheckID: "chk-1"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(out.Items) != 5 || out.End.Sub(out.Start) != 24*time.Hour {
		t.Fatalf("calls = %d, output = %+v", calls, out)
	}
	for i, item := range out.Items {
		if item.ID == "" || item.Timestamp.IsZero() || item.LocationName == "" || len(item.SubAlarms) != 1 || len(item.SubAlarms[0].CurrentValues) != 1 {
			t.Fatalf("bad item: %+v", item)
		}
		if i > 0 && item.Timestamp.Before(out.Items[i-1].Timestamp) {
			t.Fatal("history is not sorted")
		}
	}
	assertCheckAlarmSafe(t, out)
}

func TestCheckAlarmLocalValidation(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	for _, in := range []*GetCheckAlarmInput{nil, {}, {CheckID: ".."}, {CheckID: "a/b"}} {
		out, err := c.GetCheckAlarm(context.Background(), in)
		if out != nil || !errors.Is(err, core.ErrInvalidInput) {
			t.Fatalf("GetCheckAlarm = %v, %v", out, err)
		}
	}
	for _, in := range []*ListCheckAlarmHistoryInput{nil, {}, {CheckID: ".."}, {CheckID: "chk-1", Start: "bad"}, {CheckID: "chk-1", End: "bad"}, {CheckID: "chk-1", LocationID: ".."}, {CheckID: "chk-1", Start: "2026-10-14T00:00:00Z", End: "2026-10-13T00:00:00Z"}, {CheckID: "chk-1", Start: "2026-10-14T00:00:00.0001Z", End: "2026-10-14T00:00:00.0009Z"}} {
		out, err := c.ListCheckAlarmHistory(context.Background(), in)
		if out != nil || !errors.Is(err, core.ErrInvalidInput) {
			t.Fatalf("ListCheckAlarmHistory = %v, %v", out, err)
		}
	}
}

func TestCheckAlarmRejectsPeerShape(t *testing.T) {
	raw := checkAlarmFixture(t, "GetCheckAlarm")
	for _, body := range []string{"", "null", "{}", `{"data":null}`, strings.Replace(raw, `"id": "def-1"`, `"id": "../bad"`, 1), strings.Replace(raw, `"id": "alarm-loc-1"`, `"id": "../bad"`, 1), strings.ReplaceAll(raw, `"location_id": "loc-1"`, `"location_id": ""`), strings.ReplaceAll(raw, `"location_id": "loc-2"`, `"location_id": "loc-1"`), strings.ReplaceAll(raw, `2026-10-14T08:53:03.000+07:00`, `bad`)} {
		t.Run(body[:min(len(body), 30)], func(t *testing.T) {
			c := newTestClient(t, checkAlarmHandler(t, body, nil))
			out, err := c.GetCheckAlarm(context.Background(), &GetCheckAlarmInput{CheckID: "chk-1"})
			var apiErr *core.APIError
			if out != nil || !errors.As(err, &apiErr) {
				t.Fatalf("GetCheckAlarm = %v, %v", out, err)
			}
		})
	}
}

func TestCheckAlarmEmptyReference(t *testing.T) {
	for _, ref := range []string{"", "../bad"} {
		calls := 0
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			_, _ = w.Write([]byte(`{"id":"chk-1","history_alarm":` + jsonString(ref) + `,"options":{"test_frequency":60}}`))
		}))
		out, err := c.GetCheckAlarm(context.Background(), &GetCheckAlarmInput{CheckID: "chk-1"})
		if out != nil || err == nil || calls != 1 {
			t.Fatalf("output = %v, error = %v, calls = %d", out, err, calls)
		}
		if ref == "" && !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("error = %v", err)
		}
		if strings.Contains(err.Error(), "../bad") {
			t.Fatal("error echoes peer ID")
		}
	}
}

func TestCheckHistoryAllOrNothing(t *testing.T) {
	for _, mode := range []string{"failure", "conflict", "cancel", "empty", "filter", "missing filter"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			c := newTestClient(t, checkAlarmHandler(t, checkAlarmFixture(t, "GetCheckAlarm"), func(w http.ResponseWriter, r *http.Request) {
				calls++
				if mode == "cancel" {
					cancel()
				}
				if mode == "failure" && strings.Contains(r.URL.Path, "alarm-loc-2") {
					w.WriteHeader(403)
					_, _ = w.Write([]byte(`{"code":"123456789","message":"cluster.local"}`))
					return
				}
				if mode == "empty" {
					_, _ = w.Write([]byte(`{"data":[]}`))
					return
				}
				if mode == "conflict" {
					_, _ = w.Write([]byte(checkAlarmFixture(t, "ListCheckAlarmHistorySecondLocation")))
					return
				}
				if mode == "filter" {
					if !strings.Contains(r.URL.Path, "alarm-loc-2") {
						t.Error("wrong filter route")
					}
					testutil.WriteFixture(t, w, "../testdata/monitor/ListCheckAlarmHistorySecondLocation.json")
					return
				}
				testutil.WriteFixture(t, w, "../testdata/monitor/ListCheckAlarmHistory.json")
			}))
			in := &ListCheckAlarmHistoryInput{CheckID: "chk-1"}
			if mode == "filter" {
				in.LocationID = "loc-2"
			}
			if mode == "missing filter" {
				in.LocationID = "unknown"
			}
			out, err := c.ListCheckAlarmHistory(ctx, in)
			switch mode {
			case "empty":
				if err != nil || len(out.Items) != 0 || calls != 2 {
					t.Fatalf("%v, %v, calls %d", out, err, calls)
				}
			case "filter":
				if err != nil || len(out.Items) != 1 || calls != 1 {
					t.Fatalf("%v, %v, calls %d", out, err, calls)
				}
			default:
				if err == nil || out != nil {
					t.Fatalf("output = %v, error = %v", out, err)
				}
				if mode == "cancel" && (!errors.Is(err, context.Canceled) || calls != 1) {
					t.Fatalf("error = %v, calls = %d", err, calls)
				}
				if mode == "missing filter" && (!errors.Is(err, core.ErrNotFound) || calls != 0) {
					t.Fatalf("error = %v, calls = %d", err, calls)
				}
			}
		})
	}
}

func TestCheckAlarmLimits(t *testing.T) {
	for _, stage := range []string{"snapshot", "definition", "history"} {
		t.Run(stage, func(t *testing.T) {
			calls := 0
			raw := checkAlarmFixture(t, "GetCheckAlarm")
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if (stage == "snapshot" && calls == 1) || (stage == "definition" && calls == 2) || (stage == "history" && calls == 3) {
					size := int(checkAlarmMaxBody) + 1
					if stage == "history" {
						size = int(checkHistoryMaxBody) + 1
					}
					_, _ = w.Write([]byte(strings.Repeat(" ", size)))
					return
				}
				checkAlarmHandler(t, raw, nil)(w, r)
			}))
			out, err := c.ListCheckAlarmHistory(context.Background(), &ListCheckAlarmHistoryInput{CheckID: "chk-1"})
			var ae *core.APIError
			if out != nil || !errors.As(err, &ae) {
				t.Fatalf("output = %v, error = %v", out, err)
			}
		})
	}
	for _, count := range []int{3, 65} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			raw := checkAlarmFixture(t, "GetCheckAlarm")
			var envelope struct {
				Data map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
				t.Fatal(err)
			}
			var alarms []json.RawMessage
			if err := json.Unmarshal(envelope.Data["alarms"], &alarms); err != nil {
				t.Fatal(err)
			}
			base := string(alarms[0])
			alarms = nil
			for i := 1; i <= count; i++ {
				alarms = append(alarms, json.RawMessage(strings.ReplaceAll(strings.ReplaceAll(base, "alarm-loc-1", "alarm-loc-"+strconv.Itoa(i)), `"loc-1"`, jsonString("loc-"+strconv.Itoa(i)))))
			}
			encoded, err := json.Marshal(alarms)
			if err != nil {
				t.Fatal(err)
			}
			envelope.Data["alarms"] = encoded
			encoded, err = json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			c := newTestClient(t, checkAlarmHandler(t, string(encoded), func(w http.ResponseWriter, r *http.Request) {
				calls++
				body := `{"data":[],"padding":"` + strings.Repeat("x", int(checkHistoryMaxBody)-24) + `"}`
				// The body fills exactly one response's safety budget.
				if int64(len(body)) != checkHistoryMaxBody {
					t.Errorf("body length = %d", len(body))
				}
				_, _ = w.Write([]byte(body))
			}))
			out, err := c.ListCheckAlarmHistory(context.Background(), &ListCheckAlarmHistoryInput{CheckID: "chk-1"})
			var ae *core.APIError
			if out != nil || !errors.As(err, &ae) {
				t.Fatalf("output = %v, error = %v", out, err)
			}
			want := 2
			if count == 65 {
				want = 0
			}
			if calls != want {
				t.Fatalf("calls = %d, want %d", calls, want)
			}
		})
	}
}

func TestCheckHistoryOptionalEndpointsAndFrequency(t *testing.T) {
	for _, tc := range []struct {
		start, end         string
		frequency          string
		wantStart, wantEnd int64
		interval           string
	}{
		{end: "2026-10-15T07:00:00+07:00", frequency: "60.0", wantStart: 1791936000000, wantEnd: 1792022400000, interval: "60"},
		{start: "2026-10-13T00:00:00Z", frequency: "1", wantStart: 1791849600000, wantEnd: 1792022400000, interval: "1"},
		{frequency: "6e1", wantStart: 1791936000000, wantEnd: 1792022400000, interval: "60"},
		{frequency: strconv.Itoa(int(^uint(0) >> 1)), wantStart: 1791936000000, wantEnd: 1792022400000, interval: strconv.Itoa(int(^uint(0) >> 1))},
	} {
		t.Run(tc.frequency, func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					_, _ = w.Write([]byte(`{"id":"chk-1","history_alarm":"alarm-1","options":{"test_frequency":` + tc.frequency + `}}`))
					return
				}
				if calls == 2 {
					_, _ = w.Write([]byte(checkAlarmFixture(t, "GetCheckAlarm")))
					return
				}
				q := r.URL.Query()
				if q.Get("start_time") != strconv.FormatInt(tc.wantStart, 10) || q.Get("end_time") != strconv.FormatInt(tc.wantEnd, 10) || q.Get("interval") != tc.interval {
					t.Errorf("query = %v", q)
				}
				if tc.interval == "" && q.Has("interval") {
					t.Error("interval should be omitted")
				}
				_, _ = w.Write([]byte(`{"data":[]}`))
			}))
			c.now = func() time.Time { return time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC) }
			out, err := c.ListCheckAlarmHistory(context.Background(), &ListCheckAlarmHistoryInput{CheckID: "chk-1", Start: tc.start, End: tc.end})
			if err != nil || out == nil {
				t.Fatalf("output = %v, error = %v", out, err)
			}
		})
	}
}

func TestCheckAlarmSensitiveAndServerErrors(t *testing.T) {
	for _, status := range []int{200, 400, 403, 404, 409, 429, 500, 502, 503, 504} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			captures := 0
			handler := checkAlarmHandler(t, checkAlarmFixture(t, "GetCheckAlarm"), nil)
			c := New(testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == checkAlarmBase+"alarm-1" && status != 200 {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"code":"123456789","message":"cluster.local"}`))
					return
				}
				handler(w, r)
			}), func(transport.Capture) { captures++ }))
			out, err := c.GetCheckAlarm(context.Background(), &GetCheckAlarmInput{CheckID: "chk-1"})
			if captures != 0 {
				t.Fatalf("captures = %d", captures)
			}
			if status == 200 {
				if err != nil {
					t.Fatal(err)
				}
				assertCheckAlarmSafe(t, out)
				return
			}
			var ae *core.APIError
			if out != nil || !errors.As(err, &ae) || ae.StatusCode != status || strings.Contains(ae.Code, "123456789") || strings.Contains(err.Error(), "cluster.local") {
				t.Fatalf("output = %v, error = %v", out, err)
			}
			if status == 500 && errors.Is(err, core.ErrNotFound) {
				t.Fatal("definition 500 became NotFound")
			}
			if status == 404 && !errors.Is(err, core.ErrNotFound) {
				t.Fatal("404 lost NotFound")
			}
		})
	}
}

func TestCheckHistoryRejectsMalformedRecords(t *testing.T) {
	raw := checkAlarmFixture(t, "ListCheckAlarmHistory")
	for _, body := range []string{"", "null", "{}", `{"data":null}`, strings.Replace(raw, `"alarm_id": "alarm-loc-1"`, `"alarm_id": "wrong"`, 1), strings.Replace(raw, `"timestamp": "2026-10-14T08:53:04.094+07:00"`, `"timestamp": null`, 1), strings.Replace(raw, `"current_values": [`, `"current_values": [null,`, 1)} {
		t.Run(body[:min(len(body), 30)], func(t *testing.T) {
			c := newTestClient(t, checkAlarmHandler(t, checkAlarmFixture(t, "GetCheckAlarm"), func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			out, err := c.ListCheckAlarmHistory(context.Background(), &ListCheckAlarmHistoryInput{CheckID: "chk-1", LocationID: "loc-1"})
			if out != nil || err == nil {
				t.Fatalf("output = %v, error = %v", out, err)
			}
		})
	}
}

func TestCheckHistoryKeepsTiedTransitions(t *testing.T) {
	c := newTestClient(t, checkAlarmHandler(t, checkAlarmFixture(t, "GetCheckAlarm"), func(w http.ResponseWriter, r *http.Request) {
		alarm := "alarm-loc-1"
		if strings.Contains(r.URL.Path, "alarm-loc-2") {
			alarm = "alarm-loc-2"
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"2","alarm_id":` + jsonString(alarm) + `,"timestamp":"2026-10-14T01:00:00Z","old_state":"OK","new_state":"ALARM","sub_alarms":[]},{"id":"1","alarm_id":` + jsonString(alarm) + `,"timestamp":"2026-10-14T01:00:00Z","old_state":"ALARM","new_state":"OK","sub_alarms":[]}]}`))
	}))
	out, err := c.ListCheckAlarmHistory(context.Background(), &ListCheckAlarmHistoryInput{CheckID: "chk-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 4 {
		t.Fatalf("items = %d", len(out.Items))
	}
	for i, item := range out.Items {
		wantLocation := "loc-1"
		if i >= 2 {
			wantLocation = "loc-2"
		}
		wantID := strconv.Itoa(i%2 + 1)
		if item.LocationID != wantLocation || item.ID != wantID {
			t.Fatalf("item %d = %+v", i, item)
		}
	}
}

func TestCheckAlarmRedactsSentCredential(t *testing.T) {
	handler := checkAlarmHandler(t, strings.ReplaceAll(checkAlarmFixture(t, "GetCheckAlarm"), "SYNTT-VN-HCM01", "probe-token-1"), func(w http.ResponseWriter, r *http.Request) {
		fixture := "ListCheckAlarmHistory"
		if strings.Contains(r.URL.Path, "alarm-loc-2") {
			fixture = "ListCheckAlarmHistorySecondLocation"
		}
		body := strings.ReplaceAll(checkAlarmFixture(t, fixture), `"old_state": "UNDETERMINED"`, `"old_state": "token-1"`)
		_, _ = w.Write([]byte(body))
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	source := &fakeTokenSource{}
	tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source})
	c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, tc))
	out, err := c.ListCheckAlarmHistory(context.Background(), &ListCheckAlarmHistoryInput{CheckID: "chk-1"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "token-1") {
		t.Fatal("output contains sent credential")
	}
}

func TestCheckAlarmRejectsInvalidFrequency(t *testing.T) {
	for _, frequency := range []string{"1e100", "9223372036854775808", "60.5", "0", "-1", "null", `"60"`, "1e100000000"} {
		t.Run(frequency, func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					_, _ = w.Write([]byte(`{"id":"chk-1","history_alarm":"alarm-1","options":{"test_frequency":` + frequency + `}}`))
					return
				}
				if calls == 2 {
					_, _ = w.Write([]byte(checkAlarmFixture(t, "GetCheckAlarm")))
					return
				}
				_, _ = w.Write([]byte(`{"data":[]}`))
			}))
			c.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
			out, err := c.ListCheckAlarmHistory(context.Background(), &ListCheckAlarmHistoryInput{CheckID: "chk-1"})
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 || calls != 1 {
				t.Fatalf("out=%v err=%v calls=%d", out, err, calls)
			}
		})
	}
}

func TestCheckAlarmRefreshCredentialRedaction(t *testing.T) {
	for _, pair := range [][2]string{
		{"synthetic-token-A", "synthetic-token-B"},
		{"synthetic-token-long", "synthetic-token"},
		{"synthetic-token", "synthetic-token-long"},
	} {
		for _, stage := range []string{"definition", "history"} {
			for _, result := range []string{"success", "error", "decode"} {
				t.Run(pair[0]+"/"+pair[1]+"/"+stage+"/"+result, func(t *testing.T) {
					target := checkAlarmBase + "alarm-1"
					if stage == "history" {
						target = checkAlarmBase + "alarm-loc-1/histories"
					}
					echo := pair[0] + " " + pair[1]
					definition := checkAlarmFixture(t, "GetCheckAlarm")
					if stage == "definition" {
						definition = strings.ReplaceAll(definition, "SYNTT-VN-HCM01", echo)
					}
					handler := checkAlarmHandler(t, definition, func(w http.ResponseWriter, _ *http.Request) {
						body := strings.ReplaceAll(checkAlarmFixture(t, "ListCheckAlarmHistory"), "UNDETERMINED", echo)
						_, _ = w.Write([]byte(body))
					})
					c := monitorRefreshClient(t, pair[0], pair[1], target, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == target && result != "success" {
							if result == "error" {
								w.WriteHeader(http.StatusForbidden)
								_, _ = w.Write([]byte(`{"message":` + jsonString(echo) + `}`))
							} else {
								_, _ = w.Write([]byte(`{"data":{"status":{"status_updated_at":` + jsonString(echo) + `}}}`))
							}
							return
						}
						handler(w, r)
					}))
					var out any
					var err error
					if stage == "definition" {
						out, err = c.GetCheckAlarm(context.Background(), &GetCheckAlarmInput{CheckID: "chk-1"})
					} else {
						out, err = c.ListCheckAlarmHistory(context.Background(), &ListCheckAlarmHistoryInput{CheckID: "chk-1", LocationID: "loc-1"})
					}
					if result == "success" {
						if err != nil {
							t.Fatal(err)
						}
						encoded, marshalErr := json.Marshal(out)
						if marshalErr != nil {
							t.Fatal(marshalErr)
						}
						if strings.Contains(string(encoded), "-long") {
							t.Error("overlapping credential left a suffix")
						}
					} else {
						var api *core.APIError
						if !errors.As(err, &api) {
							t.Fatalf("error=%v", err)
						}
						var parse *time.ParseError
						if errors.As(err, &parse) {
							t.Error("withheld response retained timestamp decoder cause")
						}
					}
					assertMonitorCredentialsAbsent(t, out, err, pair[0], pair[1])
				})
			}
		}
	}
}
func TestCheckAlarmOperationCredentialRedaction(t *testing.T) {
	for _, pair := range [][2]string{
		{"synthetic-token-A", "synthetic-token-B"},
		{"synthetic-token-long", "synthetic-token"},
		{"synthetic-token", "synthetic-token-long"},
	} {
		for _, stage := range []string{"definition", "history"} {
			for _, result := range []string{"success", "error", "decode"} {
				t.Run(pair[0]+"/"+pair[1]+"/"+stage+"/"+result, func(t *testing.T) {
					target := checkAlarmBase + "alarm-1"
					if stage == "history" {
						target = checkAlarmBase + "alarm-loc-1/histories"
					}
					echo := pair[0] + " " + pair[1]
					definition := checkAlarmFixture(t, "GetCheckAlarm")
					if stage == "definition" || stage == "history" {
						definition = strings.ReplaceAll(definition, "SYNTT-VN-HCM01", echo)
					}
					handler := checkAlarmHandler(t, definition, func(w http.ResponseWriter, _ *http.Request) {
						body := strings.ReplaceAll(checkAlarmFixture(t, "ListCheckAlarmHistory"), "UNDETERMINED", echo)
						_, _ = w.Write([]byte(body))
					})
					c := monitorRefreshClient(t, pair[0], pair[1], "/vmonitor-uptime-manager/v1/uptimes/chk-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == target && result != "success" {
							if result == "error" {
								w.WriteHeader(http.StatusForbidden)
								_, _ = w.Write([]byte(`{"message":` + jsonString(echo) + `}`))
							} else {
								_, _ = w.Write([]byte(`{"data":{"status":{"status_updated_at":` + jsonString(echo) + `}}}`))
							}
							return
						}
						handler(w, r)
					}))
					var out any
					var err error
					if stage == "definition" {
						out, err = c.GetCheckAlarm(context.Background(), &GetCheckAlarmInput{CheckID: "chk-1"})
					} else {
						out, err = c.ListCheckAlarmHistory(context.Background(), &ListCheckAlarmHistoryInput{CheckID: "chk-1", LocationID: "loc-1"})
					}
					if result == "success" {
						if err != nil {
							t.Fatal(err)
						}
						encoded, marshalErr := json.Marshal(out)
						if marshalErr != nil {
							t.Fatal(marshalErr)
						}
						if strings.Contains(string(encoded), "-long") {
							t.Error("overlapping credential left a suffix")
						}
					} else {
						var api *core.APIError
						if !errors.As(err, &api) {
							t.Fatalf("error=%v", err)
						}
						var parse *time.ParseError
						if errors.As(err, &parse) {
							t.Error("withheld response retained timestamp decoder cause")
						}
					}
					assertMonitorCredentialsAbsent(t, out, err, pair[0], pair[1])
				})
			}
		}
	}
}

func TestCheckHistoryCredentialsAcrossLocations(t *testing.T) {
	const first, second = "synthetic-token-A", "synthetic-token-B"
	definition := strings.ReplaceAll(checkAlarmFixture(t, "GetCheckAlarm"), "SYNTT-VN-HCM01", second)
	handler := checkAlarmHandler(t, definition, func(w http.ResponseWriter, r *http.Request) {
		name := "ListCheckAlarmHistory"
		if strings.Contains(r.URL.Path, "alarm-loc-2") {
			name = "ListCheckAlarmHistorySecondLocation"
		}
		body := strings.ReplaceAll(checkAlarmFixture(t, name), "UNDETERMINED", first+" "+second)
		_, _ = w.Write([]byte(body))
	})
	c := monitorRefreshClient(t, first, second, checkAlarmBase+"alarm-loc-1/histories", handler)
	out, err := c.ListCheckAlarmHistory(context.Background(), &ListCheckAlarmHistoryInput{CheckID: "chk-1"})
	if err != nil || out == nil || len(out.Items) != 5 {
		t.Fatalf("out=%v err=%v", out, err)
	}
	assertMonitorCredentialsAbsent(t, out, err, first, second)
}
