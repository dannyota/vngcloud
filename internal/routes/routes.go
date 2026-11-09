package routes

import (
	"net/url"
	"strings"
)

// Product identifies a service endpoint.
type Product string

const (
	// ProductVServer identifies the vServer endpoint.
	ProductVServer Product = "vserver"
	// ProductVLB identifies the load balancer endpoint.
	ProductVLB Product = "vlb"
	// ProductVNet identifies the vNetwork endpoint.
	ProductVNet Product = "vnetwork"
	// ProductGLB identifies the global load balancer endpoint.
	ProductGLB Product = "glb"
	// ProductDNS identifies the DNS endpoint.
	ProductDNS Product = "dns"
	// ProductVCR identifies the container registry endpoint.
	ProductVCR Product = "vcr"
	// ProductPortal identifies the portal endpoint.
	ProductPortal Product = "portal"
	// ProductBilling identifies the billing endpoint.
	ProductBilling Product = "billing"
	// ProductCDNDocs identifies the CDN documentation endpoint.
	ProductCDNDocs Product = "cdndocs"
	// ProductMonitor identifies the monitoring endpoint.
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
	// ProductVKS identifies the Kubernetes service endpoint.
	ProductVKS Product = "vks"
	// ProductVServerBackup is the vServer snapshot gateway, which differs from
	// Backup Center's gateway.
	ProductVServerBackup Product = "vserverbackup"
	// ProductBackupCenter is Backup Center's gateway, which differs from the
	// vServer snapshot gateway.
	ProductBackupCenter Product = "backupcenter"
)

// Endpoints resolves service endpoints by product.
type Endpoints interface {
	// Endpoint returns the endpoint for a product.
	Endpoint(Product) string
}

// Route describes a service path and query.
type Route struct {
	Product Product
	Version string
	Parts   []string
	Query   url.Values
}

// URL joins an endpoint, version, escaped path parts, and query.
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
