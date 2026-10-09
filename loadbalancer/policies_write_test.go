package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

const (
	policyTestLBID       = "lb-1"
	policyTestListenerID = "listener-1"
	policyTestPolicyID   = "policy-1"
)

var (
	policyLBPath = "/v2/project-1/loadBalancers/" + policyTestLBID
	policyPath   = policyLBPath + "/listeners/" + policyTestListenerID + "/l7policies/" + policyTestPolicyID
)

func policyLBHandler(w http.ResponseWriter, _ *http.Request) {
	_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, policyTestLBID, lbStatusCreated)
}

func validCreatePolicyInput() *CreatePolicyInput {
	return &CreatePolicyInput{
		LoadBalancerID: policyTestLBID,
		ListenerID:     policyTestListenerID,
		Name:           "policy-1",
		Action:         ActionRedirectToPool,
		RedirectPoolID: "pool-1",
		Rules:          []PolicyRuleInput{{Type: PolicyRuleTypePath, CompareType: CompareTypeStartsWith, Value: "/api"}},
	}
}

func TestCreatePolicyRequestBodyRedirectToPool(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == policyLBPath:
			policyLBHandler(w, r)
		case r.Method == http.MethodPost:
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("decode body: %v, raw = %s", err, data)
			}
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, policyTestPolicyID)
		case r.Method == http.MethodGet && r.URL.Path == policyPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, policyTestPolicyID, lbStatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	in := validCreatePolicyInput()
	in.Rules = []PolicyRuleInput{{Type: PolicyRuleTypePath, CompareType: CompareTypeStartsWith, Value: "/api"}}
	if _, err := c.CreatePolicy(context.Background(), in); err != nil {
		t.Fatalf("CreatePolicy() error = %v", err)
	}
	if body["name"] != "policy-1" || body["action"] != "REDIRECT_TO_POOL" || body["redirectPoolId"] != "pool-1" {
		t.Fatalf("body = %+v, want name/action/redirectPoolId", body)
	}
	if _, ok := body["redirectUrl"]; ok {
		t.Fatalf("body = %+v, want no redirectUrl key", body)
	}
	rules, ok := body["rules"].([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("body[rules] = %+v, want 1 rule", body["rules"])
	}
	rule := rules[0].(map[string]any)
	if rule["ruleType"] != "PATH" || rule["compareType"] != "STARTS_WITH" || rule["ruleValue"] != "/api" {
		t.Fatalf("rule = %+v, want the given rule", rule)
	}
}

func TestCreatePolicyRequestBodyRedirectToURL(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == policyLBPath:
			policyLBHandler(w, r)
		case r.Method == http.MethodPost:
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, policyTestPolicyID)
		case r.Method == http.MethodGet && r.URL.Path == policyPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, policyTestPolicyID, lbStatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	in := &CreatePolicyInput{
		LoadBalancerID:   policyTestLBID,
		ListenerID:       policyTestListenerID,
		Name:             "policy-2",
		Action:           ActionRedirectToURL,
		RedirectURL:      "https://example.com",
		RedirectHTTPCode: 301,
		Rules:            []PolicyRuleInput{{Type: PolicyRuleTypePath, CompareType: CompareTypeEqualTo, Value: "/old"}},
	}
	if _, err := c.CreatePolicy(context.Background(), in); err != nil {
		t.Fatalf("CreatePolicy() error = %v", err)
	}
	if body["redirectUrl"] != "https://example.com" || body["redirectHttpCode"] != float64(301) {
		t.Fatalf("body = %+v, want the redirect URL and code", body)
	}
	if _, ok := body["redirectPoolId"]; ok {
		t.Fatalf("body = %+v, want no redirectPoolId key", body)
	}
}

func TestCreatePolicyRedirectToPoolRequiresPoolID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validCreatePolicyInput()
	in.RedirectPoolID = ""
	if _, err := c.CreatePolicy(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreatePolicyRedirectToPoolRefusesURL(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validCreatePolicyInput()
	in.RedirectURL = "https://example.com"
	if _, err := c.CreatePolicy(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreatePolicyRejectsIncompleteRule(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validCreatePolicyInput()
	in.Rules = []PolicyRuleInput{{Type: PolicyRuleTypePath}}
	if _, err := c.CreatePolicy(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreatePolicyRejectsNoRules(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validCreatePolicyInput()
	in.Rules = nil
	if _, err := c.CreatePolicy(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUpdatePolicyRejectsNoRules(t *testing.T) {
	var putCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == policyLBPath:
			policyLBHandler(w, r)
		case r.Method == http.MethodGet && r.URL.Path == policyPath:
			_, _ = w.Write([]byte(`{"data":{"uuid":"policy-1","action":"REDIRECT_TO_POOL","redirectPoolId":"pool-1",` +
				`"l7Rules":[{"compareType":"EQUAL_TO","ruleValue":"/","ruleType":"PATH"}],"progressStatus":"CREATED"}}`))
		case r.Method == http.MethodPut:
			putCalls.Add(1)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)
	in := &UpdatePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, PolicyID: policyTestPolicyID, Rules: &[]PolicyRuleInput{}}
	if _, err := c.UpdatePolicy(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if putCalls.Load() != 0 {
		t.Fatalf("PUT calls = %d, want 0", putCalls.Load())
	}
}

func TestCreatePolicyNoResendAfter502(t *testing.T) {
	var postCalls atomic.Int32
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			policyLBHandler(w, r)
		case http.MethodPost:
			postCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"upstream"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))
	if _, err := c.CreatePolicy(context.Background(), validCreatePolicyInput()); err == nil {
		t.Fatal("CreatePolicy() error = nil, want an error")
	}
	if postCalls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1 (no resend)", postCalls.Load())
	}
}

// TestCreatePolicyBusyRefusalResendsOnce checks that a busy refusal on the
// create POST itself, a race after the pre-write wait passed, is followed by
// one more wait and exactly one more send.
func TestCreatePolicyBusyRefusalResendsOnce(t *testing.T) {
	var postCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == policyLBPath:
			policyLBHandler(w, r)
		case r.Method == http.MethodPost:
			if postCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"load balancer id lb-1 is updating"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, policyTestPolicyID)
		case r.Method == http.MethodGet && r.URL.Path == policyPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, policyTestPolicyID, lbStatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	if _, err := c.CreatePolicy(context.Background(), validCreatePolicyInput()); err != nil {
		t.Fatalf("CreatePolicy() error = %v", err)
	}
	if got := postCalls.Load(); got != 2 {
		t.Fatalf("POST calls = %d, want 2 (busy refusal, then one resend)", got)
	}
}

func TestCreatePolicyBusyPreWriteBoundExceeded(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == policyLBPath {
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, policyTestLBID, lbStatusUpdating)
			return
		}
		t.Fatal("handler should only be read from")
	}))
	withInstantSleep(c)

	_, err := c.CreatePolicy(context.Background(), validCreatePolicyInput())
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestCreatePolicyRejectsBadPathID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		in := validCreatePolicyInput()
		in.LoadBalancerID = bad
		if _, err := c.CreatePolicy(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("LoadBalancerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

// --- UpdatePolicy ---

func policyGetHandler(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte(`{"data":{"uuid":"policy-1","action":"REDIRECT_TO_POOL","redirectPoolId":"pool-1",` +
		`"keepQueryString":true,"l7Rules":[{"compareType":"EQUAL_TO","ruleValue":"/","ruleType":"PATH"}],` +
		`"progressStatus":"CREATED"}}`))
}

func TestUpdatePolicyReadMergeResendsUnsetFields(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == policyLBPath:
			policyLBHandler(w, r)
		case r.Method == http.MethodGet && r.URL.Path == policyPath:
			policyGetHandler(w, r)
		case r.Method == http.MethodPut:
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("decode body: %v, raw = %s", err, data)
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	in := &UpdatePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, PolicyID: policyTestPolicyID, Action: vngcloud.Ptr(ActionRedirectToPool)}
	if _, err := c.UpdatePolicy(context.Background(), in); err != nil {
		t.Fatalf("UpdatePolicy() error = %v", err)
	}
	if body["action"] != "REDIRECT_TO_POOL" || body["redirectPoolId"] != "pool-1" {
		t.Fatalf("body = %+v, want the read action and pool resent", body)
	}
	for _, k := range []string{"redirectUrl", "redirectHttpCode", "keepQueryString"} {
		if _, ok := body[k]; ok {
			t.Fatalf("body = %+v, want no %s key on a pool redirect", body, k)
		}
	}
	rules := body["rules"].([]any)
	if len(rules) != 1 || rules[0].(map[string]any)["ruleValue"] != "/" {
		t.Fatalf("rules = %+v, want the read rule resent", rules)
	}
}

// TestUpdatePolicyRefusesIncompleteReadRule checks that UpdatePolicy refuses
// to resend a rule the read carried back missing a field, rather than
// silently narrowing it: with Rules left unset, the read-merge must resend
// every rule exactly as read, and a rule this SDK cannot fully reconstruct
// must stop the update instead of resending it incomplete.
func TestUpdatePolicyRefusesIncompleteReadRule(t *testing.T) {
	var putCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == policyLBPath:
			policyLBHandler(w, r)
		case r.Method == http.MethodGet && r.URL.Path == policyPath:
			_, _ = w.Write([]byte(`{"data":{"uuid":"policy-1","action":"REDIRECT_TO_POOL","redirectPoolId":"pool-1",` +
				`"keepQueryString":true,"l7Rules":[{"compareType":"EQUAL_TO","ruleType":"PATH"}],` +
				`"progressStatus":"CREATED"}}`))
		case r.Method == http.MethodPut:
			putCalls.Add(1)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	in := &UpdatePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, PolicyID: policyTestPolicyID, KeepQueryString: vngcloud.Ptr(false)}
	if _, err := c.UpdatePolicy(context.Background(), in); err == nil {
		t.Fatal("UpdatePolicy() error = nil, want an error for an incomplete read rule")
	}
	if putCalls.Load() != 0 {
		t.Fatalf("PUT calls = %d, want 0", putCalls.Load())
	}
}

func TestUpdatePolicyReplacesRules(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == policyLBPath:
			policyLBHandler(w, r)
		case r.Method == http.MethodGet && r.URL.Path == policyPath:
			policyGetHandler(w, r)
		case r.Method == http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	in := &UpdatePolicyInput{
		LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, PolicyID: policyTestPolicyID,
		Rules: &[]PolicyRuleInput{{Type: PolicyRuleTypeHostName, CompareType: CompareTypeEqualTo, Value: "example.com"}},
	}
	if _, err := c.UpdatePolicy(context.Background(), in); err != nil {
		t.Fatalf("UpdatePolicy() error = %v", err)
	}
	rules := body["rules"].([]any)
	if len(rules) != 1 || rules[0].(map[string]any)["ruleType"] != "HOST_NAME" {
		t.Fatalf("rules = %+v, want the replaced rule", rules)
	}
}

// TestUpdatePolicyWriteStatusesPassThrough checks that a 404, 409, or 500 on
// the PUT itself reaches the caller as an unwrapped *vngcloud.APIError
// naming that status.
func TestUpdatePolicyWriteStatusesPassThrough(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == policyLBPath:
				policyLBHandler(w, r)
			case r.Method == http.MethodGet && r.URL.Path == policyPath:
				policyGetHandler(w, r)
			case r.Method == http.MethodPut:
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"boom"}`))
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		in := &UpdatePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, PolicyID: policyTestPolicyID, KeepQueryString: vngcloud.Ptr(false)}
		_, err := c.UpdatePolicy(context.Background(), in)
		var apiErr *vngcloud.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
			t.Fatalf("status %d: err = %v, want *vngcloud.APIError with that status", status, err)
		}
	}
}

func TestUpdatePolicyRejectsNoFieldsSet(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := &UpdatePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, PolicyID: policyTestPolicyID}
	if _, err := c.UpdatePolicy(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUpdatePolicyBusyPreWriteBoundExceeded(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == policyLBPath {
			policyLBHandler(w, r)
			return
		}
		if r.URL.Path == policyPath {
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, policyTestPolicyID, lbStatusUpdating)
			return
		}
		t.Fatalf("unexpected request: %s", r.URL.Path)
	}))
	withInstantSleep(c)

	in := &UpdatePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, PolicyID: policyTestPolicyID, KeepQueryString: vngcloud.Ptr(true)}
	_, err := c.UpdatePolicy(context.Background(), in)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

// --- DeletePolicy ---

func TestDeletePolicySuccess(t *testing.T) {
	var getCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == policyLBPath:
			policyLBHandler(w, r)
		case r.Method == http.MethodGet && r.URL.Path == policyPath:
			n := getCalls.Add(1)
			if n == 1 {
				_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, policyTestPolicyID, lbStatusCreated)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"could not find resource"}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	if _, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, PolicyID: policyTestPolicyID}); err != nil {
		t.Fatalf("DeletePolicy() error = %v", err)
	}
}

func TestDeletePolicyRejectsMissingFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	if _, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestDeletePolicyRejectsBadPathIDs(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		in := &DeletePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, PolicyID: bad}
		if _, err := c.DeletePolicy(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("PolicyID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

// TestDeletePolicyWriteStatusesPassThrough checks that each documented error
// status on the DELETE itself reaches the caller as a *vngcloud.APIError
// naming that status, after exactly one DELETE.
func TestDeletePolicyWriteStatusesPassThrough(t *testing.T) {
	for _, status := range []int{400, 404, 409, 500, 502, 503} {
		var deletes atomic.Int32
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == policyLBPath:
				policyLBHandler(w, r)
			case r.Method == http.MethodGet && r.URL.Path == policyPath:
				_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, policyTestPolicyID, lbStatusCreated)
			case r.Method == http.MethodDelete:
				deletes.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"boom"}`))
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		_, err := c.DeletePolicy(context.Background(), &DeletePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, PolicyID: policyTestPolicyID})
		var apiErr *vngcloud.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
			t.Fatalf("status %d: err = %v, want *vngcloud.APIError with that status", status, err)
		}
		if got := deletes.Load(); got != 1 {
			t.Fatalf("status %d: DELETE calls = %d, want 1", status, got)
		}
	}
}

func policyBodyCapture(t *testing.T, body *map[string]any) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == policyLBPath:
			policyLBHandler(w, r)
		case r.Method == http.MethodGet && r.URL.Path == policyPath:
			_, _ = w.Write([]byte(`{"data":{"uuid":"policy-1","action":"REDIRECT_TO_URL","redirectUrl":"https://a.example",` +
				`"redirectHttpCode":302,"keepQueryString":true,"redirectPoolId":"stale",` +
				`"l7Rules":[{"compareType":"EQUAL_TO","ruleValue":"/","ruleType":"PATH"}],"progressStatus":"CREATED"}}`))
		case r.Method == http.MethodPost || r.Method == http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, body)
			if r.Method == http.MethodPost {
				_, _ = fmt.Fprintf(w, `{"uuid":%q}`, policyTestPolicyID)
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
}

func policyKeys(body map[string]any) map[string]bool {
	keys := map[string]bool{}
	for k := range body {
		keys[k] = true
	}
	return keys
}

func TestCreatePolicyBodyKeysPerAction(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, policyBodyCapture(t, &body))
	withInstantSleep(c)

	in := validCreatePolicyInput()
	if _, err := c.CreatePolicy(context.Background(), in); err != nil {
		t.Fatalf("CreatePolicy(pool) error = %v", err)
	}
	for _, k := range []string{"redirectUrl", "redirectHttpCode", "keepQueryString"} {
		if policyKeys(body)[k] {
			t.Fatalf("pool body = %+v, want no %s key", body, k)
		}
	}
	if !policyKeys(body)["redirectPoolId"] {
		t.Fatalf("pool body = %+v, want redirectPoolId", body)
	}

	body = nil
	url := &CreatePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, Name: "policy-2",
		Action: ActionRedirectToURL, RedirectURL: "https://example.com", RedirectHTTPCode: 301,
		Rules: []PolicyRuleInput{{Type: PolicyRuleTypePath, CompareType: CompareTypeEqualTo, Value: "/old"}}}
	if _, err := c.CreatePolicy(context.Background(), url); err != nil {
		t.Fatalf("CreatePolicy(url) error = %v", err)
	}
	if policyKeys(body)["redirectPoolId"] || !policyKeys(body)["redirectUrl"] ||
		!policyKeys(body)["redirectHttpCode"] || body["keepQueryString"] != false {
		t.Fatalf("url body = %+v, want redirectUrl, redirectHttpCode, keepQueryString and no redirectPoolId", body)
	}
}

func TestCreatePolicyRefusesFieldsTheActionCannotCarry(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	mutations := map[string]func(*CreatePolicyInput){
		"code on pool": func(in *CreatePolicyInput) { in.RedirectHTTPCode = 301 },
		"keep on pool": func(in *CreatePolicyInput) { in.KeepQueryString = true },
		"pool on url":  func(in *CreatePolicyInput) { in.Action, in.RedirectURL = ActionRedirectToURL, "https://x.example" },
		"url missing":  func(in *CreatePolicyInput) { in.Action, in.RedirectPoolID = ActionRedirectToURL, "" },
	}
	for name, mutate := range mutations {
		in := validCreatePolicyInput()
		mutate(in)
		if _, err := c.CreatePolicy(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestUpdatePolicyBodyFollowsMergedAction(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, policyBodyCapture(t, &body))
	withInstantSleep(c)
	base := UpdatePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, PolicyID: policyTestPolicyID}

	// Read action is a URL redirect: the stale pool id from the read is dropped.
	in := base
	in.KeepQueryString = vngcloud.Ptr(false)
	if _, err := c.UpdatePolicy(context.Background(), &in); err != nil {
		t.Fatalf("UpdatePolicy(url) error = %v", err)
	}
	if policyKeys(body)["redirectPoolId"] || body["redirectUrl"] != "https://a.example" ||
		body["redirectHttpCode"] != float64(302) || body["keepQueryString"] != false {
		t.Fatalf("url body = %+v, want the read URL and code, set keepQueryString, no pool", body)
	}

	// Switch to a pool redirect: the read URL fields are dropped.
	body = nil
	in = base
	in.Action, in.RedirectPoolID = vngcloud.Ptr(ActionRedirectToPool), vngcloud.Ptr("pool-2")
	if _, err := c.UpdatePolicy(context.Background(), &in); err != nil {
		t.Fatalf("UpdatePolicy(pool) error = %v", err)
	}
	for _, k := range []string{"redirectUrl", "redirectHttpCode", "keepQueryString"} {
		if policyKeys(body)[k] {
			t.Fatalf("pool body = %+v, want no %s key", body, k)
		}
	}
	if body["redirectPoolId"] != "pool-2" {
		t.Fatalf("pool body = %+v, want redirectPoolId pool-2", body)
	}
}

func TestUpdatePolicyRefusesFieldsTheActionCannotCarry(t *testing.T) {
	var puts atomic.Int32
	inner := policyBodyCapture(t, new(map[string]any))
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts.Add(1)
		}
		inner.ServeHTTP(w, r)
	}))
	withInstantSleep(c)
	base := UpdatePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, PolicyID: policyTestPolicyID}

	pool := base
	pool.RedirectPoolID = vngcloud.Ptr("pool-2") // read action is a URL redirect
	toPool := base
	toPool.Action, toPool.RedirectURL = vngcloud.Ptr(ActionRedirectToPool), vngcloud.Ptr("https://b.example")
	for name, in := range map[string]UpdatePolicyInput{"pool id on url": pool, "url on pool": toPool} {
		if _, err := c.UpdatePolicy(context.Background(), &in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if puts.Load() != 0 {
		t.Fatalf("PUT count = %d, want 0", puts.Load())
	}
}
