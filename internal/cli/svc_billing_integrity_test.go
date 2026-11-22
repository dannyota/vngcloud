package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestBillingPricingIntegrityHelp(t *testing.T) {
	for _, command := range []string{"create-budget", "update-budget", "delete-budget", "create-budget-threshold", "update-budget-threshold", "delete-budget-threshold", "get-quote"} {
		t.Run(command, func(t *testing.T) {
			service := "billing"
			want := []string{"8 MiB", "sent once", "unconfirmed"}
			if command == "get-quote" {
				service = "pricing"
				want = []string{"1 MiB", "redirect", "InvalidResponse"}
			}
			root, stdout, _ := newSvcRoot(t, newSvcFixture(nil))
			root.SetArgs([]string{service, command, "--help"})
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			for _, text := range want {
				if !strings.Contains(stdout.String(), text) {
					t.Errorf("help omitted %q", text)
				}
			}
		})
	}
}

func TestBillingPricingInvalidResponseCLI(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		for _, quote := range []bool{false, true} {
			t.Run(format+"/"+map[bool]string{false: "billing", true: "pricing"}[quote], func(t *testing.T) {
				path := "/gateway/api/v1/budgets"
				args := []string{"billing", "list-budgets"}
				if quote {
					path = "/v1/price"
					args = []string{"pricing", "get-quote", "--resource-type", "snapshot"}
				}
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){path: jsonHandler(200, `{}`)})
				root, stdout, _ := newSvcRoot(t, fixture)
				root.SetArgs(append([]string{"--region", "hcm-3", "--output", format}, args...))
				err := root.ExecuteContext(context.Background())
				if err == nil || classify(err).Code != "InvalidResponse" || stdout.Len() != 0 {
					t.Fatal("invalid response printed data or lost CLI classification")
				}
			})
		}
	}
}
