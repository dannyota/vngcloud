package core

import (
	"strings"
	"testing"
)

func TestRedactErrorWithholdsServiceAccountCredentialKeys(t *testing.T) {
	cfg, err := NewConfig(WithRegion("hcm-3"), WithServiceAccountCredentials("client", "synthetic-secret-marker", true))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"client_secret", "CLIENT-SECRET", "client.secret", "Client Secret", "client_se_cret"} {
		message, code := ClientOf(cfg).RedactError(key+"=synthetic-secret-marker", key+"=synthetic-secret-marker", "different-product-token")
		if strings.Contains(message, "synthetic-secret-marker") || strings.Contains(code, "synthetic-secret-marker") {
			t.Fatalf("credential key %q escaped withholding", key)
		}
		if message != "[redacted]" || code != "[redacted]" {
			t.Fatal("error text was not withheld")
		}
	}
	message, code := ClientOf(cfg).RedactError("ordinary error", "BadRequest")
	if message != "ordinary error" || code != "BadRequest" {
		t.Fatal("ordinary error changed")
	}
}

func TestRedactErrorWithholdsOverlappingTokenCredentialKeys(t *testing.T) {
	cfg, err := NewConfig(WithRegion("hcm-3"), WithServiceAccountCredentials("client", "synthetic-secret-marker", true))
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"client_secret", "CLIENT-SECRET", "client.secret", "client_se_cret"} {
		t.Run(token, func(t *testing.T) {
			message, code := ClientOf(cfg).RedactError(token+"=synthetic-secret-marker", token+"=synthetic-secret-marker", token)
			if message != "[redacted]" || code != "[redacted]" {
				t.Error("overlapping token prevented credential-key withholding")
			}
		})
	}
}
