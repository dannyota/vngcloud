package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud/dns"
)

func TestDNSUncertainReplacementKeepsPartialOutput(t *testing.T) {
	writes := 0
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/dns/hosted-zone/zone-1": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(zoneJSON(dns.StatusActive, "old", []string{"vpc-1"})))
				return
			}
			writes++
			w.WriteHeader(503)
		},
	})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--query", "HostedZone.Description", "dns", "update-hosted-zone", "--hosted-zone-id", "zone-1", "--description", "new"})
	err := root.ExecuteContext(context.Background())
	if !errors.Is(err, dns.ErrNotSettled) || classify(err).Code != "NotSettled" || exitCode(err) != 1 || writes != 1 || !strings.Contains(stdout.String(), "zone-1") {
		t.Fatalf("uncertain zone replacement lost: output=%s writes=%d error=%v", stdout.String(), writes, err)
	}
}

func TestDNSIncompleteListDoesNotPrintCompleteInventory(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/dns/hosted-zone/zone-1/record": jsonHandler(200, `{"listData":[{"recordId":"record-1"}],"page":1,"pageSize":1,"totalPage":2,"totalItem":2}`),
	})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "dns", "list-records", "--hosted-zone-id", "zone-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil || classify(err).Code != "RequestFailed" || exitCode(err) != 1 || stdout.Len() != 0 || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete read looked complete: output=%s error=%v", stdout.String(), err)
	}
}
