package core

import "testing"

func TestQuoteResourceInfo(t *testing.T) {
	type body struct {
		FlavorID string `json:"flavorId"`
		UserData string `json:"userData,omitempty"`
	}
	info, err := QuoteResourceInfo(body{FlavorID: "flavor-1"})
	if err != nil {
		t.Fatalf("QuoteResourceInfo() error = %v", err)
	}
	if info["flavorId"] != "flavor-1" {
		t.Fatalf("flavorId = %v, want flavor-1", info["flavorId"])
	}
	if info["period"] != 1 {
		t.Fatalf("period = %v, want 1", info["period"])
	}
	if info["isPoc"] != false {
		t.Fatalf("isPoc = %v, want false", info["isPoc"])
	}
	if _, ok := info["userData"]; ok {
		t.Fatal("userData present, want omitted for an empty value")
	}
}

func TestQuoteResourceInfoRejectsUnmarshalable(t *testing.T) {
	if _, err := QuoteResourceInfo(make(chan int)); err == nil {
		t.Fatal("expected an error for a body that cannot marshal to JSON")
	}
}
