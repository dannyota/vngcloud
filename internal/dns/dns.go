package dns

import (
	"context"
	"errors"
	"net/url"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

type Service struct {
	client *core.Client
}

func New(client *core.Client) *Service {
	return &Service{client: client}
}

type ListHostedZonesOptions struct {
	Name string
}

type ListRecordsOptions struct {
	Name string
}

type ListHostedZonesResult = core.ListResult[HostedZone]
type ListDNSRecordsResult = core.ListResult[DNSRecord]

func (s *Service) ListHostedZones(ctx context.Context, opts *ListHostedZonesOptions) (*ListHostedZonesResult, error) {
	q := url.Values{}
	if opts != nil && opts.Name != "" {
		q.Set("name", opts.Name)
	}
	var resp listHostedZonesResponse
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "DNS.ListHostedZones",
		Method:    "GET",
		URL:       s.dnsURL([]string{"dns", "hosted-zone"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) GetHostedZone(ctx context.Context, id string) (*HostedZone, error) {
	if id == "" {
		return nil, errors.New("vngcloud: hosted zone id is required")
	}
	var resp struct {
		Data HostedZone `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "DNS.GetHostedZone",
		Method:    "GET",
		URL:       s.dnsURL([]string{"dns", "hosted-zone", id}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (s *Service) ListRecords(ctx context.Context, hostedZoneID string, opts *ListRecordsOptions) (*ListDNSRecordsResult, error) {
	if hostedZoneID == "" {
		return nil, errors.New("vngcloud: hosted zone id is required")
	}
	q := url.Values{}
	if opts != nil && opts.Name != "" {
		q.Set("name", opts.Name)
	}
	var resp listDNSRecordsResponse
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "DNS.ListRecords",
		Method:    "GET",
		URL:       s.dnsURL([]string{"dns", "hosted-zone", hostedZoneID, "record"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) GetRecord(ctx context.Context, hostedZoneID, recordID string) (*DNSRecord, error) {
	if hostedZoneID == "" {
		return nil, errors.New("vngcloud: hosted zone id is required")
	}
	if recordID == "" {
		return nil, errors.New("vngcloud: record id is required")
	}
	var resp struct {
		Data DNSRecord `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "DNS.GetRecord",
		Method:    "GET",
		URL:       s.dnsURL([]string{"dns", "hosted-zone", hostedZoneID, "record", recordID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (s *Service) dnsURL(parts []string, q url.Values) string {
	return s.client.RouteURL(routes.Route{
		Product: routes.ProductDNS,
		Version: "v1",
		Parts:   parts,
		Query:   q,
	})
}

type listHostedZonesResponse struct {
	ListData  []HostedZone `json:"listData"`
	Page      int          `json:"page"`
	PageSize  int          `json:"pageSize"`
	TotalPage int          `json:"totalPage"`
	TotalItem int          `json:"totalItem"`
}

type listDNSRecordsResponse struct {
	ListData  []DNSRecord `json:"listData"`
	Page      int         `json:"page"`
	PageSize  int         `json:"pageSize"`
	TotalPage int         `json:"totalPage"`
	TotalItem int         `json:"totalItem"`
}

type VpcMapRegion struct {
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
	AssocVpcMapRegion []VpcMapRegion `json:"assocVpcMapRegion"`
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

type DNSRecord struct {
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
