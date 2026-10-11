package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

func natRegionalOrigin(region string) string {
	switch region {
	case "hcm-3":
		return "https://hcm-3-vnetwork.console.greennode.ai"
	case "han-1":
		return "https://han-1-vnetwork.console.greennode.ai"
	default:
		return ""
	}
}

func (c *Client) natEndpoint() (string, error) {
	if natRegionalOrigin(c.c.Region()) == "" {
		return "", fmt.Errorf("%w: %s supports hcm-3 and han-1", core.ErrInvalidConfig, listNATOperation)
	}
	if c.c.VNetworkOverride() {
		return c.c.Endpoint(routes.ProductVNet), nil
	}
	return natRegionalOrigin(c.c.Region()) + "/vnetwork-gateway/", nil
}

// NAT discovery never updates the shared endpoint cache or follows a supplied host.
func (c *Client) natZoneID(ctx context.Context, base string) (string, error) {
	return c.natZoneIDWithPrivacy(ctx, base, true)
}

func (c *Client) natZoneIDWithPrivacy(ctx context.Context, base string, sensitive bool) (string, error) {
	withhold := ""
	if sensitive {
		withhold = "NAT response withheld"
	}
	var raw json.RawMessage
	var credential string
	status, err := c.c.DoJSONStatus(ctx, transport.Request{MaxBody: natMaxBody, SentCredential: &credential, Sensitive: sensitive, NoRedirect: sensitive, WithholdMessage: withhold, Operation: "network.ListVNetworkRegions", Method: http.MethodGet, URL: routes.URL(fixedVNetEndpoint{base: base}, routes.Route{Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: []string{"regions"}}), OK: []int{http.StatusOK}}, &raw)
	if err != nil {
		if errors.Is(err, transport.ErrBodyTooLarge) {
			return "", invalidNATResponse("oversized regions envelope")
		}
		if status == http.StatusOK {
			return "", invalidNATResponse("invalid regions envelope")
		}
		return "", err
	}
	var resp natRegionsResponse
	if natReflectsCredential(raw, credential) || json.Unmarshal(raw, &resp) != nil {
		return "", invalidNATResponse("invalid regions envelope")
	}
	if resp.Success == nil || !*resp.Success || len(resp.Data) == 0 || string(resp.Data) == "null" {
		return "", invalidNATResponse("invalid regions envelope")
	}
	var regions []VNetworkRegion
	if err := json.Unmarshal(resp.Data, &regions); err != nil {
		return "", invalidNATResponse("invalid regions data")
	}
	origin := natRegionalOrigin(c.c.Region())
	zoneID := ""
	matches := 0
	for _, region := range regions {
		dashboard := strings.TrimRight(region.DashboardURL, "/")
		gateway := strings.TrimRight(region.GatewayURL, "/")
		if dashboard != origin && gateway != origin+"/vnetwork-gateway" {
			continue
		}
		// Conflicting regional origins cannot establish a unique zone mapping.
		other := natRegionalOrigin("hcm-3")
		if c.c.Region() == "hcm-3" {
			other = natRegionalOrigin("han-1")
		}
		if dashboard == other || gateway == other+"/vnetwork-gateway" {
			return "", fmt.Errorf("%w: conflicting NAT region mapping", core.ErrInvalidConfig)
		}
		matches++
		if err := core.CheckPathID(listNATOperation, "ZoneID", region.UUID); err != nil {
			return "", fmt.Errorf("%w: invalid discovered NAT zone", core.ErrInvalidConfig)
		}
		zoneID = region.UUID
	}
	if matches != 1 {
		return "", fmt.Errorf("%w: no unique NAT zone for selected region", core.ErrInvalidConfig)
	}
	return zoneID, nil
}
