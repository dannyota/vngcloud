package network

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestNATRejectAmbiguousKeys(t *testing.T) {
	const envelope = `{"success":true,"data":[ROW],"page":1,"size":10,"totalPage":1,"total":1}`
	cases := []struct{ name, row string }{
		{"uppercase null", `{"uuid":"nat-1","NATNAME":null}`},
		{"duplicate package hides null", `{"uuid":"nat-1","natPackage":{"default":null},"natPackage":{"name":"pkg"}}`},
		{"mixed case row", `{"uuid":"nat-1","NatName":"nat"}`},
		{"mixed case package", `{"uuid":"nat-1","natPackage":{"Default":true}}`},
		{"mixed case image", `{"uuid":"nat-1","natPackage":{"image":{"ImageType":"example"}}}`},
		{"mixed case limit", `{"uuid":"nat-1","natPackage":{"image":{"packageLimit":{"Cpu":2}}}}`},
		{"mixed case vpc", `{"uuid":"nat-1","vpc":{"Name":"vpc"}}`},
		{"mixed case nullable", `{"uuid":"nat-1","PublicIp":null}`},
		{"case alias beside exact", `{"uuid":"nat-1","natName":"nat","NatName":"other"}`},
		{"unicode case alias", `{"uuid":"nat-1","ſtatus":"ACTIVE"}`},
		{"duplicate row", `{"uuid":"nat-1","uuid":"nat-1"}`},
		{"duplicate package", `{"uuid":"nat-1","natPackage":{"name":"pkg","name":"pkg"}}`},
		{"duplicate image", `{"uuid":"nat-1","natPackage":{"image":{"id":"image","id":"image"}}}`},
		{"duplicate limit", `{"uuid":"nat-1","natPackage":{"image":{"packageLimit":{"cpu":2,"cpu":2}}}}`},
		{"duplicate vpc", `{"uuid":"nat-1","vpc":{"name":"vpc","name":"vpc"}}`},
		{"duplicate unknown object", `{"uuid":"nat-1","unknown":{"key":1,"key":2}}`},
		{"duplicate unknown array", `{"uuid":"nat-1","unknown":[{"key":1,"key":2}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNATInvalidKeys(t, strings.Replace(envelope, "ROW", tc.row, 1), false)
		})
	}
	for _, tc := range []struct{ name, body string }{
		{"duplicate envelope", strings.Replace(envelope, `"success":true`, `"success":false,"success":true`, 1)},
		{"mixed case envelope", strings.Replace(envelope, `"success"`, `"Success"`, 1)},
		{"mixed case data", strings.Replace(envelope, `"data"`, `"Data"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertNATInvalidKeys(t, strings.Replace(tc.body, "ROW", `{"uuid":"nat-1"}`, 1), false)
		})
	}
}

func TestNATDiscoveryRejectAmbiguousKeys(t *testing.T) {
	const envelope = `{"success":true,"data":[{"uuid":"zone-1","vnetworkDashboard":"https://hcm-3-vnetwork.console.greennode.ai"}]}`
	for _, tc := range []struct{ name, body string }{
		{"duplicate envelope", strings.Replace(envelope, `"success":true`, `"success":false,"success":true`, 1)},
		{"duplicate data", strings.Replace(envelope, `"data":`, `"data":[],"data":`, 1)},
		{"duplicate region", strings.Replace(envelope, `"uuid":"zone-1"`, `"uuid":"zone-1","uuid":"zone-1"`, 1)},
		{"duplicate unknown object", strings.Replace(envelope, `"uuid":"zone-1"`, `"uuid":"zone-1","unknown":{"key":1,"key":2}`, 1)},
		{"duplicate unknown array", strings.Replace(envelope, `"uuid":"zone-1"`, `"uuid":"zone-1","unknown":[{"key":1,"key":2}]`, 1)},
		{"mixed case envelope", strings.Replace(envelope, `"success"`, `"Success"`, 1)},
		{"mixed case data", strings.Replace(envelope, `"data"`, `"Data"`, 1)},
		{"mixed case region", strings.Replace(envelope, `"uuid"`, `"Uuid"`, 1)},
		{"mixed case origin", strings.Replace(envelope, `"vnetworkDashboard"`, `"VnetworkDashboard"`, 1)},
		{"case alias beside exact", strings.Replace(envelope, `"uuid":"zone-1"`, `"uuid":"zone-1","Uuid":"zone-1"`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertNATInvalidKeys(t, tc.body, true)
		})
	}
}

func assertNATInvalidKeys(t *testing.T, body string, discovery bool) {
	t.Helper()
	calls := 0
	c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if discovery && !strings.HasSuffix(r.URL.Path, "/regions") {
			t.Error("resource called after invalid discovery")
			_, _ = w.Write([]byte(`{"success":true,"data":[],"page":1,"size":10,"totalPage":0,"total":0}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	in := &ListNATInstancesInput{ZoneID: "zone-1"}
	if discovery {
		in = nil
	}
	out, err := c.ListNATInstances(context.Background(), in)
	var apiErr *vngcloud.APIError
	if out != nil || !errors.As(err, &apiErr) || apiErr.Code != "InvalidResponse" || apiErr.Operation != listNATOperation || apiErr.StatusCode != http.StatusOK {
		t.Errorf("out = %+v, err = %v", out, err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}
