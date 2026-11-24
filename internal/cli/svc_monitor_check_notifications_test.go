package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"danny.vn/vngcloud/monitor"
)

func TestCheckNotificationFlags(t *testing.T) {
	cases := []struct {
		name, command, input string
		flags                []string
		want                 map[string][]string
		bad                  bool
	}{
		{"create overlay", "create-check", `{"Locations":["loc-1"],"Notifications":{"Up":["old-up"],"Undetermined":["old-und"]}}`, []string{"--in-alarm-channel-id", "one,two", "--in-alarm-channel-id", "three"}, map[string][]string{"In-alarm": {"one,two", "three"}, "Up": {"old-up"}, "Undetermined": {"old-und"}}, false},
		{"update replace", "update-check", `{"Notifications":{"Up":["old"]}}`, []string{"--in-alarm-channel-id", "a", "--up-channel-id=", "--undetermined-channel-id", "u"}, map[string][]string{"In-alarm": {"a"}, "Up": {}, "Undetermined": {"u"}}, false},
		{"update preserves", "update-check", `{}`, []string{"--name", "renamed"}, map[string][]string{"In-alarm": {"current"}, "Up": {"current-up"}, "Undetermined": {}}, false},
		{"update JSON only", "update-check", `{"Notifications":{"Up":["json-up"]}}`, nil, map[string][]string{"In-alarm": {}, "Up": {"json-up"}, "Undetermined": {}}, false},
		{"create clear", "create-check", `{"Locations":["loc-1"],"Notifications":{"Up":["old"]}}`, []string{"--up-channel-id="}, map[string][]string{"In-alarm": {}, "Up": {}, "Undetermined": {}}, false},
		{"partial with JSON", "update-check", `{"Notifications":{"Up":["old"]}}`, []string{"--up-channel-id", "a"}, nil, true},
		{"whitespace", "create-check", `{"Locations":["loc-1"]}`, []string{"--up-channel-id", "  "}, nil, true},
		{"mixed empty", "update-check", `{}`, []string{"--in-alarm-channel-id=", "--in-alarm-channel-id", "x", "--up-channel-id=", "--undetermined-channel-id="}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			handler := func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					body, _ = io.ReadAll(r.Body)
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					w.WriteHeader(http.StatusCreated)
				}
				response := monitorUptimesJSON("chk-1", monitor.StatusEnabled)
				response = strings.TrimSuffix(response, "}") + `,"notifications":{"In-alarm":["current"],"Up":["current-up"],"Undetermined":[]}}`
				_, _ = w.Write([]byte(response))
			}
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/vmonitor-uptime-manager/v1/uptimes": handler, "/vmonitor-uptime-manager/v1/uptimes/chk-1": handler})
			root, _, stderr := newSvcRoot(t, fixture)
			args := []string{"--region", "hcm-3", "monitor", tc.command}
			if tc.command == "create-check" {
				args = append(args, "--name", "check", "--url", "https://example.com")
			} else {
				args = append(args, "--check-id", "chk-1")
			}
			args = append(args, "--cli-input-json", tc.input)
			args = append(args, tc.flags...)
			root.SetArgs(args)
			err := root.ExecuteContext(context.Background())
			if tc.bad {
				if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 {
					t.Fatalf("err=%v requests=%d stderr=%s", err, fixture.requestCount(), stderr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err=%v stderr=%s", err, stderr)
			}
			var got struct {
				Notifications map[string][]string `json:"notifications"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Notifications, tc.want) {
				t.Fatalf("notifications=%v want=%v body=%s", got.Notifications, tc.want, body)
			}
		})
	}
}

func TestCheckNotificationFlagsReadOnly(t *testing.T) {
	fixture := newSvcFixture(nil)
	root, _, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--read-only", "monitor", "update-check", "--check-id", "chk-1", "--in-alarm-channel-id=a", "--up-channel-id=", "--undetermined-channel-id="})
	if err := root.ExecuteContext(context.Background()); err == nil || fixture.requestCount() != 0 {
		t.Fatalf("err=%v requests=%d", err, fixture.requestCount())
	}
}

func TestPricingQuoteMonitorHelp(t *testing.T) {
	root, stdout, _ := newSvcRoot(t, newSvcFixture(nil))
	root.SetArgs([]string{"pricing", "get-quote", "--help"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"quote-create-notification-quota", "quote-create-synthetic-quota", "quote-renew-synthetic-quota", "quote-create-log-project"} {
		if !strings.Contains(stdout.String(), "monitor "+name) {
			t.Errorf("missing monitor %s", name)
		}
	}
}
