package floatingip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestResponseScopeCredentialBound(t *testing.T) {
	ctx, unsafe, cancel := ResponseContext(context.Background())
	defer cancel()
	// Exercise observation through the actual authenticated transport boundary.
	calls := 0
	cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = w.Write([]byte(`{}`)) }))
	client := core.ClientOf(cfg)
	for i := range 257 {
		observe := ctx.Value(observerKey{}).(func(string) ([]string, error))
		_, err := observe(fmt.Sprintf("credential-%d", i))
		if i < 256 && err != nil {
			t.Fatal(err)
		}
		if i == 256 && err == nil {
			t.Fatal("credential history silently truncated")
		}
	}
	values, snapshotErr := Credentials(ctx)
	if len(values) != 256 || snapshotErr == nil {
		t.Fatal("exhaustion discarded credential protection")
	}
	var raw json.RawMessage
	err := client.DoJSON(ctx, transport.Request{Operation: "test", Method: "GET", URL: "https://example.invalid/", OK: []int{200}}, &raw)
	if err == nil || calls != 0 || !unsafe([]byte(`{}`)) {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestResponseReflectionUnknownAndEscapedStrings(t *testing.T) {
	for _, raw := range []string{`{"unknown":"credential-1"}`, `{"unknown":"credential\u002d1"}`, `{"credential-1":"value"}`} {
		if !Reflects([]byte(raw), []string{"credential-1"}) {
			t.Fatal("credential reflection accepted")
		}
	}
	if Reflects([]byte(`{"unknown":"safe"}`), []string{"credential-1"}) {
		t.Fatal("safe JSON refused")
	}
}

func TestExposurePermissionAndResponseLimitsRemainWithOverride(t *testing.T) {
	for _, status := range []int{401, 403, 500} {
		c := testutil.NewCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"synthetic refusal"}`))
		}))
		ctx, unsafe, cancel := ResponseContext(context.Background())
		defer cancel()
		_, err := Exposure(ctx, c, []string{"sg-1"}, true, unsafe)
		var api *core.APIError
		if !errors.As(err, &api) || api.StatusCode != status {
			t.Fatalf("lost permission/status: %v", err)
		}
	}
}
