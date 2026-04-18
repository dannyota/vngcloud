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
