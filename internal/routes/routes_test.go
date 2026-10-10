package routes

import (
	"net/url"
	"testing"
)

type testEndpoints map[Product]string

func (e testEndpoints) Endpoint(p Product) string {
	return e[p]
}

func TestURL(t *testing.T) {
	q := url.Values{}
	q.Set("name", "web server")
	got := URL(testEndpoints{ProductVServer: "https://example.test/"}, Route{
		Product: ProductVServer,
		Version: "v2",
		Parts:   []string{"project one", "servers"},
		Query:   q,
	})
	want := "https://example.test/v2/project%20one/servers?name=web+server"
	if got != want {
		t.Fatalf("URL() = %q, want %q", got, want)
	}
}

func TestVKSURL(t *testing.T) {
	got := URL(testEndpoints{ProductVKS: "https://example.test/vks-api/"}, Route{Product: ProductVKS, Version: "v1", Parts: []string{"clusters"}, Query: url.Values{"page": {"0"}, "pageSize": {"10"}}})
	if got != "https://example.test/vks-api/v1/clusters?page=0&pageSize=10" {
		t.Fatal(got)
	}
}

func TestVServerBackupRoute(t *testing.T) {
	got := URL(testEndpoints{ProductVServerBackup: "https://backup.example.test/vserver/vbackup-gateway/", ProductVServer: "https://wrong.example.test/", Product("backupcenter"): "https://wrong.example.test/"}, Route{Product: ProductVServerBackup, Version: "v1", Parts: []string{"snapshot-policies"}, Query: url.Values{"backendId": {"backend-1"}, "projectId": {"project-1"}, "page": {"1"}, "size": {"10"}}})
	if got != "https://backup.example.test/vserver/vbackup-gateway/v1/snapshot-policies?backendId=backend-1&page=1&projectId=project-1&size=10" {
		t.Fatalf("unexpected route: %s", got)
	}
}

func TestBackupCenterURL(t *testing.T) {
	got := URL(testEndpoints{ProductBackupCenter: "https://backup.example/vbackup-gateway/", ProductVServer: "https://server.example/"}, Route{Product: ProductBackupCenter, Version: "v1", Parts: []string{"backup-policies"}, Query: url.Values{"page": {"1"}, "size": {"200"}}})
	if got != "https://backup.example/vbackup-gateway/v1/backup-policies?page=1&size=200" {
		t.Fatal(got)
	}
}
