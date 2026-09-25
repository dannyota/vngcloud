// Package loadbalancer lists and reads vLB load balancers, listeners,
// pools, policies, certificates, and packages.
package loadbalancer

import (
	"context"
	"net/url"
	"strconv"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Client is the load balancer service client.
type Client struct {
	c *core.Client
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

type ListLoadBalancersInput struct {
	Name string
	Page int
	Size int
}

type ListLoadBalancersOutput = core.PagedList[LoadBalancer]

func (c *Client) ListLoadBalancers(ctx context.Context, in *ListLoadBalancersInput) (*ListLoadBalancersOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	var resp listLoadBalancersResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "loadbalancer.ListLoadBalancers",
		Method:    "GET",
		URL:       c.lbURL([]string{projectID, "loadBalancers"}, lbListQuery(name, page, size)),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

type GetLoadBalancerInput struct {
	LoadBalancerID string `vngcloud:"required"`
}

type GetLoadBalancerOutput struct {
	LoadBalancer LoadBalancer
}

func (c *Client) GetLoadBalancer(ctx context.Context, in *GetLoadBalancerInput) (*GetLoadBalancerOutput, error) {
	if err := core.CheckRequired("loadbalancer.GetLoadBalancer", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data LoadBalancer `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "loadbalancer.GetLoadBalancer",
		Method:    "GET",
		URL:       c.lbURL([]string{projectID, "loadBalancers", in.LoadBalancerID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetLoadBalancerOutput{LoadBalancer: resp.Data}, nil
}

type ListPackagesInput struct {
	ZoneID string
}

type ListPackagesOutput = core.List[Package]

func (c *Client) ListPackages(ctx context.Context, in *ListPackagesInput) (*ListPackagesOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if in != nil && in.ZoneID != "" {
		q.Set("zoneId", in.ZoneID)
	}
	var resp struct {
		ListData []Package `json:"listData"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "loadbalancer.ListPackages",
		Method:    "GET",
		URL:       c.lbURL([]string{projectID, "loadBalancers", "packages"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListPackagesOutput{Items: resp.ListData}, nil
}

type ListCertificatesInput struct {
	Name string
	Page int
	Size int
}

type ListCertificatesOutput = core.PagedList[Certificate]

func (c *Client) ListCertificates(ctx context.Context, in *ListCertificatesInput) (*ListCertificatesOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	var resp listCertificatesResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "loadbalancer.ListCertificates",
		Method:    "GET",
		URL:       c.lbURL([]string{projectID, "cas"}, lbListQuery(name, page, size)),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

type ListListenersInput struct {
	LoadBalancerID string `vngcloud:"required"`
}

type ListListenersOutput = core.List[Listener]

func (c *Client) ListListeners(ctx context.Context, in *ListListenersInput) (*ListListenersOutput, error) {
	if err := core.CheckRequired("loadbalancer.ListListeners", in); err != nil {
		return nil, err
	}
	items, err := listLoadBalancerChild[Listener](c, ctx, "loadbalancer.ListListeners", in.LoadBalancerID, []string{"listeners"})
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
	if err := core.CheckRequired("loadbalancer.GetListener", in); err != nil {
		return nil, err
	}
	var resp struct {
		Data Listener `json:"data"`
	}
	if err := c.getLoadBalancerChild(ctx, "loadbalancer.GetListener", in.LoadBalancerID, []string{"listeners", in.ListenerID}, &resp); err != nil {
		return nil, err
	}
	return &GetListenerOutput{Listener: resp.Data}, nil
}

type ListPoolsInput struct {
	LoadBalancerID string `vngcloud:"required"`
}

type ListPoolsOutput = core.List[Pool]

func (c *Client) ListPools(ctx context.Context, in *ListPoolsInput) (*ListPoolsOutput, error) {
	if err := core.CheckRequired("loadbalancer.ListPools", in); err != nil {
		return nil, err
	}
	items, err := listLoadBalancerChild[Pool](c, ctx, "loadbalancer.ListPools", in.LoadBalancerID, []string{"pools"})
	if err != nil {
		return nil, err
	}
	return &ListPoolsOutput{Items: items}, nil
}

type GetPoolInput struct {
	LoadBalancerID string `vngcloud:"required"`
	PoolID         string `vngcloud:"required"`
}

type GetPoolOutput struct {
	Pool Pool
}

func (c *Client) GetPool(ctx context.Context, in *GetPoolInput) (*GetPoolOutput, error) {
	if err := core.CheckRequired("loadbalancer.GetPool", in); err != nil {
		return nil, err
	}
	var resp struct {
		Data Pool `json:"data"`
	}
	if err := c.getLoadBalancerChild(ctx, "loadbalancer.GetPool", in.LoadBalancerID, []string{"pools", in.PoolID}, &resp); err != nil {
		return nil, err
	}
	return &GetPoolOutput{Pool: resp.Data}, nil
}

type GetPoolHealthMonitorInput struct {
	LoadBalancerID string `vngcloud:"required"`
	PoolID         string `vngcloud:"required"`
}

type GetPoolHealthMonitorOutput struct {
	HealthMonitor HealthMonitor
}

func (c *Client) GetPoolHealthMonitor(ctx context.Context, in *GetPoolHealthMonitorInput) (*GetPoolHealthMonitorOutput, error) {
	if err := core.CheckRequired("loadbalancer.GetPoolHealthMonitor", in); err != nil {
		return nil, err
	}
	var resp struct {
		Data HealthMonitor `json:"data"`
	}
	if err := c.getLoadBalancerChild(ctx, "loadbalancer.GetPoolHealthMonitor", in.LoadBalancerID, []string{"pools", in.PoolID, "healthMonitor"}, &resp); err != nil {
		return nil, err
	}
	return &GetPoolHealthMonitorOutput{HealthMonitor: resp.Data}, nil
}

type ListPoolMembersInput struct {
	LoadBalancerID string `vngcloud:"required"`
	PoolID         string `vngcloud:"required"`
}

type ListPoolMembersOutput = core.List[PoolMember]

func (c *Client) ListPoolMembers(ctx context.Context, in *ListPoolMembersInput) (*ListPoolMembersOutput, error) {
	if err := core.CheckRequired("loadbalancer.ListPoolMembers", in); err != nil {
		return nil, err
	}
	items, err := listLoadBalancerChild[PoolMember](c, ctx, "loadbalancer.ListPoolMembers", in.LoadBalancerID, []string{"pools", in.PoolID, "members"})
	if err != nil {
		return nil, err
	}
	return &ListPoolMembersOutput{Items: items}, nil
}

type ListPoliciesInput struct {
	LoadBalancerID string `vngcloud:"required"`
	ListenerID     string `vngcloud:"required"`
}

type ListPoliciesOutput = core.List[Policy]

func (c *Client) ListPolicies(ctx context.Context, in *ListPoliciesInput) (*ListPoliciesOutput, error) {
	if err := core.CheckRequired("loadbalancer.ListPolicies", in); err != nil {
		return nil, err
	}
	items, err := listLoadBalancerChild[Policy](c, ctx, "loadbalancer.ListPolicies", in.LoadBalancerID, []string{"listeners", in.ListenerID, "l7policies"})
	if err != nil {
		return nil, err
	}
	return &ListPoliciesOutput{Items: items}, nil
}

type GetPolicyInput struct {
	LoadBalancerID string `vngcloud:"required"`
	ListenerID     string `vngcloud:"required"`
	PolicyID       string `vngcloud:"required"`
}

type GetPolicyOutput struct {
	Policy Policy
}

func (c *Client) GetPolicy(ctx context.Context, in *GetPolicyInput) (*GetPolicyOutput, error) {
	if err := core.CheckRequired("loadbalancer.GetPolicy", in); err != nil {
		return nil, err
	}
	var resp struct {
		Data Policy `json:"data"`
	}
	if err := c.getLoadBalancerChild(ctx, "loadbalancer.GetPolicy", in.LoadBalancerID, []string{"listeners", in.ListenerID, "l7policies", in.PolicyID}, &resp); err != nil {
		return nil, err
	}
	return &GetPolicyOutput{Policy: resp.Data}, nil
}

type ListTagsInput struct {
	LoadBalancerID string `vngcloud:"required"`
}

type ListTagsOutput = core.List[Tag]

func (c *Client) ListTags(ctx context.Context, in *ListTagsInput) (*ListTagsOutput, error) {
	if err := core.CheckRequired("loadbalancer.ListTags", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp []Tag
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "loadbalancer.ListTags",
		Method:    "GET",
		URL:       c.lbURL([]string{projectID, "tag", "resource", in.LoadBalancerID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListTagsOutput{Items: resp}, nil
}

type GetCertificateInput struct {
	CertificateID string `vngcloud:"required"`
}

type GetCertificateOutput struct {
	Certificate Certificate
}

func (c *Client) GetCertificate(ctx context.Context, in *GetCertificateInput) (*GetCertificateOutput, error) {
	if err := core.CheckRequired("loadbalancer.GetCertificate", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp Certificate
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "loadbalancer.GetCertificate",
		Method:    "GET",
		URL:       c.lbURL([]string{projectID, "cas", in.CertificateID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetCertificateOutput{Certificate: resp}, nil
}

func listLoadBalancerChild[T any](c *Client, ctx context.Context, operation, loadBalancerID string, childParts []string) ([]T, error) {
	var resp struct {
		Data []T `json:"data"`
	}
	if err := c.getLoadBalancerChild(ctx, operation, loadBalancerID, childParts, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *Client) getLoadBalancerChild(ctx context.Context, operation, loadBalancerID string, childParts []string, out any) error {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return err
	}
	parts := append([]string{projectID, "loadBalancers", loadBalancerID}, childParts...)
	return c.c.DoJSON(ctx, transport.Request{
		Operation: operation,
		Method:    "GET",
		URL:       c.lbURL(parts, nil),
		OK:        []int{200},
	}, out)
}

func (c *Client) lbURL(parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{
		Product: routes.ProductVLB,
		Version: "v2",
		Parts:   parts,
		Query:   q,
	})
}

func lbListQuery(name string, page, size int) url.Values {
	if page <= 0 {
		page = core.DefaultPage
	}
	if size <= 0 {
		size = core.DefaultPageSize
	}
	q := url.Values{}
	q.Set("name", name)
	q.Set("page", strconv.Itoa(page))
	q.Set("size", strconv.Itoa(size))
	return q
}

type listLoadBalancersResponse struct {
	ListData  []LoadBalancer `json:"listData"`
	Page      int            `json:"page"`
	PageSize  int            `json:"pageSize"`
	TotalPage int            `json:"totalPage"`
	TotalItem int            `json:"totalItem"`
}

type listCertificatesResponse struct {
	ListData  []Certificate `json:"listData"`
	Page      int           `json:"page"`
	PageSize  int           `json:"pageSize"`
	TotalPage int           `json:"totalPage"`
	TotalItem int           `json:"totalItem"`
}

type LoadBalancer struct {
	UUID               string `json:"uuid"`
	Name               string `json:"name"`
	DisplayStatus      string `json:"displayStatus"`
	Address            string `json:"address"`
	PrivateSubnetID    string `json:"privateSubnetId"`
	PrivateSubnetCIDR  string `json:"privateSubnetCidr"`
	Type               string `json:"type"`
	DisplayType        string `json:"displayType"`
	LoadBalancerSchema string `json:"loadBalancerSchema"`
	PackageID          string `json:"packageId"`
	Description        string `json:"description"`
	Location           string `json:"location"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
	ProgressStatus     string `json:"progressStatus"`
	Status             string `json:"status"`
	BackendSubnetID    string `json:"backendSubnetId"`
	Internal           bool   `json:"internal"`
	AutoScalable       bool   `json:"autoScalable"`
	ZoneID             string `json:"zoneId"`
	MinSize            int    `json:"minSize"`
	MaxSize            int    `json:"maxSize"`
	TotalNodes         int    `json:"totalNodes"`
	Nodes              []Node `json:"nodes"`
}

type Node struct {
	Status   string `json:"status"`
	ZoneID   string `json:"zoneId"`
	ZoneName string `json:"zoneName"`
	SubnetID string `json:"subnetId"`
}

type Package struct {
	UUID             string `json:"uuid"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	ConnectionNumber int    `json:"connectionNumber"`
	DataTransfer     int    `json:"dataTransfer"`
	Mode             string `json:"mode"`
	LBType           string `json:"lbType"`
	DisplayLBType    string `json:"displayLbType"`
}

type Certificate struct {
	UUID               string `json:"uuid"`
	Name               string `json:"name"`
	CertificateType    string `json:"certificateType"`
	ExpiredAt          string `json:"expiredAt"`
	ImportedAt         string `json:"importedAt"`
	NotAfter           int64  `json:"notAfter"`
	KeyAlgorithm       string `json:"keyAlgorithm"`
	Serial             string `json:"serial"`
	Subject            string `json:"subject"`
	DomainName         string `json:"domainName"`
	InUse              bool   `json:"inUse"`
	Issuer             string `json:"issuer"`
	SignatureAlgorithm string `json:"signatureAlgorithm"`
	NotBefore          int64  `json:"notBefore"`
}

type Tag struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	CreatedAt string `json:"createdAt"`
	SystemTag bool   `json:"systemTag,omitempty"`
}

type ListenerInsertHeader struct {
	HeaderName  string `json:"headerName"`
	HeaderValue string `json:"headerValue"`
}

type Listener struct {
	UUID                            string                 `json:"uuid"`
	Name                            string                 `json:"name"`
	Description                     string                 `json:"description"`
	Protocol                        string                 `json:"protocol"`
	ProtocolPort                    int                    `json:"protocolPort"`
	ConnectionLimit                 int                    `json:"connectionLimit"`
	DefaultPoolID                   string                 `json:"defaultPoolId"`
	DefaultPoolName                 string                 `json:"defaultPoolName"`
	TimeoutClient                   int                    `json:"timeoutClient"`
	TimeoutMember                   int                    `json:"timeoutMember"`
	TimeoutConnection               int                    `json:"timeoutConnection"`
	AllowedCIDRs                    string                 `json:"allowedCidrs"`
	CertificateAuthorities          []string               `json:"certificateAuthorities"`
	DisplayStatus                   string                 `json:"displayStatus"`
	CreatedAt                       string                 `json:"createdAt"`
	UpdatedAt                       string                 `json:"updatedAt"`
	DefaultCertificateAuthority     *string                `json:"defaultCertificateAuthority"`
	ClientCertificateAuthentication *string                `json:"clientCertificateAuthentication"`
	ProgressStatus                  string                 `json:"progressStatus"`
	InsertHeaders                   []ListenerInsertHeader `json:"insertHeaders"`
}

type Pool struct {
	UUID              string         `json:"uuid"`
	Name              string         `json:"name"`
	Protocol          string         `json:"protocol"`
	Description       string         `json:"description"`
	LoadBalanceMethod string         `json:"loadBalanceMethod"`
	DisplayStatus     string         `json:"displayStatus"`
	Stickiness        bool           `json:"stickiness"`
	TLSEncryption     bool           `json:"tlsEncryption"`
	Members           []PoolMember   `json:"members"`
	HealthMonitor     *HealthMonitor `json:"healthMonitor"`
}

type PoolMember struct {
	UUID           string `json:"uuid"`
	Address        string `json:"address"`
	ProtocolPort   int    `json:"protocolPort"`
	Weight         int    `json:"weight"`
	MonitorPort    int    `json:"monitorPort"`
	SubnetID       string `json:"subnetId"`
	Name           string `json:"name"`
	PoolID         string `json:"poolId"`
	TypeCreate     string `json:"typeCreate"`
	Backup         bool   `json:"backup"`
	DisplayStatus  string `json:"displayStatus"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updateAt"`
	CreatedBy      string `json:"createdBy"`
	ProgressStatus string `json:"progressStatus"`
}

type HealthMonitor struct {
	Timeout             int     `json:"timeout"`
	CreatedAt           string  `json:"createdAt"`
	UpdatedAt           string  `json:"updatedAt"`
	DomainName          *string `json:"domainName"`
	HTTPVersion         *string `json:"httpVersion"`
	HealthCheckProtocol string  `json:"healthCheckProtocol"`
	Interval            int     `json:"interval"`
	HealthyThreshold    int     `json:"healthyThreshold"`
	UnhealthyThreshold  int     `json:"unhealthyThreshold"`
	HealthCheckMethod   *string `json:"healthCheckMethod"`
	HealthCheckPath     *string `json:"healthCheckPath"`
	SuccessCode         *string `json:"successCode"`
	ProgressStatus      string  `json:"progressStatus"`
	DisplayStatus       string  `json:"displayStatus"`
}

type Policy struct {
	UUID             string   `json:"uuid"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	RedirectPoolID   string   `json:"redirectPoolId"`
	RedirectPoolName string   `json:"redirectPoolName"`
	Action           string   `json:"action"`
	RedirectURL      string   `json:"redirectUrl"`
	RedirectHTTPCode int      `json:"redirectHttpCode"`
	KeepQueryString  bool     `json:"keepQueryString"`
	Position         int      `json:"position"`
	L7Rules          []L7Rule `json:"l7Rules"`
	DisplayStatus    string   `json:"displayStatus"`
	CreatedAt        string   `json:"createdAt"`
	UpdatedAt        string   `json:"updatedAt"`
	ProgressStatus   string   `json:"progressStatus"`
}

type L7Rule struct {
	UUID               string `json:"uuid"`
	CompareType        string `json:"compareType"`
	RuleValue          string `json:"ruleValue"`
	RuleType           string `json:"ruleType"`
	ProvisioningStatus string `json:"provisioningStatus"`
	OperatingStatus    string `json:"operatingStatus"`
}
