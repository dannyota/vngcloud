package core

import (
	"testing"

	"danny.vn/vngcloud/internal/routes"
)

func TestBackupCenterEndpoint(t *testing.T) {
	for _, override := range []string{"", "https://backup.example/gateway"} {
		cfg, err := NewConfig(WithRegion("hcm-3"), WithStaticToken("token"), WithEndpointOverrides(EndpointOverrides{BackupCenter: override}))
		if err != nil {
			t.Fatal(err)
		}
		want := "https://hcm-3.api.vngcloud.vn/vbackup-gateway/"
		if override != "" {
			want = override + "/"
		}
		c := ClientOf(cfg)
		if c.Endpoint(routes.ProductBackupCenter) != want || c.Endpoint(routes.ProductVServer) == want {
			t.Fatal("product isolation failed")
		}
	}
}
