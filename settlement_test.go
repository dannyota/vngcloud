package vngcloud_test

import (
	"errors"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/cdn"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/containerregistry"
	"danny.vn/vngcloud/database"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/globalloadbalancer"
	"danny.vn/vngcloud/iam"
	"danny.vn/vngcloud/loadbalancer"
	"danny.vn/vngcloud/network"
	"danny.vn/vngcloud/storage"
	"danny.vn/vngcloud/tagging"
	"danny.vn/vngcloud/vks"
	"danny.vn/vngcloud/volume"
)

func TestSettlementIdentities(t *testing.T) {
	t.Run("ErrNotSettled", func(t *testing.T) {
		values := []error{vngcloud.ErrNotSettled, cdn.ErrNotSettled, compute.ErrNotSettled, containerregistry.ErrNotSettled, database.ErrNotSettled, dns.ErrNotSettled, globalloadbalancer.ErrNotSettled, iam.ErrNotSettled, loadbalancer.ErrNotSettled, network.ErrNotSettled, storage.ErrNotSettled, tagging.ErrNotSettled, vks.ErrNotSettled, volume.ErrNotSettled}
		for _, a := range values {
			for _, b := range values {
				if a != b || !errors.Is(a, b) { //nolint:errorlint
					t.Errorf("settlement identities differ: %v, %v", a, b)
				}
			}
		}
	})
	t.Run("ErrFailed", func(t *testing.T) {
		values := []error{vngcloud.ErrFailed, compute.ErrFailed, database.ErrFailed, dns.ErrFailed, globalloadbalancer.ErrFailed, loadbalancer.ErrFailed, network.ErrFailed, storage.ErrFailed, vks.ErrFailed, volume.ErrFailed}
		for _, a := range values {
			for _, b := range values {
				if a != b || !errors.Is(a, b) { //nolint:errorlint
					t.Errorf("settlement identities differ: %v, %v", a, b)
				}
			}
		}
	})
}
