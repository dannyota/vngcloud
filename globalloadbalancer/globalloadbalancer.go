// Package globalloadbalancer lists and reads GLB global load balancers,
// pools, listeners, and usage histories.
package globalloadbalancer

import (
	"context"
	"net/url"
	"strconv"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Client is the global load balancer service client.
type Client struct {
	c *core.Client
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

type ListPackagesInput struct{}

type ListPackagesOutput = core.List[Package]

func (c *Client) ListPackages(ctx context.Context, _ *ListPackagesInput) (*ListPackagesOutput, error) {
	var resp []Package
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "globalloadbalancer.ListPackages",
		Method:    "GET",
		URL:       c.glbURL([]string{"packages"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListPackagesOutput{Items: resp}, nil
}

type ListRegionsInput struct{}

type ListRegionsOutput = core.List[Region]

func (c *Client) ListRegions(ctx context.Context, _ *ListRegionsInput) (*ListRegionsOutput, error) {
	var resp []Region
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "globalloadbalancer.ListRegions",
		Method:    "GET",
		URL:       c.glbURL([]string{"regions"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListRegionsOutput{Items: resp}, nil
}

type ListLoadBalancersInput struct {
	Name   string
	Offset int
	Limit  int
}

type ListLoadBalancersOutput struct {
	Items  []LoadBalancer
	Offset int
	Limit  int
	Total  int
}

func (c *Client) ListLoadBalancers(ctx context.Context, in *ListLoadBalancersInput) (*ListLoadBalancersOutput, error) {
	offset, limit := 0, core.DefaultPageSize
	name := ""
	if in != nil {
		name = in.Name
		if in.Offset >= 0 {
			offset = in.Offset
		}
		if in.Limit > 0 {
			limit = in.Limit
		}
	}
	q := url.Values{}
	q.Set("name", name)
	q.Set("offset", strconv.Itoa(offset))
	q.Set("limit", strconv.Itoa(limit))
	var resp listLoadBalancersResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "globalloadbalancer.ListLoadBalancers",
		Method:    "GET",
		URL:       c.glbURL([]string{"global-load-balancers"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListLoadBalancersOutput{Items: resp.Items, Offset: resp.Offset, Limit: resp.Limit, Total: resp.Total}, nil
}

type GetLoadBalancerInput struct {
	LoadBalancerID string `vngcloud:"required"`
}

type GetLoadBalancerOutput struct {
	LoadBalancer LoadBalancer
}

func (c *Client) GetLoadBalancer(ctx context.Context, in *GetLoadBalancerInput) (*GetLoadBalancerOutput, error) {
	if err := core.CheckRequired("globalloadbalancer.GetLoadBalancer", in); err != nil {
		return nil, err
	}
	var resp LoadBalancer
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "globalloadbalancer.GetLoadBalancer",
		Method:    "GET",
		URL:       c.glbURL([]string{"global-load-balancers", in.LoadBalancerID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetLoadBalancerOutput{LoadBalancer: resp}, nil
}

type ListPoolsInput struct {
	LoadBalancerID string `vngcloud:"required"`
}

type ListPoolsOutput = core.List[Pool]

func (c *Client) ListPools(ctx context.Context, in *ListPoolsInput) (*ListPoolsOutput, error) {
	if err := core.CheckRequired("globalloadbalancer.ListPools", in); err != nil {
		return nil, err
	}
	items, err := listGLBChild[Pool](c, ctx, "globalloadbalancer.ListPools", in.LoadBalancerID, []string{"global-pools"})
	if err != nil {
		return nil, err
	}
	return &ListPoolsOutput{Items: items}, nil
}

type ListListenersInput struct {
	LoadBalancerID string `vngcloud:"required"`
}

type ListListenersOutput = core.List[Listener]

func (c *Client) ListListeners(ctx context.Context, in *ListListenersInput) (*ListListenersOutput, error) {
	if err := core.CheckRequired("globalloadbalancer.ListListeners", in); err != nil {
		return nil, err
	}
	items, err := listGLBChild[Listener](c, ctx, "globalloadbalancer.ListListeners", in.LoadBalancerID, []string{"global-listeners"})
	if err != nil {
		return nil, err
	}
	return &ListListenersOutput{Items: items}, nil
}

type GetListenerInput struct {
	LoadBalancerID string `vngcloud:"required"`
	ListenerID     string `vngcloud:"required"`
}

type GetListenerOutput struct {
	Listener Listener
}

func (c *Client) GetListener(ctx context.Context, in *GetListenerInput) (*GetListenerOutput, error) {
	if err := core.CheckRequired("globalloadbalancer.GetListener", in); err != nil {
		return nil, err
	}
	item, err := getGLBChild[Listener](c, ctx, "globalloadbalancer.GetListener", in.LoadBalancerID, []string{"global-listeners", in.ListenerID})
	if err != nil {
		return nil, err
	}
	return &GetListenerOutput{Listener: *item}, nil
}

type ListPoolMembersInput struct {
	LoadBalancerID string `vngcloud:"required"`
	PoolID         string `vngcloud:"required"`
}

type ListPoolMembersOutput = core.List[PoolMember]

func (c *Client) ListPoolMembers(ctx context.Context, in *ListPoolMembersInput) (*ListPoolMembersOutput, error) {
	if err := core.CheckRequired("globalloadbalancer.ListPoolMembers", in); err != nil {
		return nil, err
	}
	items, err := listGLBChild[PoolMember](c, ctx, "globalloadbalancer.ListPoolMembers", in.LoadBalancerID, []string{"global-pools", in.PoolID, "pool-members"})
	if err != nil {
		return nil, err
	}
	return &ListPoolMembersOutput{Items: items}, nil
}

type GetPoolMemberInput struct {
	LoadBalancerID string `vngcloud:"required"`
	PoolID         string `vngcloud:"required"`
	PoolMemberID   string `vngcloud:"required"`
}

type GetPoolMemberOutput struct {
	PoolMember PoolMember
}

func (c *Client) GetPoolMember(ctx context.Context, in *GetPoolMemberInput) (*GetPoolMemberOutput, error) {
	if err := core.CheckRequired("globalloadbalancer.GetPoolMember", in); err != nil {
		return nil, err
	}
	item, err := getGLBChild[PoolMember](c, ctx, "globalloadbalancer.GetPoolMember", in.LoadBalancerID, []string{"global-pools", in.PoolID, "pool-members", in.PoolMemberID})
	if err != nil {
		return nil, err
	}
	return &GetPoolMemberOutput{PoolMember: *item}, nil
}

type ListUsageHistoriesInput struct {
	LoadBalancerID string `vngcloud:"required"`
	From           string
	To             string
	Type           string
}

type ListUsageHistoriesOutput struct {
	Type  string
	From  string
	To    string
	Items []UsageHistory
}

func (c *Client) ListUsageHistories(ctx context.Context, in *ListUsageHistoriesInput) (*ListUsageHistoriesOutput, error) {
	if err := core.CheckRequired("globalloadbalancer.ListUsageHistories", in); err != nil {
		return nil, err
	}
	q := url.Values{}
	if in.From != "" {
		q.Set("from", in.From)
	}
	if in.To != "" {
		q.Set("to", in.To)
	}
	if in.Type != "" {
		q.Set("type", in.Type)
	}
	var resp listUsageHistoriesResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "globalloadbalancer.ListUsageHistories",
		Method:    "GET",
		URL:       c.glbURL([]string{"global-load-balancers", in.LoadBalancerID, "usage-histories"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListUsageHistoriesOutput{Type: resp.Type, From: resp.From, To: resp.To, Items: resp.Items}, nil
}

func listGLBChild[T any](c *Client, ctx context.Context, operation, loadBalancerID string, childParts []string) ([]T, error) {
	parts := append([]string{"global-load-balancers", loadBalancerID}, childParts...)
	var resp []T
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: operation,
		Method:    "GET",
		URL:       c.glbURL(parts, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func getGLBChild[T any](c *Client, ctx context.Context, operation, loadBalancerID string, childParts []string) (*T, error) {
	parts := append([]string{"global-load-balancers", loadBalancerID}, childParts...)
	var resp T
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: operation,
		Method:    "GET",
		URL:       c.glbURL(parts, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) glbURL(parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{
		Product: routes.ProductGLB,
		Version: "v1",
		Parts:   parts,
		Query:   q,
	})
}

// listLoadBalancersResponse decodes the API's envelope for ListLoadBalancers.
type listLoadBalancersResponse struct {
	Items  []LoadBalancer `json:"items"`
	Limit  int            `json:"limit"`
	Total  int            `json:"total"`
	Offset int            `json:"offset"`
}

// listUsageHistoriesResponse decodes the API's envelope for
// ListUsageHistories.
type listUsageHistoriesResponse struct {
	Type  string         `json:"type"`
	Items []UsageHistory `json:"items"`
	From  string         `json:"from"`
	To    string         `json:"to"`
}

type Package struct {
	ID                          string            `json:"id"`
	Name                        string            `json:"name"`
	Description                 string            `json:"description"`
	DescriptionEn               string            `json:"descriptionEn"`
	Enabled                     bool              `json:"enabled"`
	BaseSKU                     string            `json:"baseSku"`
	ConnectionSKU               string            `json:"connectionSku"`
	BaseConnectionRate          int               `json:"baseConnectionRate"`
	BaseDomesticTrafficTotal    int               `json:"baseDomesticTrafficTotal"`
	BaseNonDomesticTrafficTotal int               `json:"baseNonDomesticTrafficTotal"`
	DomesticTrafficSKU          string            `json:"domesticTrafficSku"`
	NonDomesticTrafficSKU       string            `json:"nonDomesticTrafficSku"`
	Detail                      any               `json:"detail"`
	CreatedAt                   string            `json:"createdAt"`
	UpdatedAt                   string            `json:"updatedAt"`
	VLBPackages                 []RegionalPackage `json:"vlbPackages"`
}

type RegionalPackage struct {
	ID           int    `json:"id"`
	GLBPackageID string `json:"glb_package_id"`
	Region       string `json:"region"`
	VLBPackageID string `json:"vlb_package_id"`
	CreatedAt    string `json:"created_at"`
}

type Region struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Status           string `json:"status"`
	VServerEndpoint  string `json:"vserverEndpoint"`
	VLBEndpoint      string `json:"vlbEndpoint"`
	UIServerEndpoint string `json:"uiServerEndpoint"`
}

type LoadBalancer struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Package     string   `json:"package"`
	Type        string   `json:"type"`
	UserID      int      `json:"userId"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
	DeletedAt   string   `json:"deletedAt"`
	VIPs        []VIP    `json:"vips"`
	Domains     []Domain `json:"domains"`
}

type VIP struct {
	ID                   int    `json:"id"`
	Address              string `json:"address"`
	Status               string `json:"status"`
	Region               string `json:"region"`
	GlobalLoadBalancerID string `json:"globalLoadBalancerId"`
	CreatedAt            string `json:"createdAt"`
	UpdatedAt            string `json:"updatedAt"`
	DeletedAt            string `json:"deletedAt"`
}

type Domain struct {
	ID                   int    `json:"id"`
	Hostname             string `json:"hostname"`
	Status               string `json:"status"`
	GlobalLoadBalancerID string `json:"globalLoadBalancerId"`
	DNSHostedZoneID      string `json:"dnsHostedZoneId"`
	DNSServerID          string `json:"dnsServerId"`
	CreatedAt            string `json:"createdAt"`
	UpdatedAt            string `json:"updatedAt"`
	DeletedAt            string `json:"deletedAt"`
}

type Pool struct {
	ID                   string             `json:"id"`
	Name                 string             `json:"name"`
	Description          string             `json:"description"`
	GlobalLoadBalancerID string             `json:"globalLoadBalancerId"`
	Algorithm            string             `json:"algorithm"`
	StickySession        *string            `json:"stickySession"`
	TLSEnabled           *string            `json:"tlsEnabled"`
	Protocol             string             `json:"protocol"`
	Status               string             `json:"status"`
	Health               *PoolHealthMonitor `json:"health"`
	CreatedAt            string             `json:"createdAt"`
	UpdatedAt            string             `json:"updatedAt"`
	DeletedAt            *string            `json:"deletedAt"`
}

type PoolHealthMonitor struct {
	ID                   string  `json:"id"`
	GlobalPoolID         string  `json:"globalPoolId"`
	GlobalLoadBalancerID string  `json:"globalLoadBalancerId"`
	Protocol             string  `json:"protocol"`
	Path                 *string `json:"path"`
	Timeout              int     `json:"timeout"`
	IntervalTime         int     `json:"intervalTime"`
	HealthyThreshold     int     `json:"healthyThreshold"`
	UnhealthyThreshold   int     `json:"unhealthyThreshold"`
	HTTPVersion          *string `json:"httpVersion"`
	HTTPMethod           *string `json:"httpMethod"`
	DomainName           *string `json:"domainName"`
	SuccessCode          *string `json:"successCode"`
	Status               string  `json:"status"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
	DeletedAt            *string `json:"deletedAt"`
}

type PoolMember struct {
	ID                   string             `json:"id"`
	Name                 string             `json:"name"`
	Description          string             `json:"description"`
	Region               string             `json:"region"`
	GlobalPoolID         string             `json:"globalPoolId"`
	GlobalLoadBalancerID string             `json:"globalLoadBalancerId"`
	TrafficDial          int                `json:"trafficDial"`
	VPCID                string             `json:"vpcId"`
	Type                 string             `json:"type"`
	Status               string             `json:"status"`
	Members              []PoolMemberDetail `json:"members"`
	CreatedAt            string             `json:"createdAt"`
	UpdatedAt            string             `json:"updatedAt"`
	DeletedAt            *string            `json:"deletedAt"`
}

type PoolMemberDetail struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	Description          string  `json:"description"`
	GlobalPoolMemberID   string  `json:"globalPoolMemberId"`
	GlobalLoadBalancerID string  `json:"globalLoadBalancerId"`
	SubnetID             string  `json:"subnetId"`
	Address              string  `json:"address"`
	Weight               int     `json:"weight"`
	Port                 int     `json:"port"`
	MonitorPort          int     `json:"monitorPort"`
	BackupRole           bool    `json:"backupRole"`
	Status               string  `json:"status"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
	DeletedAt            *string `json:"deletedAt"`
}

type Listener struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	Description          string  `json:"description"`
	Protocol             string  `json:"protocol"`
	Port                 int     `json:"port"`
	GlobalLoadBalancerID string  `json:"globalLoadBalancerId"`
	GlobalPoolID         string  `json:"globalPoolId"`
	TimeoutClient        int     `json:"timeoutClient"`
	TimeoutMember        int     `json:"timeoutMember"`
	TimeoutConnection    int     `json:"timeoutConnection"`
	AllowedCIDRs         string  `json:"allowedCidrs"`
	Headers              *string `json:"headers"`
	Status               string  `json:"status"`
	CreatedAt            string  `json:"createdAt"`
	UpdatedAt            string  `json:"updatedAt"`
	DeletedAt            *string `json:"deletedAt"`
}

type UsageHistory struct {
	Timestamp string  `json:"timestamp"`
	Value     float64 `json:"value"`
	Type      string  `json:"type"`
}
