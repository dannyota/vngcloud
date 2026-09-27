package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// TestPricingGetQuoteDefaultsActionToCreate checks that get-quote sends
// action "create" when --action is left off: pricing.GetQuoteInput.Action
// defaults to ActionCreate when empty, so an existing caller that never set
// it is unaffected by the field's addition.
func TestPricingGetQuoteDefaultsActionToCreate(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			var err error
			body, err = io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"optimumPrice":100,"originalPrice":100,"discountPrice":0,"propertiesPrice":[]}`))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "pricing", "get-quote", "--resource-type", "snapshot"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get-quote: %v (stderr=%s)", err, stderr.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["action"] != "create" {
		t.Fatalf("body[action] = %v, want create (%s)", decoded["action"], body)
	}
}

// TestPricingGetQuoteActionFlagSendsResize checks that --action reaches the
// request body as given, letting a caller price a resize the same way a
// create is priced.
func TestPricingGetQuoteActionFlagSendsResize(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			var err error
			body, err = io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"optimumPrice":100,"originalPrice":100,"discountPrice":0,"propertiesPrice":[]}`))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "pricing", "get-quote", "--resource-type", "server", "--action", "resize",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get-quote: %v (stderr=%s)", err, stderr.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["action"] != "resize" {
		t.Fatalf("body[action] = %v, want resize (%s)", decoded["action"], body)
	}
}
