package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/core"
)

func TestAlarmAliasesBlockCreate(t *testing.T) {
	for name, item := range map[string]string{
		"name":          `{"id":"alarm-1","name":"app-alarm","Name":"other-alarm"}`,
		"id":            `{"id":"alarm-1","ID":"alarm-other","name":"other-alarm"}`,
		"nested-id":     `{"id":"alarm-1","name":"other-alarm","alarmLog":{"id":"detail-1","ID":"detail-other"}}`,
		"nested-name":   `{"id":"alarm-1","name":"other-alarm","alarmLog":{"logProjectName":"app","LogProjectName":"other"}}`,
		"nested-object": `{"id":"alarm-1","name":"other-alarm","alarmLog":{"id":"detail-1"},"AlarmLog":{"id":"detail-other"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			creates := 0
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/vmonitor-api/api/v1/alarms/list":
					_, _ = fmt.Fprintf(w, `{"lstData":[%s],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`, item)
				case "/log-api/v1/projects/proj-1":
					_, _ = w.Write([]byte(exampleLogProjectBody("proj-1", "project")))
				case "/vmonitor-api/api/v1/alarms/logs":
					creates++
					_, _ = w.Write([]byte(`{"id":"new-alarm"}`))
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			out, err := client.CreateLogAlarm(context.Background(), &CreateLogAlarmInput{Name: "app-alarm", LogProjectID: "proj-1", ThresholdValue: new(float64(1)), NoWait: true})
			if out != nil || err == nil || creates != 0 {
				t.Fatalf("output=%+v error=%v creates=%d; want refusal without create", out, err, creates)
			}
		})
	}
}

func TestUpdateCheckKeepsOrdinaryRequestValues(t *testing.T) {
	for _, values := range []struct{ name, header, query, body string }{
		{"review-probe", "1", "old", "old-body"},
		{"same-as-name", "old-name", "old-name", "old-name"},
		{"numbers", "123", "1", "1"},
	} {
		t.Run(values.name, func(t *testing.T) {
			puts := 0
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					body := fmt.Sprintf(currentCheckJSON, StatusEnabled)
					body = strings.ReplaceAll(body, `"X-Old": "1"`, fmt.Sprintf(`"X-Old": %q`, values.header))
					body = strings.ReplaceAll(body, `"q": "old"`, fmt.Sprintf(`"q": %q`, values.query))
					body = strings.ReplaceAll(body, `"body": "old-body"`, fmt.Sprintf(`"body": %q`, values.body))
					_, _ = w.Write([]byte(body))
					return
				}
				puts++
				body := decodeBody(t, r)
				request := body["config"].(map[string]any)["request"].(map[string]any)
				if request["headers"].(map[string]any)["X-Old"] != values.header || request["query"].(map[string]any)["q"] != values.query || request["body"] != values.body {
					t.Errorf("ordinary request fields changed: %+v", request)
				}
				_, _ = w.Write([]byte(`{"id":"chk-1","name":"updated-check"}`))
			}))
			out, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Name: new("updated-check")})
			if err != nil || out == nil || puts != 1 {
				t.Fatalf("output=%+v error=%v PUTs=%d; want one successful update", out, err, puts)
			}
		})
	}
}

func TestOrdinaryCheckValuesStayRedactedInErrors(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprintf(w, currentCheckJSON, StatusEnabled)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"echoed-1","message":"sent header 1"}`))
	}))
	_, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Name: new("updated-check")})
	var api *core.APIError
	if !errors.As(err, &api) || api.StatusCode != http.StatusBadRequest {
		t.Fatalf("error=%v; want HTTP 400 after dispatch", err)
	}
	if strings.Contains(api.Code+api.Message+err.Error(), "1") {
		t.Fatal("ordinary request value escaped diagnostic redaction")
	}
}

func TestShortCheckCredentialsStillBlockReflections(t *testing.T) {
	puts := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts++
		}
		_, _ = fmt.Fprintf(w, currentCheckJSON, StatusEnabled)
	}))
	headers := map[string]string{"Authorization": "Bearer 1"}
	out, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Headers: &headers})
	if out != nil || err == nil || puts != 0 {
		t.Fatalf("output=%+v error=%v PUTs=%d; want short credential reflection refused", out, err, puts)
	}
}

func TestCheckCredentialFieldsStayProtected(t *testing.T) {
	const secret = "synthetic-check-credential"
	for _, carrier := range []string{"api-key", "auth", "query-token", "json-password", "url-password", "url-token"} {
		t.Run(carrier, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = fmt.Fprintf(w, `{"id":"chk-1","name":%q}`, secret)
			}))
			input := &CreateCheckInput{Name: "check", URL: "https://example.com", Locations: []string{"loc-1"}}
			switch carrier {
			case "api-key":
				input.Headers = map[string]string{"X-API-Key": secret}
			case "auth":
				input.Headers = map[string]string{"X-Auth": secret}
			case "query-token":
				input.Query = map[string]string{"access_token": secret}
			case "json-password":
				input.Body = fmt.Sprintf(`{"nested":{"password":%q}}`, secret)
			case "url-password":
				input.URL = "https://synthetic:" + secret + "@example.com"
			case "url-token":
				input.URL += "?token=" + secret
			}
			out, err := client.CreateCheck(context.Background(), input)
			if out != nil || err == nil {
				t.Fatal("credential-bearing check field escaped in an ordinary response field")
			}
			assertMonitorErrorPrivate(t, err, secret)
		})
	}
}
