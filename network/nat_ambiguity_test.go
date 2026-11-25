package network

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestNATCreateLaterAmbiguityClearsOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		second func(string) string
	}{
		{"two candidates", func(row string) string {
			return `{"success":true,"page":1,"size":1000,"totalPage":1,"total":2,"data":[` + row + `,` + strings.Replace(row, "nat-1", "nat-2", 1) + `]}`
		}},
		{"different region", func(row string) string {
			return natInventory(strings.Replace(row, "66b500000000000000000001", "66b500000000000000000003", 1))
		}},
		{"different VPC", func(row string) string { return natInventory(strings.Replace(row, "vpc-1", "vpc-2", 1)) }},
		{"different package", func(row string) string { return natInventory(strings.Replace(row, "package-1", "package-2", 1)) }},
		{"different ID", func(row string) string { return natInventory(strings.Replace(row, "nat-1", "nat-2", 1)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scenario := new(natScenario)
			polls := 0
			client := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario.ordered && strings.HasSuffix(r.URL.Path, "/nats") && r.Method == http.MethodGet {
					polls++
					row := strings.Replace(natTestRow, `"ACTIVE"`, `"PROVISIONING"`, 1)
					if polls == 1 {
						_, _ = w.Write([]byte(natInventory(row)))
						return
					}
					_, _ = w.Write([]byte(tc.second(row)))
					return
				}
				scenario.serve(t, w, r)
			}))
			out, err := client.CreateNATInstance(context.Background(), natTestInput())
			if !errors.Is(err, ErrNotSettled) || polls != 2 || out == nil || out.NATInstance != nil || out.OrderID != "" {
				t.Fatalf("err=%v polls=%d output=%+v", err, polls, out)
			}
		})
	}
}
