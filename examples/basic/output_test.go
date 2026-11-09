package main

import (
	"testing"

	"danny.vn/vngcloud"
)

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
