package endpoints

import (
	"fmt"
	"strings"
)

// GreenNode (formerly VNG Cloud) production hosts. The old *.vngcloud.vn
// hosts 301-redirect here, but Go strips the Authorization header on
// cross-domain redirects, so the SDK must target these hosts directly.
const (
	ConsoleDomain    = "console.greennode.ai"
	DefaultSignin    = "https://signin.greennode.ai"
	DefaultDashboard = "https://dashboard.console.greennode.ai/"
	DefaultToken     = DefaultDashboard + "accounts-api/v1/auth/token"
)

type Overrides struct {
	VServer            string
	VLB                string
	VNetwork           string
	GlobalLoadBalancer string
	GLB                string
	DNS                string
	ContainerRegistry  string
	VCR                string
	Portal             string
	Signin             string
	Dashboard          string
	Token              string
}

type Set struct {
	Region    string
	VServer   string
	VLB       string
	VNetwork  string
	GLB       string
	DNS       string
	VCR       string
	Portal    string
	Signin    string
	Dashboard string
	Token     string
}

func ResolveIAMUser(region string, overrides Overrides) Set {
	set := Set{
		Region:    region,
		VServer:   fmt.Sprintf("https://%s.%s/vserver/iam-vserver-gateway/", region, ConsoleDomain),
		VLB:       fmt.Sprintf("https://%s.%s/vserver/iam-vlb-gateway/", region, ConsoleDomain),
		VNetwork:  fmt.Sprintf("https://%s.%s/vserver/vnetwork-gateway/", region, ConsoleDomain),
		GLB:       fmt.Sprintf("https://glb.%s/glb-controller/", ConsoleDomain),
		DNS:       fmt.Sprintf("https://vdns.%s/vdns-api/", ConsoleDomain),
		VCR:       fmt.Sprintf("https://vcr.%s/vcr-api/", ConsoleDomain),
		Portal:    fmt.Sprintf("https://%s.%s/vserver/iam-billing-gateway/", region, ConsoleDomain),
		Signin:    DefaultSignin,
		Dashboard: DefaultDashboard,
		Token:     DefaultToken,
	}
	if overrides.VServer != "" {
		set.VServer = overrides.VServer
	}
	if overrides.VLB != "" {
		set.VLB = overrides.VLB
	}
	if overrides.VNetwork != "" {
		set.VNetwork = overrides.VNetwork
	}
	if endpoint := firstNonEmpty(overrides.GlobalLoadBalancer, overrides.GLB); endpoint != "" {
		set.GLB = endpoint
	}
	if overrides.DNS != "" {
		set.DNS = overrides.DNS
	}
	if endpoint := firstNonEmpty(overrides.ContainerRegistry, overrides.VCR); endpoint != "" {
		set.VCR = endpoint
	}
	if overrides.Portal != "" {
		set.Portal = overrides.Portal
	}
	if overrides.Signin != "" {
		set.Signin = overrides.Signin
	}
	if overrides.Dashboard != "" {
		set.Dashboard = overrides.Dashboard
		set.Token = strings.TrimRight(overrides.Dashboard, "/") + "/accounts-api/v1/auth/token"
	}
	if overrides.Token != "" {
		set.Token = overrides.Token
	}
	return set.Normalize()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (s Set) Normalize() Set {
	s.VServer = normalizeURL(s.VServer)
	s.VLB = normalizeURL(s.VLB)
	s.VNetwork = normalizeURL(s.VNetwork)
	s.GLB = normalizeURL(s.GLB)
	s.DNS = normalizeURL(s.DNS)
	s.VCR = normalizeURL(s.VCR)
	s.Portal = normalizeURL(s.Portal)
	s.Signin = strings.TrimRight(s.Signin, "/")
	s.Dashboard = normalizeURL(s.Dashboard)
	return s
}

func normalizeURL(u string) string {
	if u == "" || strings.HasSuffix(u, "/") {
		return u
	}
	return u + "/"
}

// VNetworkRegionalGateway returns the per-region vNetwork dashboard gateway
// used as a fallback when the primary vnetwork-gateway route is unavailable.
func VNetworkRegionalGateway(region string) string {
	if region == "" {
		return ""
	}
	return fmt.Sprintf("https://%s-vnetwork.%s/vnetwork-gateway/", region, ConsoleDomain)
}
