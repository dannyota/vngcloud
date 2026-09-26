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

	// writeLock serializes every zone write within one Client, from its
	// pre-write read (when it has one) to the end of the call, including
	// any post-write wait: two goroutines sharing a Client must never both
	// read the zone as ready and then write it at the same time. It does
	// not, and cannot, prevent the same race across two processes or two
	// Clients; the design leaves that to the caller.
	//
	// It is a 1-slot channel rather than a sync.Mutex so a caller whose ctx
	// ends while waiting for it can give up instead of blocking until the
	// holder releases it; see lockWrite.
	writeLock chan struct{}

	// sleep waits for d or ctx's end, whichever comes first, between poll
	// reads in a wait. Tests replace it with a fake so the real 2-second and
	// 60-second waits never really elapse.
	sleep sleepFunc

	// now reads the current time. A wait's poll uses it, alongside sleep, to
	// bound itself by elapsed wall time; tests replace it with a fake clock.
	now clockFunc
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg), writeLock: make(chan struct{}, 1), sleep: contextSleep, now: time.Now}
}

// lockWrite acquires c's write lock, honoring ctx: if ctx ends before the
// lock is free, it returns ctx.Err() without ever taking the lock, so a
// caller that gives up waiting never steals the lock out from under
// whichever goroutine already holds it, and never blocks a later caller's
// own acquisition.
func (c *Client) lockWrite(ctx context.Context) error {
	select {
	case c.writeLock <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// unlockWrite releases c's write lock. It must be called exactly once for
// every lockWrite call that returned nil.
func (c *Client) unlockWrite() {
	<-c.writeLock
}

// url builds a URL under the DNS endpoint.
func (c *Client) url(parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{Product: routes.ProductDNS, Version: "v1", Parts: parts, Query: q})
}

// ListHostedZonesInput's Page and Size page through the account's zones; a
// caller that must see every zone, such as a cleanup routine, loops from
// Page 1 to the Output's TotalPage rather than assuming one call returns
// them all.
type ListHostedZonesInput struct {
	Name string
	Page int
	Size int
}

type ListHostedZonesOutput = core.PagedList[HostedZone]

func (c *Client) ListHostedZones(ctx context.Context, in *ListHostedZonesInput) (*ListHostedZonesOutput, error) {
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	q := core.PageQuery(page, size)
	if name != "" {
		q.Set("name", name)
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
	const op = "dns.GetHostedZone"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "HostedZoneID", in.HostedZoneID); err != nil {
		return nil, err
	}
	var resp struct {
		Data HostedZone `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
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
	const op = "dns.ListRecords"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "HostedZoneID", in.HostedZoneID); err != nil {
		return nil, err
	}
	q := url.Values{}
	if in.Name != "" {
		q.Set("name", in.Name)
	}
	var resp listRecordsResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
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
	const op = "dns.GetRecord"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "HostedZoneID", in.HostedZoneID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "RecordID", in.RecordID); err != nil {
		return nil, err
	}
	var resp struct {
		Data Record `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
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
	Location *string `json:"location,omitempty"`
	Weight   *int    `json:"weight,omitempty"`
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
