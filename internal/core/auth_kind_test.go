package core

import "testing"

func TestIAMUserLoginProvenance(t *testing.T) {
	auth := &IAMUserAuth{RootEmail: "root@example.test", Username: "user", Password: "synthetic"}
	for _, tt := range []struct {
		name string
		opts []Option
		want bool
	}{
		{"IAM", []Option{WithIAMUser(auth)}, true},
		{"service account overrides IAM", []Option{WithIAMUser(auth), WithServiceAccountCredentials("client", "secret", true)}, false},
		{"static overrides IAM", []Option{WithIAMUser(auth), WithStaticToken("synthetic-token")}, false},
		{"custom overrides IAM", []Option{WithIAMUser(auth), WithCredentialsProvider(&fakeCredentialsProvider{})}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, err := newClient(append([]Option{WithRegion("han-1")}, tt.opts...)...)
			if err != nil {
				t.Fatal(err)
			}
			if c.UsesIAMUserLogin() != tt.want {
				t.Fatal("incorrect auth provenance")
			}
		})
	}
	if (&Client{}).UsesIAMUserLogin() {
		t.Fatal("zero client has auth provenance")
	}
}

func TestServiceAccountInternalProvenance(t *testing.T) {
	option := WithServiceAccountCredentials("id", "secret", true)
	for _, tc := range []struct {
		opts []Option
		want bool
	}{
		{[]Option{option}, true},
		{[]Option{option, WithStaticToken("token")}, false},
		{[]Option{WithStaticToken("token"), option}, false},
		{[]Option{option, WithCredentialsProvider(&fakeCredentialsProvider{})}, false},
		{[]Option{WithIAMUser(&IAMUserAuth{RootEmail: "root", Username: "user", Password: "password"})}, false},
	} {
		cfg, err := NewConfig(append([]Option{WithRegion("hcm-3")}, tc.opts...)...)
		if err != nil {
			t.Fatal(err)
		}
		if UsesServiceAccountLogin(cfg) != tc.want {
			t.Fatal("incorrect service-account provenance")
		}
	}
	if UsesServiceAccountLogin(Config{}) {
		t.Fatal("zero config has auth provenance")
	}
}
