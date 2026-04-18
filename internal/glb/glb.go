package glb

import (
	"context"
	"errors"
	"net/url"
	"strconv"

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

type ListGlobalLoadBalancersOptions struct {
	Name   string
	Offset int
	Limit  int
}

type ListGlobalLoadBalancersResult struct {
	Items  []GlobalLoadBalancer `json:"items"`
	Limit  int                  `json:"limit"`
	Total  int                  `json:"total"`
	Offset int                  `json:"offset"`
}

type ListGlobalLoadBalancerUsageHistoriesOptions struct {
	From string
	To   string
	Type string
}

type ListGlobalLoadBalancerUsageHistoriesResult struct {
	Type  string                           `json:"type"`
	Items []GlobalLoadBalancerUsageHistory `json:"items"`
	From  string                           `json:"from"`
	To    string                           `json:"to"`
}

func (s *Service) ListLoadBalancers(ctx context.Context, opts *ListGlobalLoadBalancersOptions) (*ListGlobalLoadBalancersResult, error) {
	offset, limit := 0, core.DefaultPageSize
	name := ""
	if opts != nil {
		name = opts.Name
		if opts.Offset >= 0 {
			offset = opts.Offset
		}
		if opts.Limit > 0 {
			limit = opts.Limit
		}
	}
	q := url.Values{}
	q.Set("name", name)
	q.Set("offset", strconv.Itoa(offset))
	q.Set("limit", strconv.Itoa(limit))
	var resp ListGlobalLoadBalancersResult
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "GLB.ListLoadBalancers",
		Method:    "GET",
		URL:       s.glbURL([]string{"global-load-balancers"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *Service) GetLoadBalancer(ctx context.Context, id string) (*GlobalLoadBalancer, error) {
	if id == "" {
		return nil, errors.New("vngcloud: global load balancer id is required")
	}
	var resp GlobalLoadBalancer
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "GLB.GetLoadBalancer",
		Method:    "GET",
		URL:       s.glbURL([]string{"global-load-balancers", id}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *Service) ListPackages(ctx context.Context) ([]GLBPackage, error) {
	var resp []GLBPackage
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "GLB.ListPackages",
		Method:    "GET",
		URL:       s.glbURL([]string{"packages"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Service) ListPools(ctx context.Context, loadBalancerID string) ([]GlobalPool, error) {
	return listGLBChild[GlobalPool](s, ctx, "GLB.ListPools", loadBalancerID, []string{"global-pools"})
}

func (s *Service) ListListeners(ctx context.Context, loadBalancerID string) ([]GlobalListener, error) {
	return listGLBChild[GlobalListener](s, ctx, "GLB.ListListeners", loadBalancerID, []string{"global-listeners"})
}

func (s *Service) GetListener(ctx context.Context, loadBalancerID, listenerID string) (*GlobalListener, error) {
	if listenerID == "" {
		return nil, errors.New("vngcloud: global listener id is required")
	}
	return getGLBChild[GlobalListener](s, ctx, "GLB.GetListener", loadBalancerID, []string{"global-listeners", listenerID})
}

func (s *Service) ListPoolMembers(ctx context.Context, loadBalancerID, poolID string) ([]GlobalPoolMember, error) {
	if poolID == "" {
		return nil, errors.New("vngcloud: global pool id is required")
	}
	return listGLBChild[GlobalPoolMember](s, ctx, "GLB.ListPoolMembers", loadBalancerID, []string{"global-pools", poolID, "pool-members"})
}

func (s *Service) GetPoolMember(ctx context.Context, loadBalancerID, poolID, poolMemberID string) (*GlobalPoolMember, error) {
	if poolID == "" {
		return nil, errors.New("vngcloud: global pool id is required")
	}
	if poolMemberID == "" {
		return nil, errors.New("vngcloud: global pool member id is required")
	}
	return getGLBChild[GlobalPoolMember](s, ctx, "GLB.GetPoolMember", loadBalancerID, []string{"global-pools", poolID, "pool-members", poolMemberID})
}

func (s *Service) ListUsageHistories(ctx context.Context, loadBalancerID string, opts *ListGlobalLoadBalancerUsageHistoriesOptions) (*ListGlobalLoadBalancerUsageHistoriesResult, error) {
	if loadBalancerID == "" {
		return nil, errors.New("vngcloud: global load balancer id is required")
	}
	q := url.Values{}
	if opts != nil {
		if opts.From != "" {
			q.Set("from", opts.From)
		}
		if opts.To != "" {
			q.Set("to", opts.To)
		}
		if opts.Type != "" {
			q.Set("type", opts.Type)
		}
	}
	var resp ListGlobalLoadBalancerUsageHistoriesResult
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "GLB.ListUsageHistories",
		Method:    "GET",
		URL:       s.glbURL([]string{"global-load-balancers", loadBalancerID, "usage-histories"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func listGLBChild[T any](s *Service, ctx context.Context, operation, loadBalancerID string, childParts []string) ([]T, error) {
	if loadBalancerID == "" {
		return nil, errors.New("vngcloud: global load balancer id is required")
	}
	parts := append([]string{"global-load-balancers", loadBalancerID}, childParts...)
	var resp []T
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: operation,
		Method:    "GET",
		URL:       s.glbURL(parts, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func getGLBChild[T any](s *Service, ctx context.Context, operation, loadBalancerID string, childParts []string) (*T, error) {
	if loadBalancerID == "" {
		return nil, errors.New("vngcloud: global load balancer id is required")
	}
	parts := append([]string{"global-load-balancers", loadBalancerID}, childParts...)
	var resp T
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: operation,
		Method:    "GET",
		URL:       s.glbURL(parts, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *Service) ListRegions(ctx context.Context) ([]GLBRegion, error) {
	var resp []GLBRegion
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "GLB.ListRegions",
		Method:    "GET",
		URL:       s.glbURL([]string{"regions"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Service) glbURL(parts []string, q url.Values) string {
	return s.client.RouteURL(routes.Route{
		Product: routes.ProductGLB,
		Version: "v1",
		Parts:   parts,
		Query:   q,
	})
}

type GLBPackage struct {
	ID                          string          `json:"id"`
	Name                        string          `json:"name"`
	Description                 string          `json:"description"`
	DescriptionEn               string          `json:"descriptionEn"`
	Enabled                     bool            `json:"enabled"`
	BaseSKU                     string          `json:"baseSku"`
	ConnectionSKU               string          `json:"connectionSku"`
	BaseConnectionRate          int             `json:"baseConnectionRate"`
	BaseDomesticTrafficTotal    int             `json:"baseDomesticTrafficTotal"`
	BaseNonDomesticTrafficTotal int             `json:"baseNonDomesticTrafficTotal"`
	DomesticTrafficSKU          string          `json:"domesticTrafficSku"`
	NonDomesticTrafficSKU       string          `json:"nonDomesticTrafficSku"`
	Detail                      any             `json:"detail"`
	CreatedAt                   string          `json:"createdAt"`
	UpdatedAt                   string          `json:"updatedAt"`
	VLBPackages                 []GLBVLBPackage `json:"vlbPackages"`
}

type GLBVLBPackage struct {
	ID           int    `json:"id"`
	GLBPackageID string `json:"glb_package_id"`
	Region       string `json:"region"`
	VLBPackageID string `json:"vlb_package_id"`
	CreatedAt    string `json:"created_at"`
}

type GLBRegion struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Status           string `json:"status"`
	VServerEndpoint  string `json:"vserverEndpoint"`
	VLBEndpoint      string `json:"vlbEndpoint"`
	UIServerEndpoint string `json:"uiServerEndpoint"`
}

type GlobalLoadBalancer struct {
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Status      string                     `json:"status"`
	Package     string                     `json:"package"`
	Type        string                     `json:"type"`
	UserID      int                        `json:"userId"`
	CreatedAt   string                     `json:"createdAt"`
	UpdatedAt   string                     `json:"updatedAt"`
	DeletedAt   string                     `json:"deletedAt"`
	VIPs        []GlobalLoadBalancerVIP    `json:"vips"`
	Domains     []GlobalLoadBalancerDomain `json:"domains"`
}

type GlobalLoadBalancerVIP struct {
	ID                   int    `json:"id"`
	Address              string `json:"address"`
	Status               string `json:"status"`
	Region               string `json:"region"`
	GlobalLoadBalancerID string `json:"globalLoadBalancerId"`
	CreatedAt            string `json:"createdAt"`
	UpdatedAt            string `json:"updatedAt"`
	DeletedAt            string `json:"deletedAt"`
}

type GlobalLoadBalancerDomain struct {
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

type GlobalPool struct {
	ID                   string                   `json:"id"`
	Name                 string                   `json:"name"`
	Description          string                   `json:"description"`
	GlobalLoadBalancerID string                   `json:"globalLoadBalancerId"`
	Algorithm            string                   `json:"algorithm"`
	StickySession        *string                  `json:"stickySession"`
	TLSEnabled           *string                  `json:"tlsEnabled"`
	Protocol             string                   `json:"protocol"`
	Status               string                   `json:"status"`
	Health               *GlobalPoolHealthMonitor `json:"health"`
	CreatedAt            string                   `json:"createdAt"`
	UpdatedAt            string                   `json:"updatedAt"`
	DeletedAt            *string                  `json:"deletedAt"`
}

type GlobalPoolHealthMonitor struct {
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

type GlobalPoolMember struct {
	ID                   string                   `json:"id"`
	Name                 string                   `json:"name"`
	Description          string                   `json:"description"`
	Region               string                   `json:"region"`
	GlobalPoolID         string                   `json:"globalPoolId"`
	GlobalLoadBalancerID string                   `json:"globalLoadBalancerId"`
	TrafficDial          int                      `json:"trafficDial"`
	VPCID                string                   `json:"vpcId"`
	Type                 string                   `json:"type"`
	Status               string                   `json:"status"`
	Members              []GlobalPoolMemberDetail `json:"members"`
	CreatedAt            string                   `json:"createdAt"`
	UpdatedAt            string                   `json:"updatedAt"`
	DeletedAt            *string                  `json:"deletedAt"`
}

type GlobalPoolMemberDetail struct {
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

type GlobalListener struct {
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

type GlobalLoadBalancerUsageHistory struct {
	Timestamp string  `json:"timestamp"`
	Value     float64 `json:"value"`
	Type      string  `json:"type"`
}
