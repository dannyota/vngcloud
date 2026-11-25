package core

import "testing"

func TestContainsCredential(t *testing.T) {
	for _, tc := range []struct {
		credentials, values []string
		want                bool
	}{
		{[]string{""}, []string{"value"}, false},
		{[]string{"secret"}, []string{"unrelated"}, false},
		{[]string{"", "secret"}, []string{"unrelated", "prefix-secret-suffix"}, true},
	} {
		if got := ContainsCredential(tc.credentials, tc.values...); got != tc.want {
			t.Fatalf("got %t, want %t", got, tc.want)
		}
	}
}
