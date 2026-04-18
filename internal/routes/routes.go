package routes

import (
	"net/url"
	"strings"
)

type Product string

const (
	ProductVServer Product = "vserver"
	ProductVLB     Product = "vlb"
	ProductVNet    Product = "vnetwork"
	ProductGLB     Product = "glb"
	ProductDNS     Product = "dns"
	ProductVCR     Product = "vcr"
	ProductPortal  Product = "portal"
)

type Endpoints interface {
	Endpoint(Product) string
}

type Route struct {
	Product Product
	Version string
	Parts   []string
	Query   url.Values
}

func URL(endpoints Endpoints, route Route) string {
	base := endpoints.Endpoint(route.Product)
	if route.Version != "" {
		base += strings.Trim(route.Version, "/") + "/"
	}

	escaped := make([]string, 0, len(route.Parts))
	for _, part := range route.Parts {
		if part == "" {
			continue
		}
		escaped = append(escaped, url.PathEscape(part))
	}
	if len(escaped) > 0 {
		base += strings.Join(escaped, "/")
	}
	if len(route.Query) > 0 {
		base += "?" + route.Query.Encode()
	}
	return base
}
