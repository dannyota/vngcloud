//go:build live

package vngcloud_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/backup"
)

// Sensitive SDK requests suppress capture hooks. This test-only transport
// saves the two authorized bodies locally without weakening SDK capture rules.
type backupLiveCapture struct {
	t      *testing.T
	bodies map[string][]byte
}

func (c *backupLiveCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	name := ""
	if req.Method == http.MethodGet && req.URL.Host == "hcm-3.api.vngcloud.vn" {
		switch req.URL.Path {
		case "/vbackup-gateway/v1/backends":
			name = "backends"
		case "/vbackup-gateway/v1/backup-policies":
			name = "policies"
		}
	}
	if name == "" {
		return resp, nil
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	c.bodies[name] = body
	wrapped, err := json.MarshalIndent(struct {
		StatusCode int             `json:"statusCode"`
		Body       json.RawMessage `json:"body"`
	}{resp.StatusCode, body}, "", "  ")
	if err != nil {
		c.t.Fatal("capture encode failed")
	}
	dir := "examples/basic/output/raw/backup"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		c.t.Fatal("capture directory failed")
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), wrapped, 0o600); err != nil {
		c.t.Fatal("capture write failed")
	}
	c.t.Logf("%s status=%d", name, resp.StatusCode)
	return resp, nil
}

func testLiveBackup(ctx context.Context, t *testing.T) {
	capture := &backupLiveCapture{t: t, bodies: map[string][]byte{}}
	cfg, err := vngcloud.LoadConfig(ctx, vngcloud.WithRegion("hcm-3"), vngcloud.WithTokenCache(filepath.Join(liveHome, ".vngcloud", "cache")), vngcloud.WithConfigFile(emptyFile(t, "config")), vngcloud.WithSharedCredentialsFile(emptyFile(t, "credentials")), vngcloud.WithTransport(capture), vngcloud.WithRetry(0, 0))
	if err != nil {
		t.Fatal("backup configuration failed")
	}
	client := backup.New(cfg)
	b, err := client.ListBackends(ctx, nil)
	if err != nil {
		t.Fatal("ListBackends failed")
	}
	p, err := client.ListPolicies(ctx, nil)
	if err != nil {
		t.Fatal("ListPolicies failed")
	}
	compareBackupLive(t, capture.bodies["backends"], b.Items, b.Page, b.PageSize, b.TotalPage, b.TotalItem, false)
	compareBackupLive(t, capture.bodies["policies"], p.Items, &p.Page, &p.PageSize, p.TotalPage, p.TotalItem, true)
	t.Logf("backends=%d policies=%d; raw comparison passed", len(b.Items), len(p.Items))
}

func compareBackupLive(t *testing.T, body []byte, items any, page, size *int, totalPages, totalItems int, policies bool) {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal("raw collection decode failed")
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw["items"], &rows); err != nil {
		t.Fatal("raw items decode failed")
	}
	if policies {
		for _, row := range rows {
			delete(row, "userId")
			config, ok := row["config"].(map[string]any)
			if !ok {
				t.Fatal("raw policy config shape failed")
			}
			for _, key := range []string{"hourlyConfig", "weeklyConfig", "monthlyConfig", "statusSendEmail"} {
				delete(config, key)
			}
		}
	}
	// Compare only present wire fields: optional daily values remain absent or null.
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal("SDK items encode failed")
	}
	var decoded []map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal("SDK items decode failed")
	}
	if len(rows) != len(decoded) {
		t.Fatal("item count comparison failed")
	}
	for i := range rows {
		compareBackupFields(t, rows[i], decoded[i])
	}
	for key, value := range map[string]any{"page": page, "pageSize": size, "totalPages": totalPages, "totalItems": totalItems} {
		actual, err := json.Marshal(value)
		if err != nil {
			t.Fatal("metadata encode failed")
		}
		var a, b any
		if json.Unmarshal(raw[key], &a) != nil || json.Unmarshal(actual, &b) != nil || !reflect.DeepEqual(a, b) {
			t.Fatal("metadata comparison failed")
		}
	}
	if len(raw) != 5 {
		t.Fatal("unexpected collection fields")
	}
}

func compareBackupFields(t *testing.T, raw, decoded map[string]any) {
	t.Helper()
	for key, value := range raw {
		got, ok := decoded[key]
		if !ok {
			t.Fatal("unmodeled live field")
		}
		if nested, ok := value.(map[string]any); ok {
			other, ok := got.(map[string]any)
			if !ok {
				t.Fatal("nested field shape differs")
			}
			compareBackupFields(t, nested, other)
		} else if !reflect.DeepEqual(value, got) {
			t.Fatal("field comparison failed")
		}
	}
}
