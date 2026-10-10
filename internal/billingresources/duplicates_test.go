package billingresources

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func TestListDuplicateKeys(t *testing.T) {
	fixture := testutil.FixtureBody(t, "../../testdata/billing/ListResources.json")
	for _, tc := range []struct{ name, body string }{
		{"renewal", strings.Replace(fixture, `"isRenewing": false`, `"isRenewing": true,"isRenewing":null`, 1)},
		{"code", strings.Replace(fixture, `"code": 200`, `"code":500,"code":200`, 1)},
		{"data", strings.Replace(fixture, `"data": {`, `"data":null,"data": {`, 1)},
		{"nested raw", strings.Replace(fixture, `"level": "good"`, `"level":"bad","level":"good"`, 1)},
		{"escaped equal", strings.Replace(fixture, `"isRenewing": false`, `"isRenewing":true,"is\u0052enewing":false`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testutil.NewCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			_, err := List(context.Background(), c)
			var api *core.APIError
			if !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("duplicate accepted: %v", err)
			}
		})
	}
}

func TestAccountDuplicateKeys(t *testing.T) {
	for _, data := range []string{
		`{"accountId":12345,"userId":54321,"userId":12345}`,
		`{"accountId":54321,"accountId":12345,"userId":12345}`,
	} {
		c := testutil.NewCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"code":200,"data":` + data + `}`))
		}))
		got, err := Account(context.Background(), c)
		var api *core.APIError
		if got != "" || !errors.As(err, &api) || api.Code != "InvalidResponse" {
			t.Fatalf("duplicate accepted: %q %v", got, err)
		}
	}
}

func TestListSiblingKeys(t *testing.T) {
	c := testutil.NewCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":{"data":[{"status":{"a":1},"statusUI":{"a":2}},{"status":{"a":3}}]}}`))
	}))
	if _, err := List(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}
