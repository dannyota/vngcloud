package cdn

import (
	"context"
	"strings"
	"testing"
)

func TestEnvelopeRedactionKeepsAccountWithholding(t *testing.T) {
	const key = "synthetic-key@example.com"
	h := newVCDN(t, key, reply(200, "", `{"success":false,"code":500,"message":"bad `+key+`"}`))
	_, err := h.ListCertificates(context.Background(), nil)
	ae := apiError(t, err)
	if !strings.Contains(ae.Message, "was withheld") {
		t.Fatalf("account message not withheld: %v", err)
	}
	if strings.Contains(err.Error(), key) {
		t.Fatal("credential leaked")
	}
}
