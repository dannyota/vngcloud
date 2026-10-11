package network

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/pricing"
)

// ListNetworkPricesInput bounds public-IP discovery.
type ListNetworkPricesInput struct {
	MaxRequests  int
	AllowPartial bool
}

// ListNetworkPricesOutput contains account quotes and unknown IP charges.
type ListNetworkPricesOutput struct {
	Items    []NetworkPrice
	Metadata pricing.DiscoveryMetadata
}

// NetworkPrice describes one distinct IP charge scope.
type NetworkPrice struct {
	Kind  string
	Name  string
	Type  string
	Scope string
	Unit  string
	Price pricing.DiscoveryPrice
}

// ListNetworkPrices quotes three public-VIP types separately. Other IP charges
// remain unknown. Quotes reserve no capacity or future price.
func (c *Client) ListNetworkPrices(ctx context.Context, in *ListNetworkPricesInput) (*ListNetworkPricesOutput, error) {
	if in == nil {
		in = &ListNetworkPricesInput{}
	}
	s, err := discovery.New(ctx, in.MaxRequests)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", core.ErrInvalidInput, err)
	}
	defer s.Close()
	if _, err = c.c.RequireDiscoveryProjectID(s); err != nil {
		return nil, err
	}
	if err = s.Preflight(3); err != nil {
		return nil, err
	}
	out := &ListNetworkPricesOutput{Items: []NetworkPrice{}, Metadata: pricing.DiscoveryMetadata{Region: c.c.Region(), Complete: true, Failures: []pricing.DiscoveryFailure{}, Notes: networkDiscoveryNotes()}}
	errs := []error{}
	usable := false
	for _, kind := range []string{"public", "public-vm", "public-mkp"} {
		row := NetworkPrice{Kind: "public-vip", Name: "Public Virtual IP (" + kind + ")", Type: kind, Scope: c.c.Region(), Unit: "IP-month", Price: pricing.DiscoveryPrice{Status: "failed", Source: "pricing.GetQuote"}}
		req := transport.Request{Operation: "pricing.GetQuote", Method: http.MethodPost, URL: c.c.RouteURL(routes.Route{Product: routes.ProductPortal, Version: "v1", Parts: []string{"price"}}), OK: []int{200}, Body: map[string]any{"resourceType": "public-vip", "action": "create", "resourceInfo": map[string]any{"type": kind, "isBuyMorePo": false, "isPoc": false}}}
		_, err = s.DoJSON(c.c, req, nil, func(status int, raw json.RawMessage) error {
			price, e := decodeNetworkDiscoveryPrice(req.Operation, status, raw)
			if e == nil {
				row.Price = price
			}
			return e
		})
		if err != nil {
			if !in.AllowPartial {
				return nil, err
			}
			errs = append(errs, err)
			out.Metadata.Complete = false
			failure := pricing.DiscoveryFailure{Operation: req.Operation, ItemKey: kind, Code: "DiscoveryIncomplete"}
			var api *core.APIError
			if errors.As(err, &api) {
				failure.Status = api.StatusCode
				failure.Code = core.ResolvedCode(api.StatusCode, "")
				if api.Code == "InvalidResponse" {
					failure.Code = api.Code
				}
			}
			out.Metadata.Failures = append(out.Metadata.Failures, failure)
		} else {
			usable = true
		}
		out.Items = append(out.Items, row)
		if err != nil && (c.c.DiscoveryFatal(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, discovery.ErrLimit)) {
			break
		}
	}
	if !usable && len(errs) > 0 {
		return nil, errs[0]
	}
	out.Items = append(out.Items, []NetworkPrice{
		{Kind: "floating-ip", Name: "Floating IP", Scope: c.c.Region(), Unit: "IP-month", Price: pricing.DiscoveryPrice{Status: "unknown", Source: "https://docs.greennode.ai/vserver/compute-hcm03-1a/vpc/floating-ip.md"}},
		{Kind: "external-interface", Name: "External Interface", Scope: c.c.Region(), Unit: "IP-month", Price: pricing.DiscoveryPrice{Status: "unknown", Source: "https://docs.greennode.ai/vserver/compute-hcm03-1a/vpc/instance-ip-address.md"}},
		{Kind: "reservation", Name: "Floating IP reservation", Scope: c.c.Region(), Unit: "IP-month", Price: pricing.DiscoveryPrice{Status: "unknown", Source: "https://docs.greennode.ai/vserver/compute-hcm03-1a/vpc/floating-ip.md"}},
	}...)
	slices.SortStableFunc(out.Items, func(a, b NetworkPrice) int { return cmp.Compare(a.Type, b.Type) })
	out.Metadata.Requests = s.Requests()
	out.Metadata.QuotedAt = s.CompletedAt()
	if len(errs) > 0 {
		return out, &pricing.DiscoveryError{Errors: errs}
	}
	return out, nil
}

func networkDiscoveryNotes() []pricing.PricingNote {
	notes := slices.DeleteFunc(pricing.DiscoveryNotes(), func(n pricing.PricingNote) bool { return n.Scope != "network" && n.Topic != "account-scope" })
	return append(notes,
		pricing.PricingNote{Topic: "quote-scope", Scope: "public-vip", Text: "Each public-VIP type has a separate account quote. Equal amounts do not establish interchangeable products.", Evidence: "observed"},
		pricing.PricingNote{Topic: "unknown-ip-charges", Scope: "network", Text: "Floating IP, external-interface, and reservation charges remain unknown. Reservation affects Floating IP billing; detached External Interfaces keep billing until release.", SourceURL: "https://docs.greennode.ai/vserver/compute-hcm03-1a/vpc/instance-ip-address.md", Evidence: "published"},
		pricing.PricingNote{Topic: "nat", Scope: "network", Text: "NAT costs are excluded. Use network list-nat-zones, list-nat-packages, and quote-create-nat-instance separately.", Evidence: "unverified"})
}
