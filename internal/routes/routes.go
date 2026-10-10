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
	ProductBilling Product = "billing"
	ProductCDNDocs Product = "cdndocs"
	ProductMonitor Product = "monitor"
	// ProductIAM is the IAM console host: the policies API only. The
	// accounts API (service accounts, users, caller identity) stays under
	// ProductDashboard, per the IAM writes design.
	ProductIAM Product = "iam"
	// ProductDashboard is the dashboard console host, used directly by
	// operations that are not billing- or portal-specific, such as the IAM
	// accounts API.
	ProductDashboard Product = "dashboard"
	// ProductStorage is the vStorage console API host.
	ProductStorage Product = "storage"
	// ProductCDN is the vCDN API host. It takes an API key, not an IAM
	// token, and ignores the region.
	ProductCDN Product = "cdn"
	ProductVKS Product = "vks"
	// ProductVServerBackup is the vServer snapshot gateway, which differs from
	// Backup Center's gateway.
	ProductVServerBackup Product = "vserverbackup"
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
