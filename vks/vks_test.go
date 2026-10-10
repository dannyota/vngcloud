package vks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

const emptyClusters = `{"items":[],"total":0,"page":0,"pageSize":10}`
const validVersions = `[{"version":"synthetic","enable":false,"stage":"test","deprecatedAt":null}]`
const validQuota = `{"maxClusters":0,"numClusters":0,"maxNodeGroupsPerCluster":0,"maxNodesPerNodeGroup":0}`

func testClient(t *testing.T, handler http.HandlerFunc, opts ...vngcloud.LoadOption) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	base := []vngcloud.LoadOption{vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("test-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VKS: server.URL}), vngcloud.WithRetry(0, time.Millisecond)}
	cfg, err := vngcloud.NewConfig(append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg)
}

func reads(c *Client) []func(context.Context) error {
	return []func(context.Context) error{
		func(ctx context.Context) error { _, err := c.ListClusters(ctx, nil); return err },
		func(ctx context.Context) error { _, err := c.ListClusterVersions(ctx, nil); return err },
		func(ctx context.Context) error { _, err := c.GetQuota(ctx, nil); return err },
	}
}

func TestRoutesAndDefaults(t *testing.T) {
	for _, region := range []string{"hcm-3", "han-1"} {
		t.Run(region, func(t *testing.T) {
			count := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				count++
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("method or bearer mismatch")
				}
				for k := range r.Header {
					if strings.Contains(strings.ToLower(k), "project") || strings.EqualFold(k, "portal-user-id") {
						t.Errorf("unexpected header %s", k)
					}
				}
				switch r.URL.Path {
				case "/v1/clusters":
					if r.URL.RawQuery != "page=0&pageSize=10" {
						t.Errorf("query %s", r.URL.RawQuery)
					}
					_, _ = io.WriteString(w, emptyClusters)
				case "/v1/cluster-versions":
					if r.URL.RawQuery != "" {
						t.Error("unexpected query")
					}
					_, _ = io.WriteString(w, validVersions)
				case "/v1/quota":
					if r.URL.RawQuery != "" {
						t.Error("unexpected query")
					}
					_, _ = io.WriteString(w, validQuota)
				default:
					t.Errorf("path %s", r.URL.Path)
				}
			}, vngcloud.WithRegion(region), vngcloud.WithProjectID("project-test"))
			for _, read := range reads(c) {
				if err := read(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if count != 3 {
				t.Fatalf("requests %d", count)
			}
		})
	}
}

func TestInvalidInputBeforeRequest(t *testing.T) {
	c := testClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })
	for _, in := range []*ListClustersInput{{Page: -1}, {Size: -1}, {Page: math.MaxInt32 + 1}, {Size: math.MaxInt32 + 1}} {
		if _, err := c.ListClusters(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("error %v", err)
		}
	}
}

func TestRegionAndZeroConfig(t *testing.T) {
	for _, override := range []string{"", "http://unused.test"} {
		cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("unsupported"), vngcloud.WithCredentialsProvider(rejectProvider{t}), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VKS: override}))
		if err != nil {
			t.Fatal(err)
		}
		for _, read := range reads(New(cfg)) {
			if err := read(context.Background()); !errors.Is(err, vngcloud.ErrInvalidConfig) {
				t.Fatal(err)
			}
		}
	}
	for _, read := range reads(New(vngcloud.Config{})) {
		if err := read(context.Background()); !errors.Is(err, vngcloud.ErrInvalidConfig) {
			t.Fatal(err)
		}
	}
}

type rejectProvider struct{ t *testing.T }

func (p rejectProvider) Token(context.Context) (vngcloud.Token, error) {
	p.t.Error("unexpected login")
	return vngcloud.Token{}, errors.New("login")
}
func (rejectProvider) Invalidate(string) {}

func TestFixtures(t *testing.T) {
	for _, region := range []string{"hcm", "han"} {
		for _, op := range []string{"clusters", "cluster-versions", "quota"} {
			t.Run(region+"/"+op, func(t *testing.T) {
				b, err := os.ReadFile("../testdata/vks/" + region + "-" + op + ".json")
				if err != nil {
					t.Fatal(err)
				}
				c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b) })
				var read func(context.Context) error
				switch op {
				case "clusters":
					read = reads(c)[0]
				case "cluster-versions":
					read = reads(c)[1]
				default:
					read = reads(c)[2]
				}
				if err := read(context.Background()); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	// Cluster items are schema-derived because the live cluster lists were empty.
	b, err := os.ReadFile("../testdata/vks/cluster-list-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "page=1&pageSize=2" {
			t.Error("query mismatch")
		}
		_, _ = w.Write(b)
	})
	out, err := c.ListClusters(context.Background(), &ListClustersInput{Page: 1, Size: 2})
	if err != nil {
		t.Fatal(err)
	}
	if out.Page != 1 || out.PageSize != 2 || out.TotalItem != 5 || out.TotalPage != 3 || len(out.Items) != 1 {
		t.Fatalf("output %+v", out)
	}
	want := Cluster{ID: "cls-1", Name: "<account>", Description: "<account>", Status: "UNKNOWN", ReleaseChannel: "STABLE", Version: "synthetic", AZStrategy: "UNKNOWN", CreatedAt: "synthetic-date", UpdatedAt: "synthetic-date", NumNodes: 3, EnablePrivateCluster: true}
	if out.Items[0] != want {
		t.Fatalf("cluster %+v", out.Items[0])
	}
}

func TestMalformedResponses(t *testing.T) {
	bodies := [][]string{
		{"", `null`, `{}`, `[]`, `{"data":[]}`, `{"items":null,"total":0,"page":0,"pageSize":10}`, `{"items":[],"total":-1,"page":0,"pageSize":10}`, `{"items":[],"total":0,"page":-1,"pageSize":10}`, `{"items":[],"total":0,"page":0,"pageSize":0}`, `{"items":[],"total":0,"page":2147483648,"pageSize":10}`, `{"items":[],"total":0,"page":0,"pageSize":2147483648}`, `{"items":[],"total":1.5,"page":0,"pageSize":10}`, `{"items":[],"page":0,"pageSize":10}`, `{"items":[null],"total":1,"page":0,"pageSize":10}`},
		{"", `null`, `{}`, `[null]`, `[{}]`, `{"data":[]}`, `[{"version":"v","enable":null,"stage":"s"}]`, `[{"version":null,"enable":false,"stage":"s"}]`, `[{"version":"v","enable":false}]`, `[{"version":"v","enable":"false","stage":"s"}]`, `[{"version":"v","enable":false,"stage":"s","deprecatedAt":2}]`},
		{"", `null`, `{}`, `[]`, `{"data":{}}`, `{"maxClusters":null,"numClusters":0,"maxNodeGroupsPerCluster":0,"maxNodesPerNodeGroup":0}`, `{"maxClusters":1.5,"numClusters":0,"maxNodeGroupsPerCluster":0,"maxNodesPerNodeGroup":0}`, `{"maxClusters":0,"numClusters":0,"maxNodeGroupsPerCluster":0}`, `{"maxClusters":2147483648,"numClusters":0,"maxNodeGroupsPerCluster":0,"maxNodesPerNodeGroup":0}`},
	}
	for i, list := range bodies {
		for j, body := range list {
			t.Run(fmt.Sprintf("%d/%d", i, j), func(t *testing.T) {
				c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
				if err := reads(c)[i](context.Background()); err == nil || !strings.Contains(err.Error(), "malformed response") {
					t.Fatalf("error %v", err)
				}
			})
		}
	}
}

func TestSafeErrorsAndCapture(t *testing.T) {
	for i := range 3 {
		for _, status := range []int{200, 202, 400, 401, 403, 404, 429, 500, 502, 503, 504} {
			t.Run(fmt.Sprintf("%d/%d", i, status), func(t *testing.T) {
				captures := 0
				var logs strings.Builder
				c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":{"message":"fake-secret"},"code":"fake-secret"}`)
				}, vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }), vngcloud.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
				err := reads(c)[i](context.Background())
				if err == nil {
					t.Fatal("missing error")
				}
				assertSafe(t, err)
				var ae *vngcloud.APIError
				if !errors.As(err, &ae) || ae.StatusCode != status {
					t.Fatalf("status error %v", err)
				}
				if ae.Retryable != (status == 429 || status == 502 || status == 503 || status == 504) {
					t.Fatal("retryability mismatch")
				}
				if strings.Contains(ae.Code, "fake-secret") || ae.Operation != []string{"vks.ListClusters", "vks.ListClusterVersions", "vks.GetQuota"}[i] {
					t.Fatal("unsafe error fields")
				}
				sentinel := map[int]error{401: vngcloud.ErrAuth, 403: vngcloud.ErrPermission, 404: vngcloud.ErrNotFound, 429: vngcloud.ErrRateLimited}[status]
				if sentinel != nil && !errors.Is(err, sentinel) {
					t.Fatal("sentinel missing")
				}
				if captures != 0 || strings.Contains(logs.String(), "fake-secret") {
					t.Fatal("capture or debug leaked")
				}
			})
		}
	}
}

func assertSafe(t *testing.T, err error) {
	t.Helper()
	for e := err; e != nil; e = errors.Unwrap(e) {
		if strings.Contains(e.Error(), "fake-secret") {
			t.Fatal("error leaked")
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNetworkAndCancellation(t *testing.T) {
	for _, cause := range []error{errors.New("fake-secret"), context.Canceled, context.DeadlineExceeded} {
		c := testClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }, vngcloud.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, cause })}))
		for _, read := range reads(c) {
			err := read(context.Background())
			if err == nil {
				t.Fatal("missing error")
			}
			assertSafe(t, err)
			if cause != nil && (errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded)) && !errors.Is(err, cause) {
				t.Fatal("context sentinel lost")
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := testClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })
	for _, read := range reads(c) {
		if err := read(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}

func TestUnknownSecretFieldsDropped(t *testing.T) {
	const marker = "vks-model-credential-marker"
	const fields = `"kubeconfig":"` + marker + `","token":"` + marker + `","accessToken":"` + marker + `","clientSecret":"` + marker + `","privateKey":"` + marker + `","certificate":"` + marker + `","caCert":"` + marker + `","password":"` + marker + `"`
	bodies := []struct {
		name string
		body string
	}{
		{"cluster", `{"items":[{"id":"cls-1",` + fields + `,"config":{` + fields + `}}],"total":1,"page":0,"pageSize":10,` + fields + `}`},
		{"version", `[{"version":"v","enable":false,"stage":"s",` + fields + `,"config":{` + fields + `}}]`},
		{"quota", `{"maxClusters":0,"numClusters":0,"maxNodeGroupsPerCluster":0,"maxNodesPerNodeGroup":0,` + fields + `,"config":{` + fields + `}}`},
	}
	for i, tc := range bodies {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tc.body) })
			var model any
			switch i {
			case 0:
				out, err := c.ListClusters(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(out.Items) != 1 || out.Items[0].ID != "cls-1" {
					t.Fatal("expected one decoded cluster")
				}
				model = out.Items[0]
			case 1:
				out, err := c.ListClusterVersions(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(out.Items) != 1 || out.Items[0].Version != "v" {
					t.Fatal("expected one decoded version")
				}
				model = out.Items[0]
			case 2:
				out, err := c.GetQuota(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				model = out.Quota
			}
			if strings.Contains(fmt.Sprintf("%+v", model), marker) {
				t.Fatal("formatted model leaked credential marker")
			}
			b, err := json.Marshal(model)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), marker) {
				t.Fatal("JSON model leaked credential marker")
			}
		})
	}
}

func TestRequiredFieldTypes(t *testing.T) {
	bases := []map[string]any{
		{"items": []any{}, "total": 0, "page": 0, "pageSize": 10},
		{"version": "v", "enable": false, "stage": "s"},
		{"maxClusters": 0, "numClusters": 0, "maxNodeGroupsPerCluster": 0, "maxNodesPerNodeGroup": 0},
	}
	for i, base := range bases {
		for field := range base {
			for _, mutation := range []string{"missing", "null", "wrong"} {
				t.Run(fmt.Sprintf("%d/%s/%s", i, field, mutation), func(t *testing.T) {
					body := make(map[string]any, len(base))
					for k, v := range base {
						body[k] = v
					}
					switch mutation {
					case "missing":
						delete(body, field)
					case "null":
						body[field] = nil
					case "wrong":
						body[field] = map[string]any{"token": "fake-secret"}
					}
					var value any = body
					if i == 1 {
						value = []any{body}
					}
					b, err := json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
					c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b) })
					err = reads(c)[i](context.Background())
					if err == nil || !strings.Contains(err.Error(), "malformed response") {
						t.Fatalf("error %v", err)
					}
					assertSafe(t, err)
				})
			}
		}
	}
}

func TestTotalPagesWithoutOverflow(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"items":[],"total":9223372036854775807,"page":0,"pageSize":2}`)
	})
	out, err := c.ListClusters(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalItem != math.MaxInt64 || out.TotalPage != math.MaxInt64/2+1 {
		t.Fatalf("metadata %+v", out)
	}
}

func TestRetryAndSuccess(t *testing.T) {
	for i, body := range []string{emptyClusters, validVersions, validQuota} {
		calls := 0
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `{"error":{"message":"fake-secret"}}`)
				return
			}
			_, _ = io.WriteString(w, body)
		}, vngcloud.WithRetry(1, time.Millisecond))
		if err := reads(c)[i](context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatalf("calls %d", calls)
		}
	}
}

func TestRedirectSafeError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://fake-secret.example/?token=fake-secret", http.StatusFound)
	})
	for _, read := range reads(c) {
		err := read(context.Background())
		if err == nil {
			t.Fatal("missing error")
		}
		assertSafe(t, err)
	}
}

type failureProvider struct{ err error }

func (p failureProvider) Token(context.Context) (vngcloud.Token, error) {
	return vngcloud.Token{}, p.err
}
func (failureProvider) Invalidate(string) {}

func TestAuthenticationSafeError(t *testing.T) {
	for _, cause := range []error{
		&vngcloud.LoginError{Status: 401, Reason: "fake-secret", Err: vngcloud.ErrAuth},
		&vngcloud.APIError{StatusCode: 403, Code: "fake-secret", Message: "fake-secret", Err: errors.New("fake-secret")},
		errors.New("fake-secret"),
	} {
		c := testClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }, vngcloud.WithCredentialsProvider(failureProvider{cause}))
		for _, read := range reads(c) {
			err := read(context.Background())
			if err == nil {
				t.Fatal("missing error")
			}
			assertSafe(t, err)
			if errors.Is(cause, vngcloud.ErrAuth) {
				var login *vngcloud.LoginError
				if !errors.As(err, &login) || !errors.Is(err, vngcloud.ErrAuth) || login.Status != 401 {
					t.Fatal("login class lost")
				}
			}
		}
	}
}

func TestSuccessfulCaptureSuppressed(t *testing.T) {
	for i, body := range []string{emptyClusters, validVersions, validQuota} {
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }, vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { t.Error("unexpected capture") }))
		if err := reads(c)[i](context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVersionDeprecationDate(t *testing.T) {
	for _, suffix := range []string{"", `,"deprecatedAt":null`, `,"deprecatedAt":"synthetic-date"`} {
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `[{"version":"v","enable":false,"stage":"s"`+suffix+`}]`)
		})
		out, err := c.ListClusterVersions(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		if strings.Contains(suffix, "synthetic-date") {
			want = "synthetic-date"
		}
		if out.Items[0].DeprecatedAt != want {
			t.Fatal("deprecation date mismatch")
		}
	}
}

type opaqueFailure struct{}

func (opaqueFailure) Error() string { panic("arbitrary error text must not be read") }

func TestOpaqueCauseIsDiscarded(t *testing.T) {
	c := testClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }, vngcloud.WithCredentialsProvider(failureProvider{opaqueFailure{}}))
	for _, read := range reads(c) {
		err := read(context.Background())
		if err == nil {
			t.Fatal("missing error")
		}
		var ae *vngcloud.APIError
		if !errors.As(err, &ae) || ae.Err != nil {
			t.Fatal("unknown cause retained")
		}
		_ = err.Error()
	}
}

func TestMaximumInputQuery(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "page=2147483647&pageSize=2147483647" {
			t.Error("maximum query mismatch")
		}
		_, _ = io.WriteString(w, emptyClusters)
	})
	if _, err := c.ListClusters(context.Background(), &ListClustersInput{Page: math.MaxInt32, Size: math.MaxInt32}); err != nil {
		t.Fatal(err)
	}
}
