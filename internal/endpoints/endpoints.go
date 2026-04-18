package endpoints

import (
	"fmt"
	"strings"
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
	Token              string
}

type Set struct {
	Region   string
	VServer  string
	VLB      string
	VNetwork string
	GLB      string
	DNS      string
	VCR      string
	Portal   string
	Signin   string
	Token    string
}

func ResolveIAMUser(region string, overrides Overrides) Set {
	set := Set{
		Region:   region,
		VServer:  fmt.Sprintf("https://%s.console.vngcloud.vn/vserver/iam-vserver-gateway/", region),
		VLB:      fmt.Sprintf("https://%s.console.vngcloud.vn/vserver/iam-vlb-gateway/", region),
		VNetwork: fmt.Sprintf("https://%s.console.vngcloud.vn/vserver/vnetwork-gateway/", region),
		GLB:      "https://glb.console.vngcloud.vn/glb-controller/",
		DNS:      "https://vdns.api.vngcloud.vn/",
		VCR:      "https://vcr.console.vngcloud.vn/vcr-api/",
		Portal:   fmt.Sprintf("https://%s.console.vngcloud.vn/vserver/iam-billing-gateway/", region),
		Signin:   "https://signin.vngcloud.vn",
		Token:    "https://dashboard.console.vngcloud.vn/accounts-api/v1/auth/token",
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
	return s
}

func normalizeURL(u string) string {
	if u == "" || strings.HasSuffix(u, "/") {
		return u
	}
	return u + "/"
}
