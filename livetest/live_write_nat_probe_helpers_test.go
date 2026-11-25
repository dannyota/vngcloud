//go:build livewrite

package livetest_test

import (
	"errors"
	"io"
	"os"

	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/network"
)

const (
	natDetailResult = "nat-detail-result"
	natRulesResult  = "nat-rules-result"
)

func natBillingRowsAbsent(rows []billing.Resource, id string) (bool, bool) {
	absent := true
	for _, row := range rows {
		if row.Product == "" || row.ArtifactType == "" || row.ArtifactID == "" {
			return false, false
		}
		if row.Product == "vserver" && row.ArtifactType == "nat" && row.ArtifactID == id {
			absent = false
		}
	}
	return absent, true
}

func natRouteIdentityValid(row network.RouteTable) bool { return row.UUID != "" && row.NetworkID != "" }

func natHoldResult(root *os.Root, name string) error {
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() {
		return errors.New("invalid NAT probe result")
	}
	file, err := natHoldOpenResult(root, name)
	if err != nil {
		return errors.New("invalid NAT probe result")
	}
	defer func() { _ = file.Close() }()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return errors.New("invalid NAT probe result")
	}
	data, err := io.ReadAll(io.LimitReader(file, 5))
	if err != nil || string(data) != "pass" {
		return errors.New("NAT probe result did not pass")
	}
	return nil
}
