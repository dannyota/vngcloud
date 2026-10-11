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
