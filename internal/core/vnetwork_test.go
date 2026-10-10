package core_test

import (
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func TestVNetworkOverridePresence(t *testing.T) {
	for _, override := range []string{"", "https://override.example/", "https://hcm-3.console.greennode.ai/vserver/vnetwork-gateway/"} {
		cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VNetwork: override}))
		if err != nil {
			t.Fatal(err)
		}
		if got := core.ClientOf(cfg).VNetworkOverride(); got != (override != "") {
			t.Errorf("override %q: got %v", override, got)
		}
	}
	if core.ClientOf(vngcloud.Config{}).VNetworkOverride() {
		t.Error("zero config has override")
	}
	cfg := testutil.NewConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	if !core.ClientOf(cfg).VNetworkOverride() {
		t.Error("test endpoint must remain authoritative")
	}
}
