package billingresources

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func TestAccountStrict(t *testing.T) {
	for _, tc := range []struct{ value, want string }{{"12345", "12345"}, {"12345.0", "12345"}, {"9223372036854775807", "9223372036854775807"}, {"0", ""}, {"-1", ""}, {"1.5", ""}, {"null", ""}, {`"12345"`, ""}, {"9223372036854775808", ""}} {
		t.Run(tc.value, func(t *testing.T) {
			c := testutil.NewCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/gateway/api/v1/home/user-info" || r.Header.Get("portal-user-id") != "" {
					t.Error("account request")
				}
				_, _ = w.Write([]byte(`{"code":200,"data":{"accountId":` + tc.value + `}}`))
			}))
			got, err := Account(context.Background(), c)
			if got != tc.want || (err == nil) != (tc.want != "") {
				t.Fatalf("account value %q error %v", got, err)
			}
		})
	}
}

func TestListAllTypedFields(t *testing.T) {
	for _, data := range []string{`{"data":[null]}`, `{"data":[{"cost":"NaN"}]}`, `{"data":[],"totalAutoRenews":"1"}`, `{"data":[{"billingElements":[{"sku":1}]}]}`} {
		c := testutil.NewCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"code":200,"data":` + data + `}`))
		}))
		_, err := List(context.Background(), c)
		var api *core.APIError
		if !errors.As(err, &api) {
			t.Fatal("malformed typed list accepted")
		}
	}
}

func TestListNumericCode(t *testing.T) {
	c := testutil.NewCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":200.0,"data":{"data":[]}}`))
	}))
	if _, err := List(context.Background(), c); err != nil {
		t.Fatal("numeric code 200 rejected")
	}
}
