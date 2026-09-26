package endpoints

import "testing"

func TestResolveIAMUser(t *testing.T) {
	got := ResolveIAMUser("hcm-3", Overrides{})
	if got.VServer != "https://hcm-3.console.greennode.ai/vserver/iam-vserver-gateway/" {
		t.Fatalf("unexpected vserver endpoint: %s", got.VServer)
	}
	if got.Signin != "https://signin.greennode.ai" {
		t.Fatalf("unexpected signin endpoint: %s", got.Signin)
	}
	if got.VCR != "https://vcr.console.greennode.ai/vcr-api/" {
		t.Fatalf("unexpected vcr endpoint: %s", got.VCR)
	}
	if got.DNS != "https://vdns.console.greennode.ai/vdns-api/" {
		t.Fatalf("unexpected dns endpoint: %s", got.DNS)
	}
	if got.Portal != "https://hcm-3.console.greennode.ai/vserver/iam-billing-gateway/" {
		t.Fatalf("unexpected portal endpoint: %s", got.Portal)
	}
	if got.Dashboard != "https://dashboard.console.greennode.ai/" {
		t.Fatalf("unexpected dashboard endpoint: %s", got.Dashboard)
	}
	if got.Token != "https://dashboard.console.greennode.ai/accounts-api/v1/auth/token" {
		t.Fatalf("unexpected token endpoint: %s", got.Token)
	}
	if got.Billing != "https://dashboard.console.greennode.ai/" {
		t.Fatalf("unexpected billing endpoint: %s", got.Billing)
	}
	if got.CDNDocs != "https://docs.greennode.ai/faq/vcdn" {
		t.Fatalf("unexpected cdndocs endpoint: %s", got.CDNDocs)
	}
	if got.Monitor != "https://vmonitor.console.greennode.ai/" {
		t.Fatalf("unexpected monitor endpoint: %s", got.Monitor)
	}
}

func TestResolveIAMUserMonitorOverride(t *testing.T) {
	got := ResolveIAMUser("hcm-3", Overrides{Monitor: "http://example.test/monitor"})
	if got.Monitor != "http://example.test/monitor/" {
		t.Fatalf("unexpected monitor override: %s", got.Monitor)
	}
}

// TestResolveIAMUserCDNDocsOverrideNotSlashNormalized checks that CDNDocs, a
// full page URL rather than a base to build paths under, is left exactly as
// given: unlike every other endpoint, Normalize must never append a trailing
// slash to it.
func TestResolveIAMUserCDNDocsOverrideNotSlashNormalized(t *testing.T) {
	got := ResolveIAMUser("hcm-3", Overrides{CDNDocs: "https://docs.example/faq/vcdn"})
	if got.CDNDocs != "https://docs.example/faq/vcdn" {
		t.Fatalf("unexpected cdndocs override: %s", got.CDNDocs)
	}
}

func TestResolveIAMUserBillingFollowsDashboardOverride(t *testing.T) {
	got := ResolveIAMUser("hcm-3", Overrides{Dashboard: "https://d.example/"})
	if got.Billing != "https://d.example/" {
		t.Fatalf("unexpected billing endpoint: %s", got.Billing)
	}
}

func TestResolveIAMUserBillingOverride(t *testing.T) {
	got := ResolveIAMUser("hcm-3", Overrides{Billing: "https://b.example"})
	if got.Billing != "https://b.example/" {
		t.Fatalf("unexpected billing endpoint: %s", got.Billing)
	}
	if got.Dashboard != "https://dashboard.console.greennode.ai/" {
		t.Fatalf("billing override changed dashboard: %s", got.Dashboard)
	}
}

func TestResolveIAMUserOverrides(t *testing.T) {
	got := ResolveIAMUser("hcm-3", Overrides{VServer: "http://example.test/vserver", ContainerRegistry: "http://example.test/vcr", Portal: "http://example.test/portal"})
	if got.VServer != "http://example.test/vserver/" {
		t.Fatalf("unexpected override endpoint: %s", got.VServer)
	}
	if got.VCR != "http://example.test/vcr/" {
		t.Fatalf("unexpected vcr override endpoint: %s", got.VCR)
	}
	if got.Portal != "http://example.test/portal/" {
		t.Fatalf("unexpected portal override endpoint: %s", got.Portal)
	}
}

func TestResolveIAMUserShortOverrideAliases(t *testing.T) {
	got := ResolveIAMUser("hcm-3", Overrides{GLB: "http://example.test/glb", VCR: "http://example.test/vcr"})
	if got.GLB != "http://example.test/glb/" {
		t.Fatalf("unexpected glb override endpoint: %s", got.GLB)
	}
	if got.VCR != "http://example.test/vcr/" {
		t.Fatalf("unexpected vcr override endpoint: %s", got.VCR)
	}
}

func TestResolveIAMUserDashboardOverrideDerivesToken(t *testing.T) {
	got := ResolveIAMUser("hcm-3", Overrides{Dashboard: "http://example.test/dash/"})
	if got.Dashboard != "http://example.test/dash/" {
		t.Fatalf("unexpected dashboard override: %s", got.Dashboard)
	}
	if got.Token != "http://example.test/dash/accounts-api/v1/auth/token" {
		t.Fatalf("token not derived from dashboard override: %s", got.Token)
	}
}

func TestVNetworkRegionalGateway(t *testing.T) {
	if got := VNetworkRegionalGateway("hcm-3"); got != "https://hcm-3-vnetwork.console.greennode.ai/vnetwork-gateway/" {
		t.Fatalf("unexpected vnetwork gateway: %s", got)
	}
	if got := VNetworkRegionalGateway(""); got != "" {
		t.Fatalf("expected empty gateway for empty region, got %s", got)
	}
}
