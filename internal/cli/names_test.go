package cli

import "testing"

func TestKebab(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"ListServers", "list-servers"},
		{"GetServer", "get-server"},
		{"ServerID", "server-id"},
		{"VirtualIPAddressID", "virtual-ip-address-id"},
		{"ListVPCs", "list-vpcs"},
		{"ListSSHKeys", "list-ssh-keys"},
		{"ListOSImages", "list-os-images"},
		{"ListWANIPs", "list-wanips"},
		{"ID", "id"},
		{"VPCID", "vpcid"},
		{"CreateBudgetThreshold", "create-budget-threshold"},
		{"GetCurrentPeriodCost", "get-current-period-cost"},
		{"ListAllSecurityGroupRules", "list-all-security-group-rules"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := kebab(tt.name); got != tt.want {
				t.Fatalf("kebab(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestFlagNameForUsesRenameTable(t *testing.T) {
	if got := flagNameFor("VPCID"); got != "vpc-id" {
		t.Fatalf("flagNameFor(VPCID) = %q, want vpc-id", got)
	}
	if got := flagNameFor("Query"); got != "search" {
		t.Fatalf("flagNameFor(Query) = %q, want search", got)
	}
	if got := flagNameFor("ServerID"); got != "server-id" {
		t.Fatalf("flagNameFor(ServerID) = %q, want server-id", got)
	}
}

func TestCheckOpName(t *testing.T) {
	if err := checkOpName("ListServers", "list-servers"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := checkOpName("ListServers", "list-servrs"); err == nil {
		t.Fatalf("expected an error for a mismatched name")
	}
	// No method in the current SDK needs an operation-name rename, but the
	// table mechanism itself is generic: a hypothetical entry works the same
	// way as the field-name renames above.
	renameTable["Frobnicate"] = "frob"
	defer delete(renameTable, "Frobnicate")
	if err := checkOpName("Frobnicate", "frob"); err != nil {
		t.Fatalf("unexpected error with a table override: %v", err)
	}
}
