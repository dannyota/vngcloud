package pricing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/transport"
)

func estimateHTTPConfig(t *testing.T, url string, opts ...vngcloud.LoadOption) vngcloud.Config {
	t.Helper()
	options := []vngcloud.LoadOption{vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("estimate-canary"), vngcloud.WithRetry(0, 0), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: url, Portal: url, Storage: url})}
	options = append(options, opts...)
	cfg, err := vngcloud.NewConfig(options...)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestEstimateReflectionBeforeRetryRefreshCapture(t *testing.T) {
	for _, status := range []int{200, 401, 503} {
		for _, target := range []string{"quote", "project", "zones", "parents", "types", "images", "encryption", "regions", "storage types", "purchases", "config"} {
			t.Run(fmt.Sprintf("%s/%d", target, status), func(t *testing.T) {
				calls, captures := 0, 0
				server := estimateServer()
				server.RootDiskEncryptionTypeID = "aes-xts-plain64_256"
				in := &EstimateInput{Servers: []EstimateServer{server}}
				if target == "regions" || target == "config" || target == "storage types" || target == "purchases" {
					in = &EstimateInput{Storage: []EstimateStorage{{Key: "s", Quantity: 1, Type: "Gold", QuotaGB: 30}}}
				}
				handler := estimateHandler(t, func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"optimumPrice":10,"propertiesPrice":[{"name":"estimate\u002dcanary"}]}`))
				})
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					match := target == "project" && strings.HasSuffix(r.URL.Path, "/projects") || target == "parents" && strings.HasSuffix(r.URL.Path, "/volume_type_zones") || target == "storage types" && strings.HasSuffix(r.URL.Path, "/project_types") || target == "purchases" && strings.HasSuffix(r.URL.Path, "/purchase_types") || target == "quote" && r.Method == "POST" || target == "zones" && strings.HasSuffix(r.URL.Path, "/zones") || target == "types" && strings.HasSuffix(r.URL.Path, "/volume_types") || target == "images" && strings.HasSuffix(r.URL.Path, "/images/os") || target == "encryption" && strings.HasSuffix(r.URL.Path, "/encryption_types") || target == "regions" && strings.HasSuffix(r.URL.Path, "/regions") || target == "config" && strings.HasSuffix(r.URL.Path, "/configurations")
					if match {
						calls++
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"estimate\u002dcanary":"reflected"}`))
						return
					}
					if strings.Contains(r.URL.Path, "/internal/") || strings.Contains(r.URL.Path, "billing-api/") {
						estimateStorageHandler(t, nil)(w, r)
						return
					}
					handler(w, r)
				}))
				defer srv.Close()
				project := "project-1"
				if target == "project" {
					project = ""
				}
				cfg := estimateHTTPConfig(t, srv.URL, vngcloud.WithProjectID(project), vngcloud.WithRetry(2, 1), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }))
				out, err := New(cfg).Estimate(estimateContext(), in)
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != status || api.Retryable || calls != 1 || captures != 0 || strings.Contains(err.Error(), "canary") {
					t.Fatalf("error %v calls %d captures %d", err, calls, captures)
				}
			})
		}
	}
}

func TestEstimateRedirectsRefused(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(strconv.FormatBool(foreign), func(t *testing.T) {
			destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("followed redirect") }))
			defer destination.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				location := "/v1/orders"
				if foreign {
					location = destination.URL + "/v1/orders"
				}
				http.Redirect(w, r, location, http.StatusTemporaryRedirect)
			}))
			defer srv.Close()
			out, err := New(estimateHTTPConfig(t, srv.URL)).Estimate(estimateContext(), &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}})
			if err == nil || out != nil {
				t.Fatal("accepted redirect")
			}
		})
	}
}

func TestEstimateHostileQuoteSchemas(t *testing.T) {
	for _, body := range []string{`{}`, `{"optimumPrice":null}`, `{"optimumPrice":0}`, `{"optimumPrice":-1}`, `{"optimumPrice":1e999}`, `{"optimumPrice":1,"optimumPrice":2}`, `{"optimumPrice":1,"currency":"USD"}`, `{"optimumPrice":1,"currency":null}`, `{"optimumPrice":1,"propertiesPrice":[{"monthlyPrice":-1}]}`, `{"success":false,"optimumPrice":1}`, `{"optimumPrice":1,"page":1}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			out, err := New(estimateHTTPConfig(t, srv.URL)).Estimate(estimateContext(), &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}})
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 {
				t.Fatalf("error %v", err)
			}
			if _, e := json.Marshal(out); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestEstimateWithholdsUntrustedRefusalDiagnostics(t *testing.T) {
	for _, catalog := range []bool{false, true} {
		t.Run(strconv.FormatBool(catalog), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{"code":"reflected-account","message":"reflected-description"}`))
			}))
			defer srv.Close()
			in := &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}}
			if catalog {
				in = &EstimateInput{Servers: []EstimateServer{estimateServer()}}
			}
			_, err := New(estimateHTTPConfig(t, srv.URL)).Estimate(estimateContext(), in)
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.StatusCode != 500 || strings.Contains(api.Code, "reflected") || strings.Contains(err.Error(), "reflected") {
				t.Fatalf("unsafe diagnostics %v", err)
			}
		})
	}
}

func TestEstimateExplicitProfilesAndTokenCache(t *testing.T) {
	var logins atomic.Int32
	signin, token := estimateIAMServer(t, func(w http.ResponseWriter, _ *http.Request) {
		n := logins.Add(1)
		_, _ = fmt.Fprintf(w, `{"accessToken":"profile-token-%d","expiresIn":3600}`, n)
	})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer profile-token-") {
			t.Error("explicit profile used environment credentials")
		}
		_, _ = w.Write([]byte(`{"optimumPrice":10}`))
	}))
	defer api.Close()
	dir := t.TempDir()
	configPath, credsPath := filepath.Join(dir, "config"), filepath.Join(dir, "credentials")
	if err := os.WriteFile(configPath, []byte("[profile alpha]\nregion=hcm-3\nproject_id=project-1\n[profile beta]\nregion=hcm-3\nproject_id=project-1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credsPath, []byte("[alpha]\nroot_email=root@example.test\nusername=synthetic-user\npassword=synthetic-password\n[beta]\nroot_email=root@example.test\nusername=synthetic-user\npassword=synthetic-password\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VNGCLOUD_ACCESS_TOKEN", "environment-token")
	t.Setenv("VNGCLOUD_PROJECT_ID", "wrong-project")
	cache := filepath.Join(dir, "cache")
	in := &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}}
	for _, profile := range []string{"alpha", "beta", "alpha"} {
		cfg, err := vngcloud.LoadConfig(context.Background(), vngcloud.WithProfile(profile), vngcloud.WithConfigFile(configPath), vngcloud.WithSharedCredentialsFile(credsPath), vngcloud.WithTokenCache(cache), vngcloud.WithRetry(0, 0), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Signin: signin.URL, Token: token.URL, Dashboard: token.URL + "/", Portal: api.URL, VServer: api.URL}))
		if err != nil {
			t.Fatal(err)
		}
		out, err := New(cfg).Estimate(estimateContext(), in)
		if err != nil || out == nil {
			t.Fatalf("estimate error %v", err)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{"root@example.test", "synthetic-user", "profile-token", "environment-token", "project-1"} {
			if strings.Contains(string(raw), value) {
				t.Fatal("output retained account identity")
			}
		}
	}
	if logins.Load() != 2 {
		t.Fatal("profile cache identity changed")
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		files++
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatal("unsafe token cache mode")
		}
	}
	info, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	if files != 2 || runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		t.Fatal("unexpected cache files or directory mode")
	}
}

func TestEstimateResponseBodyCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"unused":"` + strings.Repeat("x", 8<<20) + `","optimumPrice":10}`))
	}))
	defer srv.Close()
	out, err := New(estimateHTTPConfig(t, srv.URL)).Estimate(estimateContext(), &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}})
	if out != nil || err == nil || !errors.Is(err, transport.ErrBodyTooLarge) || errors.Is(err, ErrEstimateLimit) {
		t.Fatalf("output %v error %v", out, err)
	}
}

func TestEstimateReflectionEncodings(t *testing.T) {
	for _, status := range []int{200, 401, 503} {
		for _, body := range []string{`{"description":"estimate-canary"}`, `{"description":"estimate\u002dcanary"}`, `{"name":"estimate-canary"`, `{"estimate-canary":1}`, `{"unused":?,"optimumPrice":1}`} {
			t.Run(fmt.Sprintf("%d/%s", status, body), func(t *testing.T) {
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.WriteHeader(status)
					_, _ = w.Write([]byte(body))
				}))
				defer srv.Close()
				out, err := New(estimateHTTPConfig(t, srv.URL, vngcloud.WithRetry(2, 1))).Estimate(estimateContext(), &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}})
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != status || api.Retryable || calls != 1 || strings.Contains(err.Error(), "canary") {
					t.Fatalf("error %v calls %d", err, calls)
				}
			})
		}
	}
}

func TestEstimateRotatedCredentialsStayGuarded(t *testing.T) {
	for _, value := range []string{"original-token", "rotated-token"} {
		t.Run(value, func(t *testing.T) {
			provider := &estimateRotatingProvider{entered: make(chan struct{}), release: make(chan struct{})}
			close(provider.release)
			calls, captures := 0, 0
			var logs bytes.Buffer
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") == "Bearer original-token" {
					w.WriteHeader(401)
					_, _ = w.Write([]byte(`{}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"optimumPrice": 1, "propertiesPrice": []map[string]string{{"name": value}}})
			}))
			defer srv.Close()
			cfg := estimateHTTPConfig(t, srv.URL, vngcloud.WithCredentialsProvider(provider), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }), vngcloud.WithLogger(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
			out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}})
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 || calls != 2 || captures != 0 || strings.Contains(logs.String(), value) || strings.Contains(err.Error(), value) {
				t.Fatalf("error %v calls %d captures %d", err, calls, captures)
			}
		})
	}
}
