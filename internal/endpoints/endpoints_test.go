package endpoints

import "testing"

func TestResolveIAMUser(t *testing.T) {
	got := ResolveIAMUser("hcm-3", Overrides{})
	if got.VServer != "https://hcm-3.console.vngcloud.vn/vserver/iam-vserver-gateway/" {
		t.Fatalf("unexpected vserver endpoint: %s", got.VServer)
	}
	if got.Signin != "https://signin.vngcloud.vn" {
		t.Fatalf("unexpected signin endpoint: %s", got.Signin)
	}
	if got.VCR != "https://vcr.console.vngcloud.vn/vcr-api/" {
		t.Fatalf("unexpected vcr endpoint: %s", got.VCR)
	}
	if got.Portal != "https://hcm-3.console.vngcloud.vn/vserver/iam-billing-gateway/" {
		t.Fatalf("unexpected portal endpoint: %s", got.Portal)
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
