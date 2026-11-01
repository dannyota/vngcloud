package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
)

func TestQuotaOutcomeClassification(t *testing.T) {
	for _, tc := range []struct {
		err        error
		status     int
		code       string
		wantStatus int
	}{
		{fmt.Errorf("shape: %w", core.ErrInvalidInput), 0, "InvalidUsage", 0},
		{ErrQuotaChanged, 0, "QuotaChanged", 0},
		{ErrUnpriced, 0, "Unpriced", 0},
		{ErrPriceAboveMax, 0, "PriceAboveMax", 0},
		{core.ErrNoCredentials, 0, "NoCredentials", 0},
		{errors.Join(core.ErrNoCredentials, core.ErrInvalidConfig), 0, "NoCredentials", 0},
		{core.ErrTokenCache, 0, "TokenCacheFailed", 0},
		{&core.ServiceAccountLoginError{Status: 401, Reason: "token exchange failed"}, 0, "ServiceAccountLoginFailed", 401},
		{context.Canceled, 0, "RequestFailed", 0},
		{ErrUnexpectedStatus, 0, "UnexpectedStatus", 0},
		{errors.Join(dns.ErrNotSettled, ErrQuotaChanged, &core.APIError{StatusCode: 404, Code: "NotFound"}), 200, "NotSettled", 200},
	} {
		got := quotaOutcomeError("monitor.RecoverSyntheticQuota", tc.err, tc.status)
		if got.Code != tc.code || got.Status != tc.wantStatus || got.Operation != "monitor.RecoverSyntheticQuota" {
			t.Fatalf("err=%v got=%+v", tc.err, got)
		}
	}
	if errors.Is(ErrQuotaChanged, core.ErrInvalidInput) {
		t.Fatal("guard error is not usage")
	}
}

func TestSyntheticMutationSDKProfileRefusal(t *testing.T) {
	for _, setting := range []string{"true", "invalid"} {
		t.Run(setting, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config")
			if err := os.WriteFile(configPath, []byte("[default]\nregion = hcm-3\nread_only = "+setting+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "credentials"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			t.Cleanup(server.Close)
			cfg, err := vngcloud.LoadConfig(context.Background(), vngcloud.WithConfigFile(configPath), vngcloud.WithSharedCredentialsFile(filepath.Join(dir, "credentials")), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Monitor: server.URL + "/"}))
			if err != nil {
				t.Fatal(err)
			}
			c := New(cfg)
			recoverOut, recoverErr := c.RecoverSyntheticQuota(context.Background(), &RecoverSyntheticQuotaInput{ExpectedQuotaID: "quota-syn-1"})
			renewOut, renewErr := c.RenewSyntheticQuota(context.Background(), &RenewSyntheticQuotaInput{ExpectedEndTime: "2500-12-01T00:00:00Z"})
			want := "ReadOnly"
			if setting == "invalid" {
				want = "InvalidConfig"
			}
			if recoverErr == nil || renewErr == nil || recoverOut.Error.Code != want || renewOut.Error.Code != want || calls.Load() != 0 {
				t.Fatalf("recover=%+v renew=%+v calls=%d", recoverOut, renewOut, calls.Load())
			}
		})
	}
}
