package core

import (
	"testing"

	"danny.vn/vngcloud/internal/routes"
)

func TestVServerBackupOverrideIsolation(t *testing.T) {
	base, err := NewConfig(WithRegion("hcm-3"), WithStaticToken("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	overridden, err := NewConfig(WithRegion("hcm-3"), WithStaticToken("test-token"), WithEndpointOverrides(EndpointOverrides{VServerBackup: "https://backup.example.test/gateway"}))
	if err != nil {
		t.Fatal(err)
	}
	c, b := ClientOf(overridden), ClientOf(base)
	if c.Endpoint(routes.ProductVServerBackup) != "https://backup.example.test/gateway/" {
		t.Fatal("override missing")
	}
	for _, product := range []routes.Product{routes.ProductVServer, routes.ProductVLB, routes.ProductVNet, routes.ProductGLB, routes.ProductDNS, routes.ProductVCR, routes.ProductPortal, routes.ProductBilling, routes.ProductCDNDocs, routes.ProductMonitor, routes.ProductIAM, routes.ProductDashboard, routes.ProductStorage, routes.ProductCDN, routes.Product("backupcenter")} {
		if c.Endpoint(product) != b.Endpoint(product) {
			t.Errorf("override changed %s", product)
		}
	}
}
