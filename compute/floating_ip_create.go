package compute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/floatingip"
	"danny.vn/vngcloud/pricing"
)

func (c *Client) floatingCreateQuote(ctx context.Context, in *CreateServerInput, info map[string]any) (*pricing.GetQuoteOutput, error) {
	if !in.AttachFloatingIP {
		return c.serverQuote(ctx, &pricing.GetQuoteInput{ResourceType: pricing.ResourceServer, Action: pricing.ActionCreate, ResourceInfo: info})
	}
	quote, err := floatingip.QuoteCreate(ctx, func(ctx context.Context) (*floatingip.Quote, error) {
		q, err := c.serverQuote(ctx, &pricing.GetQuoteInput{ResourceType: pricing.ResourceServer, Action: pricing.ActionCreate, ResourceInfo: info})
		if err != nil {
			return nil, err
		}
		out := &floatingip.Quote{OptimumPrice: q.OptimumPrice, OriginalPrice: q.OriginalPrice, DiscountPrice: q.DiscountPrice, DiscountPercent: q.DiscountPercent}
		for _, p := range q.Properties {
			out.Properties = append(out.Properties, floatingip.Property(p))
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	out := &pricing.GetQuoteOutput{OptimumPrice: quote.OptimumPrice, OriginalPrice: quote.OriginalPrice, DiscountPrice: quote.DiscountPrice, DiscountPercent: quote.DiscountPercent}
	for _, p := range quote.Properties {
		out.Properties = append(out.Properties, pricing.PriceProperty(p))
	}
	return out, nil
}

func floatingAddressSettled(server *Server) bool {
	if server == nil || !strings.EqualFold(server.Status, serverStatusActive) || server.PublicIP == "" {
		return false
	}
	var id string
	for _, nic := range server.InternalInterfaces {
		if nic.FloatingIP == "" {
			if nic.FloatingIPID != "" {
				return false
			}
			continue
		}
		ip, err := netip.ParseAddr(nic.FloatingIP)
		if err != nil || ip.Unmap().String() != server.PublicIP || core.CheckPathID("compute.CreateServer", "FloatingIPID", nic.FloatingIPID) != nil {
			return false
		}
		if id != "" && id != nic.FloatingIPID {
			return false
		}
		id = nic.FloatingIPID
	}
	return id != ""
}

func (c *Client) waitFloatingAddress(ctx context.Context, op, id string, initial *Server) (result *Server, resultErr error) {
	ctx, scope := serverOperationContext(ctx)
	defer finishServerOperation(scope, op, &result, &resultErr)
	if initial == nil || initial.UUID != id {
		return &Server{UUID: id}, fmt.Errorf("%w: %w", ErrNotSettled, invalidServerResponse(op, 0, nil))
	}
	if floatingAddressSettled(initial) {
		return initial, nil
	}
	result = initial
	err := poll(ctx, c.now, c.sleep, 2*time.Minute, func(ctx context.Context) (bool, error) {
		out, err := c.GetServer(ctx, &GetServerInput{ServerID: id})
		if err != nil {
			return true, err
		}
		if out.Server.UUID != id {
			return true, invalidServerResponse(op, 0, nil)
		}
		result = &out.Server
		if isServerError(result.Status) {
			return true, fmt.Errorf("%w: server reached ERROR", ErrFailed)
		}
		return floatingAddressSettled(result), nil
	}, func() error {
		return fmt.Errorf("%w: floating address did not settle within 2m; inspect get-server or list-servers before any new create", ErrNotSettled)
	})
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: inspect get-server or list-servers before any new create: %w", ErrNotSettled, err)
	}
	return result, err
}

// watchFloatingQuote rechecks retained quote fields whenever a later request
// observes a credential, including the send-once order's own credential.
func watchFloatingQuote(ctx context.Context, scope *serverResponseScope, quote *pricing.GetQuoteOutput) (context.Context, error) {
	raw, err := json.Marshal(quote)
	if err != nil || scope.unsafe(raw) {
		return ctx, invalidServerResponse("compute.CreateServer", 0, err)
	}
	return floatingip.WithObserver(ctx, func(value string) ([]string, error) {
		values, err := scope.observe(value)
		if err != nil {
			return nil, err
		}
		if floatingip.Reflects(raw, values) {
			return nil, invalidServerResponse("compute.CreateServer", 0, nil)
		}
		return values, nil
	}), nil
}
