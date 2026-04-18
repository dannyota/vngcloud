package compute

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

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

type ListServersOptions = core.ListOptions
type ListSSHKeysOptions struct {
	Name string
	Page int
	Size int
}
type ListServerGroupsOptions struct {
	Name string
	Page int
	Size int
}
type ListOSImagesOptions struct {
	ZoneID string
}
type ListUserImagesOptions = core.ListOptions

type ListServersResult = core.ListResult[Server]
type ListSSHKeysResult = core.ListResult[SSHKey]
type ListServerGroupsResult = core.ListResult[ServerGroup]
type ListUserImagesResult = core.ListResult[UserImage]

func (s *Service) ListServers(ctx context.Context, opts *ListServersOptions) (*ListServersResult, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp listServersResponse
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Compute.ListServers",
		Method:    "GET",
		URL:       s.computeURL("v2", []string{projectID, "servers"}, core.ListQuery(opts)),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) GetServer(ctx context.Context, id string) (*Server, error) {
	if id == "" {
		return nil, errors.New("vngcloud: server id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data Server `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Compute.GetServer",
		Method:    "GET",
		URL:       s.computeURL("v2", []string{projectID, "servers", id}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (s *Service) ListSSHKeys(ctx context.Context, opts *ListSSHKeysOptions) (*ListSSHKeysResult, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	name := ""
	page, size := core.DefaultPage, core.DefaultPageSize
	if opts != nil {
		name = opts.Name
		if opts.Page > 0 {
			page = opts.Page
		}
		if opts.Size > 0 {
			size = opts.Size
		}
	}
	q.Set("name", name)
	q.Set("page", strconv.Itoa(page))
	q.Set("size", strconv.Itoa(size))

	var resp listSSHKeysResponse
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Compute.ListSSHKeys",
		Method:    "GET",
		URL:       s.computeURL("v2", []string{projectID, "sshKeys"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) ListServerGroups(ctx context.Context, opts *ListServerGroupsOptions) (*ListServerGroupsResult, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("name", "")
	q.Set("offset", "0")
	q.Set("limit", strconv.Itoa(core.DefaultPageSize))
	if opts != nil {
		q.Set("name", opts.Name)
		if opts.Page > 0 {
			q.Set("offset", strconv.Itoa(opts.Page))
		}
		if opts.Size > 0 {
			q.Set("limit", strconv.Itoa(opts.Size))
		}
	}

	var resp listServerGroupsResponse
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Compute.ListServerGroups",
		Method:    "GET",
		URL:       s.computeURL("v2", []string{projectID, "serverGroups"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) ListServerSecurityGroups(ctx context.Context) ([]ServerSecurityGroup, error) {
	servers, err := s.ListServers(ctx, nil)
	if err != nil {
		return nil, err
	}
	items := make([]ServerSecurityGroup, 0)
	for _, server := range servers.Items {
		for _, secgroup := range server.SecurityGroups {
			items = append(items, ServerSecurityGroup{
				ServerID: server.UUID,
				Name:     secgroup.Name,
				UUID:     secgroup.UUID,
			})
		}
	}
	return items, nil
}

func (s *Service) ListServerGroupMembers(ctx context.Context) ([]ServerGroupMembership, error) {
	groups, err := s.ListServerGroups(ctx, nil)
	if err != nil {
		return nil, err
	}
	items := make([]ServerGroupMembership, 0)
	for _, group := range groups.Items {
		for _, server := range group.Servers {
			items = append(items, ServerGroupMembership{
				ServerGroupID: group.UUID,
				Name:          server.Name,
				UUID:          server.UUID,
			})
		}
	}
	return items, nil
}

func (s *Service) ListServerGroupPolicies(ctx context.Context) ([]ServerGroupPolicy, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []serverGroupPolicyResp `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Compute.ListServerGroupPolicies",
		Method:    "GET",
		URL:       s.computeURL("v2", []string{projectID, "serverGroups", "policies"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	policies := make([]ServerGroupPolicy, 0, len(resp.Data))
	for _, p := range resp.Data {
		policies = append(policies, p.toPolicy())
	}
	return policies, nil
}

func (s *Service) ListOSImages(ctx context.Context, opts *ListOSImagesOptions) ([]OSImage, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if opts != nil && opts.ZoneID != "" {
		q.Set("zoneId", opts.ZoneID)
	}
	var resp struct {
		Images []OSImage `json:"images"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Compute.ListOSImages",
		Method:    "GET",
		URL:       s.computeURL("v1", []string{projectID, "images", "os"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Images, nil
}

func (s *Service) ListGPUImages(ctx context.Context) ([]OSImage, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Images []OSImage `json:"images"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Compute.ListGPUImages",
		Method:    "GET",
		URL:       s.computeURL("v1", []string{projectID, "images", "gpu"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Images, nil
}

func (s *Service) ListUserImages(ctx context.Context, opts *ListUserImagesOptions) (*ListUserImagesResult, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp listUserImagesResponse
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Compute.ListUserImages",
		Method:    "GET",
		URL:       s.computeURL("v2", []string{projectID, "user-images"}, core.ListQuery(opts)),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) computeURL(version string, parts []string, q url.Values) string {
	return s.client.RouteURL(routes.Route{
		Product: routes.ProductVServer,
		Version: version,
		Parts:   parts,
		Query:   q,
	})
}

type listServersResponse struct {
	ListData  []Server `json:"listData"`
	Page      int      `json:"page"`
	PageSize  int      `json:"pageSize"`
	TotalPage int      `json:"totalPage"`
	TotalItem int      `json:"totalItem"`
}

type listSSHKeysResponse struct {
	ListData  []SSHKey `json:"listData"`
	Page      int      `json:"page"`
	PageSize  int      `json:"pageSize"`
	TotalPage int      `json:"totalPage"`
	TotalItem int      `json:"totalItem"`
}

type listServerGroupsResponse struct {
	ListData  []ServerGroup `json:"listData"`
	Page      int           `json:"page"`
	PageSize  int           `json:"pageSize"`
	TotalPage int           `json:"totalPage"`
	TotalItem int           `json:"totalItem"`
}

type listUserImagesResponse struct {
	ListData  []UserImage `json:"listData"`
	Page      int         `json:"page"`
	PageSize  int         `json:"pageSize"`
	TotalPage int         `json:"totalPage"`
	TotalItem int         `json:"totalItem"`
}

type serverGroupPolicyResp struct {
	Name          string `json:"name"`
	UUID          string `json:"uuid"`
	Status        string `json:"status"`
	Description   string `json:"description"`
	DescriptionVI string `json:"descriptionVi"`
}

func (p serverGroupPolicyResp) toPolicy() ServerGroupPolicy {
	return ServerGroupPolicy{
		Name:   p.Name,
		UUID:   p.UUID,
		Status: p.Status,
		Descriptions: map[string]string{
			"en": p.Description,
			"vi": p.DescriptionVI,
		},
	}
}

type Server struct {
	BootVolumeID          string             `json:"bootVolumeId"`
	CreatedAt             string             `json:"createdAt"`
	Description           string             `json:"description"`
	EncryptionVolume      bool               `json:"encryptionVolume"`
	EnableLog             bool               `json:"enableLog"`
	EnableMetric          bool               `json:"enableMetric"`
	Licence               bool               `json:"licence"`
	LicenseKey            string             `json:"licenseKey"`
	Location              string             `json:"location"`
	Metadata              string             `json:"metadata"`
	MigrateState          string             `json:"migrateState"`
	MigrationStatus       string             `json:"migrationStatus"`
	Name                  string             `json:"name"`
	Product               string             `json:"product"`
	ServerGroupID         any                `json:"serverGroupId"`
	ServerGroupName       string             `json:"serverGroupName"`
	SSHKeyName            string             `json:"sshKeyName"`
	Status                string             `json:"status"`
	StopBeforeMigrate     bool               `json:"stopBeforeMigrate"`
	User                  string             `json:"user"`
	UUID                  string             `json:"uuid"`
	Image                 Image              `json:"image"`
	Flavor                Flavor             `json:"flavor"`
	SecurityGroups        []ServerSecgroup   `json:"secGroups"`
	ExternalInterfaces    []NetworkInterface `json:"externalInterfaces"`
	InternalInterfaces    []NetworkInterface `json:"internalInterfaces"`
	ZoneID                string             `json:"zoneId"`
	Zone                  core.NetworkZone   `json:"zone"`
	AppLicense            any                `json:"appLicense"`
	AppLicenseName        string             `json:"appLicenseName"`
	AppPackageVersionName string             `json:"appPackageVersionName"`
	DefaultTagIDs         []string           `json:"defaultTagIds"`
	FlavorZoneID          string             `json:"flavorZoneId"`
	FlavorZones           any                `json:"flavorZones"`
	GPUMemory             any                `json:"gpuMemory"`
	HostGroupID           string             `json:"hostGroupId"`
}

type NetworkInterface struct {
	CreatedAt     string `json:"createdAt"`
	FixedIP       string `json:"fixedIp"`
	FloatingIP    string `json:"floatingIp"`
	FloatingIPID  string `json:"floatingIpId"`
	InterfaceType string `json:"interfaceType"`
	MAC           string `json:"mac"`
	NetworkUUID   string `json:"networkUuid"`
	PortUUID      string `json:"portUuid"`
	Product       string `json:"product"`
	ServerUUID    string `json:"serverUuid"`
	Status        string `json:"status"`
	SubnetUUID    string `json:"subnetUuid"`
	Type          string `json:"type"`
	UpdatedAt     string `json:"updatedAt"`
	UUID          string `json:"uuid"`
}

type Flavor struct {
	Bandwidth              int64  `json:"bandwidth"`
	BandwidthUnit          string `json:"bandwidthUnit"`
	CPU                    int64  `json:"cpu"`
	CPUPlatformDescription string `json:"cpuPlatformDescription"`
	FlavorID               string `json:"flavorId"`
	GPU                    int64  `json:"gpu"`
	Group                  string `json:"group"`
	Memory                 int64  `json:"memory"`
	Metadata               string `json:"metaData"`
	Name                   string `json:"name"`
	RemainingVMs           int64  `json:"remainingVms"`
	ZoneID                 string `json:"zoneId"`
}

type Image struct {
	FlavorZoneIDs []string     `json:"flavorZoneIds"`
	ID            string       `json:"id"`
	ImageType     string       `json:"imageType"`
	ImageVersion  string       `json:"imageVersion"`
	Licence       bool         `json:"licence"`
	PackageLimit  PackageLimit `json:"packageLimit"`
}

type PackageLimit struct {
	CPU      int64 `json:"cpu"`
	DiskSize int64 `json:"diskSize"`
	Memory   int64 `json:"memory"`
}

type ServerSecgroup struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
}

type SSHKey struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  string `json:"createdAt"`
	PublicKey  string `json:"pubKey"`
	PrivateKey string `json:"privateKey"`
	Status     string `json:"status"`
}

type ServerGroup struct {
	UUID          string              `json:"uuid"`
	ServerGroupID any                 `json:"serverGroupId"`
	Name          string              `json:"name"`
	Description   string              `json:"description"`
	PolicyID      string              `json:"policyId"`
	PolicyName    string              `json:"policyName"`
	CreatedAt     string              `json:"createdAt"`
	Servers       []ServerGroupMember `json:"servers"`
}

type ServerGroupMember struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
}

type ServerSecurityGroup struct {
	ServerID string `json:"serverId"`
	Name     string `json:"name"`
	UUID     string `json:"uuid"`
}

type ServerGroupMembership struct {
	ServerGroupID string `json:"serverGroupId"`
	Name          string `json:"name"`
	UUID          string `json:"uuid"`
}

type ServerGroupPolicy struct {
	Name          string            `json:"name"`
	UUID          string            `json:"uuid"`
	Status        string            `json:"status"`
	Description   string            `json:"description"`
	DescriptionVI string            `json:"descriptionVi"`
	Descriptions  map[string]string `json:"descriptions"`
}

type OSImage struct {
	ID            string       `json:"id"`
	ImageType     string       `json:"imageType"`
	ImageVersion  string       `json:"imageVersion"`
	Licence       *bool        `json:"licence"`
	FlavorZoneIDs []string     `json:"flavorZoneIds"`
	PackageLimit  PackageLimit `json:"packageLimit"`
	LicenseKey    *string      `json:"licenseKey"`
	DefaultTagIDs []string     `json:"defaultTagIds"`
	ZoneID        string       `json:"zoneId"`
	Description   string       `json:"description"`
}

type UserImage struct {
	UUID      string  `json:"uuid"`
	ProjectID string  `json:"projectId"`
	Name      string  `json:"name"`
	MinDisk   int     `json:"minDisk"`
	ImageSize float64 `json:"imageSize"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"createdAt"`
	Metadata  string  `json:"metaData"`
}

func (sv Server) IsRunning() bool {
	return strings.EqualFold(sv.Status, "ACTIVE")
}

func (sv Server) CanDelete() bool {
	switch strings.ToUpper(sv.Status) {
	case "ACTIVE", "ERROR", "STOPPED":
		return true
	default:
		return false
	}
}
