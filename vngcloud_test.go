package vngcloud_test

import (
	"testing"

	"danny.vn/vngcloud"
)

func TestPublicFacadeCompile(t *testing.T) {
	_ = vngcloud.Config{}
	_ = &vngcloud.IAMUserAuth{}

	if vngcloud.NewConfig == nil {
		t.Fatal("NewConfig is nil")
	}
}

func TestPtr(t *testing.T) {
	p := vngcloud.Ptr(false)
	if *p != false {
		t.Fatalf("*Ptr(false) = %v, want false", *p)
	}
	q := vngcloud.Ptr(false)
	if p == q {
		t.Fatal("Ptr returned the same pointer for two calls")
	}
}
