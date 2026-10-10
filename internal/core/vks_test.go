package core

import (
	"testing"

	"danny.vn/vngcloud/internal/routes"
)

func TestVKSEndpoint(t *testing.T) {
	for _, region := range []string{"hcm-3", "han-1"} {
		cfg, err := NewConfig(WithRegion(region), WithStaticToken("test"), WithEndpointOverrides(EndpointOverrides{VKS: "http://example.test/vks"}))
		if err != nil {
			t.Fatal(err)
		}
		if got := ClientOf(cfg).Endpoint(routes.ProductVKS); got != "http://example.test/vks/" {
			t.Fatal(got)
		}
	}
}
