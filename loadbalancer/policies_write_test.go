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

	in := &UpdatePolicyInput{LoadBalancerID: policyTestLBID, ListenerID: policyTestListenerID, PolicyID: policyTestPolicyID, KeepQueryString: vngcloud.Ptr(false)}
	if _, err := c.UpdatePolicy(context.Background(), in); err != nil {
		t.Fatalf("UpdatePolicy() error = %v", err)
	}
	if body["action"] != "REDIRECT_TO_POOL" || body["redirectPoolId"] != "pool-1" {
		t.Fatalf("body = %+v, want the read action and pool resent", body)
	}
	if body["keepQueryString"] != false {
		t.Fatalf("keepQueryString = %v, want false (the set field)", body["keepQueryString"])
	}
	rules := body["rules"].([]any)
	if len(rules) != 1 || rules[0].(map[string]any)["ruleValue"] != "/" {
		t.Fatalf("rules = %+v, want the read rule resent", rules)
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
