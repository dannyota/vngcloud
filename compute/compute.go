// Package compute lists and reads vServer instances, SSH keys, server
// groups, and images.
package compute

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Client is the compute service client.
type Client struct {
	c *core.Client
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

type ListServersInput struct {
	Page int
	Size int
}

type ListServersOutput = core.PagedList[Server]

func (c *Client) ListServers(ctx context.Context, in *ListServersInput) (*ListServersOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	page, size := 0, 0
	if in != nil {
		page, size = in.Page, in.Size
	}
	var resp listServersResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "compute.ListServers",
		Method:    "GET",
		URL:       c.computeURL("v2", []string{projectID, "servers"}, core.PageQuery(page, size)),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

type GetServerInput struct {
	ServerID string `vngcloud:"required"`
}

type GetServerOutput struct {
	Server Server
}

func (c *Client) GetServer(ctx context.Context, in *GetServerInput) (*GetServerOutput, error) {
	if err := core.CheckRequired("compute.GetServer", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data Server `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "compute.GetServer",
		Method:    "GET",
		URL:       c.computeURL("v2", []string{projectID, "servers", in.ServerID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetServerOutput{Server: resp.Data}, nil
}

type ListSSHKeysInput struct {
	Name string
	Page int
	Size int
}

type ListSSHKeysOutput = core.PagedList[SSHKey]

func (c *Client) ListSSHKeys(ctx context.Context, in *ListSSHKeysInput) (*ListSSHKeysOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	name := ""
	page, size := core.DefaultPage, core.DefaultPageSize
	if in != nil {
		name = in.Name
		if in.Page > 0 {
			page = in.Page
		}
		if in.Size > 0 {
			size = in.Size
		}
	}
	q.Set("name", name)
	q.Set("page", strconv.Itoa(page))
	q.Set("size", strconv.Itoa(size))

	var resp listSSHKeysResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "compute.ListSSHKeys",
		Method:    "GET",
		URL:       c.computeURL("v2", []string{projectID, "sshKeys"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

type ListServerGroupsInput struct {
	Name string
	Page int
	Size int
}

type ListServerGroupsOutput = core.PagedList[ServerGroup]

func (c *Client) ListServerGroups(ctx context.Context, in *ListServerGroupsInput) (*ListServerGroupsOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("name", "")
	q.Set("offset", "0")
	q.Set("limit", strconv.Itoa(core.DefaultPageSize))
	if in != nil {
		q.Set("name", in.Name)
		if in.Page > 0 {
			q.Set("offset", strconv.Itoa(in.Page))
		}
		if in.Size > 0 {
			q.Set("limit", strconv.Itoa(in.Size))
		}
	}

	var resp listServerGroupsResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "compute.ListServerGroups",
		Method:    "GET",
		URL:       c.computeURL("v2", []string{projectID, "serverGroups"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

type ListServerSecurityGroupsInput struct{}

type ListServerSecurityGroupsOutput = core.List[ServerSecurityGroup]

func (c *Client) ListServerSecurityGroups(ctx context.Context, _ *ListServerSecurityGroupsInput) (*ListServerSecurityGroupsOutput, error) {
	servers, err := c.ListServers(ctx, nil)
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
	return &ListServerSecurityGroupsOutput{Items: items}, nil
}

type ListServerGroupMembersInput struct{}

type ListServerGroupMembersOutput = core.List[ServerGroupMembership]

func (c *Client) ListServerGroupMembers(ctx context.Context, _ *ListServerGroupMembersInput) (*ListServerGroupMembersOutput, error) {
	groups, err := c.ListServerGroups(ctx, nil)
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
	return &ListServerGroupMembersOutput{Items: items}, nil
}

type ListServerGroupPoliciesInput struct{}

type ListServerGroupPoliciesOutput = core.List[ServerGroupPolicy]

func (c *Client) ListServerGroupPolicies(ctx context.Context, _ *ListServerGroupPoliciesInput) (*ListServerGroupPoliciesOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []serverGroupPolicyResp `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "compute.ListServerGroupPolicies",
		Method:    "GET",
		URL:       c.computeURL("v2", []string{projectID, "serverGroups", "policies"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	policies := make([]ServerGroupPolicy, 0, len(resp.Data))
	for _, p := range resp.Data {
		policies = append(policies, p.toPolicy())
	}
	return &ListServerGroupPoliciesOutput{Items: policies}, nil
}

type ListOSImagesInput struct {
	ZoneID string
}

type ListOSImagesOutput = core.List[OSImage]

func (c *Client) ListOSImages(ctx context.Context, in *ListOSImagesInput) (*ListOSImagesOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if in != nil && in.ZoneID != "" {
		q.Set("zoneId", in.ZoneID)
	}
	var resp struct {
		Images []OSImage `json:"images"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "compute.ListOSImages",
		Method:    "GET",
		URL:       c.computeURL("v1", []string{projectID, "images", "os"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListOSImagesOutput{Items: resp.Images}, nil
}

type ListGPUImagesInput struct{}

type ListGPUImagesOutput = core.List[OSImage]

func (c *Client) ListGPUImages(ctx context.Context, _ *ListGPUImagesInput) (*ListGPUImagesOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Images []OSImage `json:"images"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "compute.ListGPUImages",
		Method:    "GET",
		URL:       c.computeURL("v1", []string{projectID, "images", "gpu"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListGPUImagesOutput{Items: resp.Images}, nil
}

type ListUserImagesInput struct {
	Page int
	Size int
}

type ListUserImagesOutput = core.PagedList[UserImage]

func (c *Client) ListUserImages(ctx context.Context, in *ListUserImagesInput) (*ListUserImagesOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	page, size := 0, 0
	if in != nil {
		page, size = in.Page, in.Size
	}
	var resp listUserImagesResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "compute.ListUserImages",
		Method:    "GET",
		URL:       c.computeURL("v2", []string{projectID, "user-images"}, core.PageQuery(page, size)),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (c *Client) computeURL(version string, parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{
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
