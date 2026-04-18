package loadbalancer

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

type ListLoadBalancersOptions struct {
	Name string
	Page int
	Size int
}

type ListLoadBalancerPackagesOptions struct {
	ZoneID string
}

type ListCertificatesOptions struct {
	Name string
	Page int
	Size int
}

type ListLoadBalancersResult = core.ListResult[LoadBalancer]
type ListCertificatesResult = core.ListResult[Certificate]

func (s *Service) ListLoadBalancers(ctx context.Context, opts *ListLoadBalancersOptions) (*ListLoadBalancersResult, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp listLoadBalancersResponse
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "LoadBalancer.ListLoadBalancers",
		Method:    "GET",
		URL:       s.lbURL([]string{projectID, "loadBalancers"}, lbListQuery(optsNamePageSize(opts))),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) GetLoadBalancer(ctx context.Context, id string) (*LoadBalancer, error) {
	if id == "" {
		return nil, errors.New("vngcloud: load balancer id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data LoadBalancer `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "LoadBalancer.GetLoadBalancer",
		Method:    "GET",
		URL:       s.lbURL([]string{projectID, "loadBalancers", id}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (s *Service) ListLoadBalancerPackages(ctx context.Context, opts *ListLoadBalancerPackagesOptions) ([]LoadBalancerPackage, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if opts != nil && opts.ZoneID != "" {
		q.Set("zoneId", opts.ZoneID)
	}
	var resp struct {
		ListData []LoadBalancerPackage `json:"listData"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "LoadBalancer.ListLoadBalancerPackages",
		Method:    "GET",
		URL:       s.lbURL([]string{projectID, "loadBalancers", "packages"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.ListData, nil
}

func (s *Service) ListCertificates(ctx context.Context, opts *ListCertificatesOptions) (*ListCertificatesResult, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp listCertificatesResponse
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "LoadBalancer.ListCertificates",
		Method:    "GET",
		URL:       s.lbURL([]string{projectID, "cas"}, lbListQuery(optsNamePageSize(opts))),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) ListListeners(ctx context.Context, loadBalancerID string) ([]Listener, error) {
	return listLoadBalancerChild[Listener](s, ctx, "LoadBalancer.ListListeners", loadBalancerID, []string{"listeners"})
}

func (s *Service) GetListener(ctx context.Context, loadBalancerID, listenerID string) (*Listener, error) {
	if listenerID == "" {
		return nil, errors.New("vngcloud: listener id is required")
	}
	var resp struct {
		Data Listener `json:"data"`
	}
	if err := s.getLoadBalancerChild(ctx, "LoadBalancer.GetListener", loadBalancerID, []string{"listeners", listenerID}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (s *Service) ListPools(ctx context.Context, loadBalancerID string) ([]Pool, error) {
	return listLoadBalancerChild[Pool](s, ctx, "LoadBalancer.ListPools", loadBalancerID, []string{"pools"})
}

func (s *Service) GetPool(ctx context.Context, loadBalancerID, poolID string) (*Pool, error) {
	if poolID == "" {
		return nil, errors.New("vngcloud: pool id is required")
	}
	var resp struct {
		Data Pool `json:"data"`
	}
	if err := s.getLoadBalancerChild(ctx, "LoadBalancer.GetPool", loadBalancerID, []string{"pools", poolID}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (s *Service) GetPoolHealthMonitor(ctx context.Context, loadBalancerID, poolID string) (*HealthMonitor, error) {
	if poolID == "" {
		return nil, errors.New("vngcloud: pool id is required")
	}
	var resp struct {
		Data HealthMonitor `json:"data"`
	}
	if err := s.getLoadBalancerChild(ctx, "LoadBalancer.GetPoolHealthMonitor", loadBalancerID, []string{"pools", poolID, "healthMonitor"}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (s *Service) ListPoolMembers(ctx context.Context, loadBalancerID, poolID string) ([]PoolMember, error) {
	if poolID == "" {
		return nil, errors.New("vngcloud: pool id is required")
	}
	return listLoadBalancerChild[PoolMember](s, ctx, "LoadBalancer.ListPoolMembers", loadBalancerID, []string{"pools", poolID, "members"})
}

func (s *Service) ListPolicies(ctx context.Context, loadBalancerID, listenerID string) ([]Policy, error) {
	if listenerID == "" {
		return nil, errors.New("vngcloud: listener id is required")
	}
	return listLoadBalancerChild[Policy](s, ctx, "LoadBalancer.ListPolicies", loadBalancerID, []string{"listeners", listenerID, "l7policies"})
}

func (s *Service) GetPolicy(ctx context.Context, loadBalancerID, listenerID, policyID string) (*Policy, error) {
	if listenerID == "" {
		return nil, errors.New("vngcloud: listener id is required")
	}
	if policyID == "" {
		return nil, errors.New("vngcloud: policy id is required")
	}
	var resp struct {
		Data Policy `json:"data"`
	}
	if err := s.getLoadBalancerChild(ctx, "LoadBalancer.GetPolicy", loadBalancerID, []string{"listeners", listenerID, "l7policies", policyID}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (s *Service) ListTags(ctx context.Context, loadBalancerID string) ([]LoadBalancerTag, error) {
	if loadBalancerID == "" {
		return nil, errors.New("vngcloud: load balancer id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp []LoadBalancerTag
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "LoadBalancer.ListTags",
		Method:    "GET",
		URL:       s.lbURL([]string{projectID, "tag", "resource", loadBalancerID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Service) GetCertificate(ctx context.Context, id string) (*Certificate, error) {
	if id == "" {
		return nil, errors.New("vngcloud: certificate id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp Certificate
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "LoadBalancer.GetCertificate",
		Method:    "GET",
		URL:       s.lbURL([]string{projectID, "cas", id}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func listLoadBalancerChild[T any](s *Service, ctx context.Context, operation, loadBalancerID string, childParts []string) ([]T, error) {
	var resp struct {
		Data []T `json:"data"`
	}
	if err := s.getLoadBalancerChild(ctx, operation, loadBalancerID, childParts, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (s *Service) getLoadBalancerChild(ctx context.Context, operation, loadBalancerID string, childParts []string, out any) error {
	if loadBalancerID == "" {
		return errors.New("vngcloud: load balancer id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return err
	}
	parts := append([]string{projectID, "loadBalancers", loadBalancerID}, childParts...)
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: operation,
		Method:    "GET",
		URL:       s.lbURL(parts, nil),
		OK:        []int{200},
	}, out); err != nil {
		return err
	}
	return nil
}

func (s *Service) lbURL(parts []string, q url.Values) string {
	return s.client.RouteURL(routes.Route{
		Product: routes.ProductVLB,
		Version: "v2",
		Parts:   parts,
		Query:   q,
	})
}

type namePageSizer interface {
	namePageSize() (string, int, int)
}

type namePageSize struct {
	name string
	page int
	size int
}

func (n namePageSize) namePageSize() (string, int, int) {
	return n.name, n.page, n.size
}

func optsNamePageSize(v any) namePageSizer {
	switch opts := v.(type) {
	case *ListLoadBalancersOptions:
		if opts == nil {
			return namePageSize{}
		}
		return namePageSize{name: opts.Name, page: opts.Page, size: opts.Size}
	case *ListCertificatesOptions:
		if opts == nil {
			return namePageSize{}
		}
		return namePageSize{name: opts.Name, page: opts.Page, size: opts.Size}
	default:
		return namePageSize{}
	}
}

func lbListQuery(opts namePageSizer) url.Values {
	name, page, size := opts.namePageSize()
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
	UUID               string             `json:"uuid"`
	Name               string             `json:"name"`
	DisplayStatus      string             `json:"displayStatus"`
	Address            string             `json:"address"`
	PrivateSubnetID    string             `json:"privateSubnetId"`
	PrivateSubnetCIDR  string             `json:"privateSubnetCidr"`
	Type               string             `json:"type"`
	DisplayType        string             `json:"displayType"`
	LoadBalancerSchema string             `json:"loadBalancerSchema"`
	PackageID          string             `json:"packageId"`
	Description        string             `json:"description"`
	Location           string             `json:"location"`
	CreatedAt          string             `json:"createdAt"`
	UpdatedAt          string             `json:"updatedAt"`
	ProgressStatus     string             `json:"progressStatus"`
	Status             string             `json:"status"`
	BackendSubnetID    string             `json:"backendSubnetId"`
	Internal           bool               `json:"internal"`
	AutoScalable       bool               `json:"autoScalable"`
	ZoneID             string             `json:"zoneId"`
	MinSize            int                `json:"minSize"`
	MaxSize            int                `json:"maxSize"`
	TotalNodes         int                `json:"totalNodes"`
	Nodes              []LoadBalancerNode `json:"nodes"`
}

type LoadBalancerNode struct {
	Status   string `json:"status"`
	ZoneID   string `json:"zoneId"`
	ZoneName string `json:"zoneName"`
	SubnetID string `json:"subnetId"`
}

type LoadBalancerPackage struct {
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

type LoadBalancerTag struct {
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
