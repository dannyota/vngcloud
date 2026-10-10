//go:build live

package livetest_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/volume"
)

func testLiveSnapshotPolicies(ctx context.Context, t *testing.T) {
	bodies := map[string][]byte{}
	cfg, err := vngcloud.LoadConfig(ctx,
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithTokenCache(filepath.Join(liveHome, ".vngcloud", "cache")),
		vngcloud.WithConfigFile(emptyFile(t, "config")),
		vngcloud.WithSharedCredentialsFile(emptyFile(t, "credentials")),
		vngcloud.WithResponseCapture(func(captured vngcloud.ResponseCapture) {
			if captured.Operation != "volume.ListSnapshotBackends" && captured.Operation != "volume.ListSnapshotPolicies" {
				return
			}
			bodies[captured.Operation] = append([]byte(nil), captured.Body...)
			saveSnapshotCapture(t, captured.Body, map[string]string{"volume.ListSnapshotBackends": "snapshot_backends", "volume.ListSnapshotPolicies": "snapshot_policies"}[captured.Operation])
		}))
	if err != nil {
		t.Fatal("snapshot config failed")
	}
	client := volume.New(cfg)
	backends, err := client.ListSnapshotBackends(ctx, &volume.ListSnapshotBackendsInput{Name: "HCM-03"})
	if err != nil {
		t.Fatal("backend read failed")
	}
	compareSnapshotCapture(t, bodies["volume.ListSnapshotBackends"], backends.Items, false)
	t.Logf("backends: %d; read passed", len(backends.Items))
	if len(backends.Items) == 0 {
		t.Fatal("no backend available for policy verification")
	}
	for i, backend := range backends.Items {
		out, err := client.ListSnapshotPolicies(ctx, &volume.ListSnapshotPoliciesInput{BackendID: backend.ID})
		if err != nil {
			var api *vngcloud.APIError
			if errors.As(err, &api) {
				t.Logf("policy HTTP status: %d", api.StatusCode)
			}
			t.Logf("policy failure: invalid input=%t; invalid config=%t; project absent=%t; project ambiguous=%t", errors.Is(err, vngcloud.ErrInvalidInput), errors.Is(err, vngcloud.ErrInvalidConfig), errors.Is(err, vngcloud.ErrProjectNotFound), errors.Is(err, vngcloud.ErrProjectAmbiguous))
			t.Fatal("policy read failed")
		}
		compareSnapshotCapture(t, bodies["volume.ListSnapshotPolicies"], out.Items, true)
		var metadata struct{ Page, PageSize, TotalPages, TotalItems int }
		if err := json.Unmarshal(bodies["volume.ListSnapshotPolicies"], &metadata); err != nil {
			t.Fatal("metadata decode failed")
		}
		if out.Page != metadata.Page || out.PageSize != metadata.PageSize || out.TotalPage != metadata.TotalPages || out.TotalItem != metadata.TotalItems {
			t.Fatal("page metadata differs")
		}
		t.Logf("backend index %d: policies %d; comparison passed", i, len(out.Items))
	}
}

func compareSnapshotCapture(t *testing.T, body []byte, items any, policies bool) {
	t.Helper()
	if len(body) == 0 {
		t.Fatal("no snapshot response captured")
	}
	var raw struct{ Items []map[string]any }
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal("raw response decode failed")
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal("SDK encoding failed")
	}
	var decoded []map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal("SDK output decode failed")
	}
	if len(raw.Items) != len(decoded) {
		t.Fatal("item count differs")
	}
	for i, row := range raw.Items {
		if policies {
			for _, key := range []string{"userId", "backendId", "projectId", "isDefault", "deletedAt"} {
				delete(row, key)
			}
			config, ok := row["config"].(map[string]any)
			if !ok {
				t.Fatal("policy config shape differs")
			}
			delete(config, "weeklyConfig")
			delete(config, "monthlyConfig")
			for _, key := range []string{"hourlyConfig", "dailyConfig"} {
				if value, exists := config[key]; exists && value == nil {
					delete(config, key)
				}
			}
		}
		if !reflect.DeepEqual(row, decoded[i]) {
			t.Fatal("raw and decoded retained fields differ")
		}
	}
}

func saveSnapshotCapture(t *testing.T, body []byte, resource string) {
	t.Helper()
	dir := repoPath("examples/basic/output/raw/volume")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal("capture directory failed")
	}
	wrapped, err := json.Marshal(struct {
		Body json.RawMessage `json:"body"`
	}{Body: body})
	if err != nil {
		t.Fatal("capture encoding failed")
	}
	if err := os.WriteFile(filepath.Join(dir, resource+".json"), wrapped, 0o600); err != nil {
		t.Fatal("capture write failed")
	}
}
