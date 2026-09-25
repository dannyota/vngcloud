// Package dns lists and reads vDNS hosted zones and records.
package dns

import (
	"context"
	"net/url"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Client is the DNS service client.
type Client struct {
	c *core.Client
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

// url builds a URL under the DNS endpoint.
func (c *Client) url(parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{Product: routes.ProductDNS, Version: "v1", Parts: parts, Query: q})
}

type ListHostedZonesInput struct {
	Name string
}

type ListHostedZonesOutput = core.PagedList[HostedZone]

func (c *Client) ListHostedZones(ctx context.Context, in *ListHostedZonesInput) (*ListHostedZonesOutput, error) {
	q := url.Values{}
	if in != nil && in.Name != "" {
		q.Set("name", in.Name)
	}
	var resp listHostedZonesResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "dns.ListHostedZones",
		Method:    "GET",
		URL:       c.url([]string{"dns", "hosted-zone"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

type GetHostedZoneInput struct {
	HostedZoneID string `vngcloud:"required"`
}

type GetHostedZoneOutput struct {
	HostedZone HostedZone
}

func (c *Client) GetHostedZone(ctx context.Context, in *GetHostedZoneInput) (*GetHostedZoneOutput, error) {
	if err := core.CheckRequired("dns.GetHostedZone", in); err != nil {
		return nil, err
	}
	var resp struct {
		Data HostedZone `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "dns.GetHostedZone",
		Method:    "GET",
		URL:       c.url([]string{"dns", "hosted-zone", in.HostedZoneID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetHostedZoneOutput{HostedZone: resp.Data}, nil
}

type ListRecordsInput struct {
	HostedZoneID string `vngcloud:"required"`
	Name         string
}

type ListRecordsOutput = core.PagedList[Record]

func (c *Client) ListRecords(ctx context.Context, in *ListRecordsInput) (*ListRecordsOutput, error) {
	if err := core.CheckRequired("dns.ListRecords", in); err != nil {
		return nil, err
	}
	q := url.Values{}
	if in.Name != "" {
		q.Set("name", in.Name)
	}
	var resp listRecordsResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "dns.ListRecords",
		Method:    "GET",
		URL:       c.url([]string{"dns", "hosted-zone", in.HostedZoneID, "record"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

type GetRecordInput struct {
	HostedZoneID string `vngcloud:"required"`
	RecordID     string `vngcloud:"required"`
}

type GetRecordOutput struct {
	Record Record
}

func (c *Client) GetRecord(ctx context.Context, in *GetRecordInput) (*GetRecordOutput, error) {
	if err := core.CheckRequired("dns.GetRecord", in); err != nil {
		return nil, err
	}
	var resp struct {
		Data Record `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "dns.GetRecord",
		Method:    "GET",
		URL:       c.url([]string{"dns", "hosted-zone", in.HostedZoneID, "record", in.RecordID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetRecordOutput{Record: resp.Data}, nil
}

type listHostedZonesResponse struct {
	ListData  []HostedZone `json:"listData"`
	Page      int          `json:"page"`
	PageSize  int          `json:"pageSize"`
	TotalPage int          `json:"totalPage"`
	TotalItem int          `json:"totalItem"`
}

type listRecordsResponse struct {
	ListData  []Record `json:"listData"`
	Page      int      `json:"page"`
	PageSize  int      `json:"pageSize"`
	TotalPage int      `json:"totalPage"`
	TotalItem int      `json:"totalItem"`
}

type VPCMapRegion struct {
	VPCID  string `json:"vpcId"`
	Region string `json:"region"`
}

type HostedZone struct {
	ID                string         `json:"hostedZoneId"`
	DomainName        string         `json:"domainName"`
	Status            string         `json:"status"`
	Description       string         `json:"description"`
	Type              string         `json:"type"`
	CountRecords      int            `json:"countRecords"`
	AssociatedVPCIDs  []string       `json:"assocVpcIds"`
	AssocVPCMapRegion []VPCMapRegion `json:"assocVpcMapRegion"`
	PortalUserID      int            `json:"portalUserId"`
	CreatedAt         time.Time      `json:"createdAt"`
	DeletedAt         *time.Time     `json:"deletedAt"`
	UpdatedAt         time.Time      `json:"updatedAt"`
}

type RecordValue struct {
	Value    string  `json:"value"`
	Location *string `json:"location"`
	Weight   *int    `json:"weight"`
}

type Record struct {
	ID                  string        `json:"recordId"`
	SubDomain           string        `json:"subDomain"`
	HostedZoneID        string        `json:"hostedZoneId"`
	Status              string        `json:"status"`
	Type                string        `json:"type"`
	RoutingPolicy       string        `json:"routingPolicy"`
	Value               []RecordValue `json:"value"`
	TTL                 int           `json:"ttl"`
	EnableStickySession *bool         `json:"enableStickySession"`
	CreatedAt           time.Time     `json:"createdAt"`
	DeletedAt           *time.Time    `json:"deletedAt"`
	UpdatedAt           time.Time     `json:"updatedAt"`
}
