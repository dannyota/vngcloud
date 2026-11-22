package main

import (
	"context"
	"testing"

	"danny.vn/vngcloud"
)

func TestSnapshotPolicyRawCapturePerBackend(t *testing.T) {
	store := newRawCaptureStore()
	for _, id := range []string{"backend-a", "backend-b"} {
		store.add("synthetic", "", vngcloud.ResponseCapture{Operation: "volume.ListSnapshotPolicies", URL: "https://synthetic.invalid/policies?backendId=" + id, StatusCode: 200, Body: []byte(`[]`)})
		if resource := store.resources["volume/snapshot_policy/"+id]; resource == nil || len(resource.Regions) != 1 {
			t.Errorf("missing capture for %s", id)
		}
	}
}

func TestBackupRawCapture(t *testing.T) {
	store := newRawCaptureStore()
	store.add("synthetic", "", vngcloud.ResponseCapture{Operation: "backup.ListBackends", StatusCode: 200, Body: []byte(`[]`)})
	if resource := store.resources["backup/backends"]; resource == nil || len(resource.Regions) != 1 {
		t.Fatal("backup capture missing")
	}
}

func TestNoLoadBalancerChildArtifactsWithoutCalls(t *testing.T) {
	children, err := collectLoadBalancerChildren(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := newSDKOutputStore()
	recordLoadBalancerChildren(store, vngcloud.Config{}, children, nil)
	if len(store.resources) != 0 {
		t.Fatalf("unexpected child artifacts: %v", store.resources)
	}
}

func TestHistoryRawCaptureMappings(t *testing.T) {
	for op, path := range map[string]string{"billing.ListTransactions": "billing/transaction", "billing.ListPayments": "billing/payment"} {
		if operationOutputPaths[op] != path {
			t.Fatal("history raw capture mapping missing")
		}
		store := newRawCaptureStore()
		body := []byte(`{"code":200,"data":{"synthetic":true}}`)
		store.add("synthetic", "", vngcloud.ResponseCapture{Operation: op, Method: "GET", URL: "https://synthetic.invalid/", StatusCode: 200, Body: body})
		body[0] = 'x'
		output := store.resources[path]
		if output == nil || len(output.Regions) != 1 || output.Regions[0].Operation != op || string(output.Regions[0].Body) != `{"code":200,"data":{"synthetic":true}}` {
			t.Fatal("history raw response dropped or aliased")
		}
	}
}
