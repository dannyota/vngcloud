//go:build live

package livetest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/vks"
)

// Live capture bypasses the public hook only for the three approved GETs.
// The SDK hook remains disabled on every VKS request.
type liveVKSCapture struct {
	mu       sync.Mutex
	bodies   map[string][]byte
	statuses map[string]int
}

func (c *liveVKSCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if req.Method != "GET" || (req.URL.Host != "vks.console.greennode.ai" && req.URL.Host != "vks-han-1.console.greennode.ai") {
		return resp, nil
	}
	switch req.URL.Path {
	case "/vks-api/v1/clusters", "/vks-api/v1/cluster-versions", "/vks-api/v1/quota":
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		c.mu.Lock()
		c.bodies[req.URL.Path] = body
		c.statuses[req.URL.Path] = resp.StatusCode
		c.mu.Unlock()
	}
	return resp, nil
}

func (c *liveVKSCapture) compare(t *testing.T, region, resource string, decoded any) {
	t.Helper()
	path := "/vks-api/v1/" + resource
	c.mu.Lock()
	body, status := c.bodies[path], c.statuses[path]
	c.mu.Unlock()
	if len(body) == 0 || status != 200 {
		t.Fatal("VKS raw capture missing or unsuccessful")
	}
	envelope := struct {
		Body json.RawMessage `json:"body"`
	}{Body: body}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		t.Fatal("VKS capture encoding failed")
	}
	dir := repoPath("examples", "basic", "output", "raw", "vks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal("VKS capture directory failed")
	}
	if err := os.WriteFile(filepath.Join(dir, region+"-"+resource+".json"), data, 0o600); err != nil {
		t.Fatal("VKS capture write failed")
	}
	model, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal("VKS model encoding failed")
	}
	var rawValue, modelValue any
	if json.Unmarshal(body, &rawValue) != nil || json.Unmarshal(model, &modelValue) != nil {
		t.Fatal("VKS comparison decoding failed")
	}
	if !liveVKSFieldsEqual(rawValue, modelValue) {
		t.Fatal("VKS raw and decoded fields differ")
	}
	t.Logf("status %d: PASS", status)
}

func liveVKSFieldsEqual(raw, model any) bool {
	switch r := raw.(type) {
	case map[string]any:
		m, ok := model.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range r {
			v, ok := m[key]
			if key == "deprecatedAt" && value == nil && v == "" {
				continue
			}
			if !ok || !liveVKSFieldsEqual(value, v) {
				return false
			}
		}
		return true
	case []any:
		m, ok := model.([]any)
		if !ok || len(r) != len(m) {
			return false
		}
		for i, value := range r {
			if !liveVKSFieldsEqual(value, m[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(raw, model)
	}
}

func testLiveVKS(ctx context.Context, t *testing.T, cacheDir, configFile, credentialsFile string) {
	for _, region := range []string{"hcm-3", "han-1"} {
		t.Run(region, func(t *testing.T) {
			capture := &liveVKSCapture{bodies: map[string][]byte{}, statuses: map[string]int{}}
			cfg, err := vngcloud.LoadConfig(ctx, vngcloud.WithRegion(region), vngcloud.WithTokenCache(cacheDir), vngcloud.WithConfigFile(configFile), vngcloud.WithSharedCredentialsFile(credentialsFile), vngcloud.WithTransport(capture))
			if err != nil {
				t.Fatal("VKS configuration failed")
			}
			client := vks.New(cfg)
			clusters, err := client.ListClusters(ctx, nil)
			if err != nil {
				t.Fatalf("ListClusters: %v", err)
			}
			capture.compare(t, region, "clusters", struct {
				Items    []vks.Cluster `json:"items"`
				Total    int           `json:"total"`
				Page     int           `json:"page"`
				PageSize int           `json:"pageSize"`
			}{clusters.Items, clusters.TotalItem, clusters.Page, clusters.PageSize})
			t.Logf("clusters: %d", len(clusters.Items))
			versions, err := client.ListClusterVersions(ctx, nil)
			if err != nil {
				t.Fatalf("ListClusterVersions: %v", err)
			}
			capture.compare(t, region, "cluster-versions", versions.Items)
			t.Logf("versions: %d", len(versions.Items))
			quota, err := client.GetQuota(ctx, nil)
			if err != nil {
				t.Fatalf("GetQuota: %v", err)
			}
			capture.compare(t, region, "quota", quota.Quota)
		})
	}
}
