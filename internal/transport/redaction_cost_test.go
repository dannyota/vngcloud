package transport

import (
	"strings"
	"testing"
)

func TestCredentialKeyRedactionConstantSpace(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
	}{
		{"small ordinary", strings.Repeat("a", 1024), strings.Repeat("a", 1024)},
		{"large ordinary", strings.Repeat("a", (4<<20)-64), strings.Repeat("a", (4<<20)-64)},
		{"large late marker", strings.Repeat("a", (4<<20)-64) + "client_secret", "[redacted]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			allocations := testing.AllocsPerRun(1, func() {
				got = redactCredentialKeys(tc.text)
			})
			if got != tc.want {
				t.Fatal("credential-key redaction changed the field")
			}
			if allocations != 0 {
				t.Fatalf("credential-key scan allocated %.0f times for %d bytes", allocations, len(tc.text))
			}
		})
	}
}

func TestCredentialKeyRedactionStreamingMatches(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"", ""},
		{"ordinary error", "ordinary error"},
		{"CLIENT_SECRET=synthetic-value", "[redacted]"},
		{"c.l-i_e n:t s/e+c[r]e{t}=synthetic-value", "[redacted]"},
		{"clientseclientsecret=synthetic-value", "[redacted]"},
		{"clientseclIent-secret=synthetic-value", "[redacted]"},
		{"cl\u0130ent_secret=synthetic-value", "[redacted]"},
		{"client\u200bsecret=synthetic-value", "[redacted]"},
		{"client9secret=ordinary-value", "client9secret=ordinary-value"},
		{"clientésecret=ordinary-value", "clientésecret=ordinary-value"},
		{"clientſecret=ordinary-value", "clientſecret=ordinary-value"},
		{"clientsec", "clientsec"},
		{"clientsecreclientsecre", "clientsecreclientsecre"},
	} {
		if got := redactCredentialKeys(tc.text); got != tc.want {
			t.Fatal("credential-key scan changed filtering, case or prefix matching")
		}
	}
}
