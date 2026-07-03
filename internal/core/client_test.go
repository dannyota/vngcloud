package core

import "testing"

func TestNewClientDoesNotMutateAuthConfig(t *testing.T) {
	auth := &IAMUserAuth{RootEmail: "root@example.test", Username: "user", Password: "pass"}
	if _, err := NewClient(Config{Region: "hcm-3", IAMUser: auth}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if auth.SigninBaseURL != "" || auth.TokenURL != "" || auth.DashboardURI != "" {
		t.Fatalf("NewClient mutated the caller's IAMUserAuth: %+v", auth)
	}
}
