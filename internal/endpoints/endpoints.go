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

	// DefaultCDNDocs is the public FAQ page listing GreenNode's CDN IP
	// ranges. It must stay on greennode.ai: the old docs.vngcloud.vn URL
	// redirects here, and the SDK's same-host redirect rule refuses to
	// follow that redirect itself.
	DefaultCDNDocs = "https://docs.greennode.ai/faq/vcdn"

	// DefaultMonitor is the vMonitor uptime manager host root. It carries no
	// region: checks are per account, as the design documents. Uptime paths
	// live under "vmonitor-uptime-manager/v1/" beneath it, and notification
	// channels, once mapped, would live under a second prefix on the same
	// host.
	DefaultMonitor = "https://vmonitor.console.greennode.ai/"

	// DefaultIAM is the IAM console host root, carrying the policies API
	// under "policies-api/v1/". The accounts API (service accounts, users,
	// caller identity) stays on Dashboard: it answers there too, but the
	// policies API returns the console's own HTML page with status 200 on
	// the dashboard host, so the two never share one endpoint field.
	DefaultIAM = "https://iam.console.greennode.ai/"

	// DefaultStorage is the vStorage console API host root. Paths live under
	// "internal/v1/" beneath it.
	DefaultStorage = "https://vstorage.console.greennode.ai/"

	// DefaultCDN is the vCDN API host root. Paths live under "v1/" beneath
	// it. It is not a console host: it takes an API key, not an IAM token.
	DefaultCDN = "https://vcdn-api.vngcloud.vn/vcdn-api/"
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
	Billing            string
	CDNDocs            string
	Monitor            string
	IAM                string
	Storage            string
	CDN                string
	VKS                string
	VServerBackup      string
	BackupCenter       string
}

type Set struct {
	Region        string
	VServer       string
	VLB           string
	VNetwork      string
	GLB           string
	DNS           string
	VCR           string
	Portal        string
	Signin        string
	Dashboard     string
	Token         string
	Billing       string
	CDNDocs       string
	Monitor       string
	IAM           string
	Storage       string
	CDN           string
	VKS           string
	VServerBackup string
	BackupCenter  string
}

// Backup Center IAM routing is verified only in hcm-3.
var backupCenterEndpoints = map[string]string{
	"hcm-3": "https://hcm-3.api.vngcloud.vn/vbackup-gateway/",
}

func ResolveIAMUser(region string, overrides Overrides) Set {
	set := Set{
		Region:        region,
		VServer:       fmt.Sprintf("https://%s.%s/vserver/iam-vserver-gateway/", region, ConsoleDomain),
		VLB:           fmt.Sprintf("https://%s.%s/vserver/iam-vlb-gateway/", region, ConsoleDomain),
		VNetwork:      fmt.Sprintf("https://%s.%s/vserver/vnetwork-gateway/", region, ConsoleDomain),
		GLB:           fmt.Sprintf("https://glb.%s/glb-controller/", ConsoleDomain),
		DNS:           fmt.Sprintf("https://vdns.%s/vdns-api/", ConsoleDomain),
		VCR:           fmt.Sprintf("https://vcr.%s/vcr-api/", ConsoleDomain),
		Portal:        fmt.Sprintf("https://%s.%s/vserver/iam-billing-gateway/", region, ConsoleDomain),
		Signin:        DefaultSignin,
		Dashboard:     DefaultDashboard,
		Token:         DefaultToken,
		CDNDocs:       DefaultCDNDocs,
		Monitor:       DefaultMonitor,
		IAM:           DefaultIAM,
		Storage:       DefaultStorage,
		CDN:           DefaultCDN,
		VServerBackup: VServerBackup(region),
		BackupCenter:  backupCenterEndpoints[region],
	}
	switch region {
	case "hcm-3":
		set.VKS = "https://vks.console.greennode.ai/vks-api/"
	case "han-1":
		set.VKS = "https://vks-han-1.console.greennode.ai/vks-api/"
	}
	if overrides.VKS != "" {
		set.VKS = overrides.VKS
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
	set.Billing = set.Dashboard
	if overrides.Billing != "" {
		set.Billing = overrides.Billing
	}
	if overrides.CDNDocs != "" {
		set.CDNDocs = overrides.CDNDocs
	}
	if overrides.Monitor != "" {
		set.Monitor = overrides.Monitor
	}
	if overrides.IAM != "" {
		set.IAM = overrides.IAM
	}
	if overrides.Storage != "" {
		set.Storage = overrides.Storage
	}
	if overrides.CDN != "" {
		set.CDN = overrides.CDN
	}
	if overrides.VServerBackup != "" {
		set.VServerBackup = overrides.VServerBackup
	}
	if overrides.BackupCenter != "" {
		set.BackupCenter = overrides.BackupCenter
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

// Normalize trims or adds trailing slashes so every field is ready to have a
// path joined onto it. CDNDocs is left untouched: it is the full CDN IP
// range FAQ page URL, not a base to build paths under.
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
	s.Billing = normalizeURL(s.Billing)
	s.Monitor = normalizeURL(s.Monitor)
	s.IAM = normalizeURL(s.IAM)
	s.Storage = normalizeURL(s.Storage)
	s.VKS = normalizeURL(s.VKS)
	s.CDN = normalizeURL(s.CDN)
	s.VServerBackup = normalizeURL(s.VServerBackup)
	s.BackupCenter = normalizeURL(s.BackupCenter)
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

// VServerBackup returns only gateways verified for snapshot policy reads.
func VServerBackup(region string) string {
	return vServerBackupEndpoints[region]
}

var vServerBackupEndpoints = map[string]string{
	"hcm-3": "https://hcm-3.console.greennode.ai/vserver/vbackup-gateway/",
}
