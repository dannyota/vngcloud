package sdk

import (
	"context"

	"danny.vn/vngcloud/internal/compute"
	"danny.vn/vngcloud/internal/containerregistry"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/dns"
	"danny.vn/vngcloud/internal/glb"
	"danny.vn/vngcloud/internal/loadbalancer"
	"danny.vn/vngcloud/internal/network"
	"danny.vn/vngcloud/internal/portal"
	"danny.vn/vngcloud/internal/volume"
)

type Config = core.Config
type EndpointOverrides = core.EndpointOverrides

type IAMUserAuth = core.IAMUserAuth
type TOTPProvider = core.TOTPProvider
type TOTPFunc = core.TOTPFunc
type SecretTOTP = core.SecretTOTP

type ClientOption = core.ClientOption
type ResponseCapture = core.ResponseCapture
type ResponseCaptureFunc = core.ResponseCaptureFunc

type APIError = core.APIError
type ListOptions = core.ListOptions
type Page = core.Page
type ListResult[T any] = core.ListResult[T]

type Project = core.Project
type ListProjectsOptions = core.ListProjectsOptions

type ComputeService = compute.Service
type ListServersOptions = compute.ListServersOptions
type ListSSHKeysOptions = compute.ListSSHKeysOptions
type ListServerGroupsOptions = compute.ListServerGroupsOptions
type ListOSImagesOptions = compute.ListOSImagesOptions
type ListUserImagesOptions = compute.ListUserImagesOptions
type ListServersResult = compute.ListServersResult
type ListSSHKeysResult = compute.ListSSHKeysResult
type ListServerGroupsResult = compute.ListServerGroupsResult
type ListUserImagesResult = compute.ListUserImagesResult
type Server = compute.Server
type NetworkInterface = compute.NetworkInterface
type Flavor = compute.Flavor
type Image = compute.Image
type PackageLimit = compute.PackageLimit
type ServerSecgroup = compute.ServerSecgroup
type SSHKey = compute.SSHKey
type ServerGroup = compute.ServerGroup
type ServerGroupMember = compute.ServerGroupMember
type ServerSecurityGroup = compute.ServerSecurityGroup
type ServerGroupMembership = compute.ServerGroupMembership
type ServerGroupPolicy = compute.ServerGroupPolicy
type OSImage = compute.OSImage
type UserImage = compute.UserImage

type VolumeService = volume.Service
type ListVolumesOptions = volume.ListVolumesOptions
type ListVolumeTypeZonesOptions = volume.ListVolumeTypeZonesOptions
type ListVolumeTypesOptions = volume.ListVolumeTypesOptions
type ListSnapshotsOptions = volume.ListSnapshotsOptions
type ListVolumesResult = volume.ListVolumesResult
type ListSnapshotsResult = volume.ListSnapshotsResult
type Volume = volume.Volume
type Zone = volume.Zone
type VolumeType = volume.VolumeType
type VolumeTypeZone = volume.VolumeTypeZone
type EncryptionType = volume.EncryptionType
type Snapshot = volume.Snapshot

type ContainerRegistryService = containerregistry.Service
type ListContainerRepositoriesOptions = containerregistry.ListContainerRepositoriesOptions
type ListContainerRegistryUsersOptions = containerregistry.ListContainerRegistryUsersOptions
type ListContainerRepositoriesResult = containerregistry.ListContainerRepositoriesResult
type ListContainerRegistryUsersResult = containerregistry.ListContainerRegistryUsersResult
type ContainerRepository = containerregistry.ContainerRepository
type ContainerRegistryUser = containerregistry.ContainerRegistryUser

type PortalService = portal.Service
type PortalUserInfo = portal.PortalUserInfo
type PortalZone = portal.PortalZone
type PortalQuota = portal.PortalQuota
type PortalTagQuota = portal.PortalTagQuota

type DNSService = dns.Service
type ListHostedZonesOptions = dns.ListHostedZonesOptions
type ListRecordsOptions = dns.ListRecordsOptions
type ListHostedZonesResult = dns.ListHostedZonesResult
type ListDNSRecordsResult = dns.ListDNSRecordsResult
type VpcMapRegion = dns.VpcMapRegion
type HostedZone = dns.HostedZone
type RecordValue = dns.RecordValue
type DNSRecord = dns.DNSRecord

type GlobalLoadBalancerService = glb.Service
type ListGlobalLoadBalancersOptions = glb.ListGlobalLoadBalancersOptions
type ListGlobalLoadBalancersResult = glb.ListGlobalLoadBalancersResult
type ListGlobalLoadBalancerUsageHistoriesOptions = glb.ListGlobalLoadBalancerUsageHistoriesOptions
type ListGlobalLoadBalancerUsageHistoriesResult = glb.ListGlobalLoadBalancerUsageHistoriesResult
type GlobalLoadBalancerPackage = glb.GLBPackage
type GlobalLoadBalancerRegionalPackage = glb.GLBVLBPackage
type GlobalLoadBalancerRegion = glb.GLBRegion
type GlobalLoadBalancer = glb.GlobalLoadBalancer
type GlobalLoadBalancerVIP = glb.GlobalLoadBalancerVIP
type GlobalLoadBalancerDomain = glb.GlobalLoadBalancerDomain
type GlobalPool = glb.GlobalPool
type GlobalPoolHealthMonitor = glb.GlobalPoolHealthMonitor
type GlobalPoolMember = glb.GlobalPoolMember
type GlobalPoolMemberDetail = glb.GlobalPoolMemberDetail
type GlobalListener = glb.GlobalListener
type GlobalLoadBalancerUsageHistory = glb.GlobalLoadBalancerUsageHistory

type LoadBalancerService = loadbalancer.Service
type ListLoadBalancersOptions = loadbalancer.ListLoadBalancersOptions
type ListLoadBalancerPackagesOptions = loadbalancer.ListLoadBalancerPackagesOptions
type ListCertificatesOptions = loadbalancer.ListCertificatesOptions
type ListLoadBalancersResult = loadbalancer.ListLoadBalancersResult
type ListCertificatesResult = loadbalancer.ListCertificatesResult
type LoadBalancer = loadbalancer.LoadBalancer
type LoadBalancerNode = loadbalancer.LoadBalancerNode
type LoadBalancerPackage = loadbalancer.LoadBalancerPackage
type Certificate = loadbalancer.Certificate
type LoadBalancerTag = loadbalancer.LoadBalancerTag
type ListenerInsertHeader = loadbalancer.ListenerInsertHeader
type Listener = loadbalancer.Listener
type Pool = loadbalancer.Pool
type PoolMember = loadbalancer.PoolMember
type HealthMonitor = loadbalancer.HealthMonitor
type Policy = loadbalancer.Policy
type L7Rule = loadbalancer.L7Rule

type NetworkService = network.Service
type NetworkListOptions = network.NetworkListOptions
type ListVPCsOptions = network.ListVPCsOptions
type ListWANIPsOptions = network.ListWANIPsOptions
type ListNetworkInterfacesOptions = network.ListNetworkInterfacesOptions
type ListSecurityGroupsOptions = network.ListSecurityGroupsOptions
type ListVirtualIPAddressesOptions = network.ListVirtualIPAddressesOptions
type ListRouteTablesOptions = network.ListRouteTablesOptions
type ListPeeringsOptions = network.ListPeeringsOptions
type ListNetworkACLsOptions = network.ListNetworkACLsOptions
type ListInterconnectsOptions = network.ListInterconnectsOptions
type ListEndpointsOptions = network.ListEndpointsOptions
type ListVPCsResult = network.ListVPCsResult
type ListWANIPsResult = network.ListWANIPsResult
type ListNetworkInterfacesResult = network.ListNetworkInterfacesResult
type ListSecurityGroupsResult = network.ListSecurityGroupsResult
type ListVirtualIPAddressesResult = network.ListVirtualIPAddressesResult
type ListRouteTablesResult = network.ListRouteTablesResult
type ListPeeringsResult = network.ListPeeringsResult
type ListNetworkACLsResult = network.ListNetworkACLsResult
type ListInterconnectsResult = network.ListInterconnectsResult
type ListEndpointsResult = network.ListEndpointsResult
type VPC = network.VPC
type NetworkZone = network.NetworkZone
type WANIP = network.WANIP
type ElasticNetworkInterface = network.ElasticNetworkInterface
type SecurityGroup = network.SecurityGroup
type VirtualIPAddress = network.VirtualIPAddress
type NetworkRoute = network.NetworkRoute
type RouteTable = network.RouteTable
type Peering = network.Peering
type NetworkACL = network.NetworkACL
type Subnet = network.Subnet
type SubnetSecondarySubnet = network.SubnetSecondarySubnet
type SecurityGroupRule = network.SecurityGroupRule
type RouteTableRoute = network.RouteTableRoute
type NetworkEndpoint = network.NetworkEndpoint
type NetworkEndpointDetail = network.NetworkEndpointDetail
type Tag = network.Tag
type Interconnect = network.Interconnect
type AddressPair = network.AddressPair
type VNetworkRegion = network.VNetworkRegion

const (
	DefaultPage     = core.DefaultPage
	DefaultPageSize = core.DefaultPageSize
)

var (
	ErrAuth             = core.ErrAuth
	ErrNotFound         = core.ErrNotFound
	ErrPermission       = core.ErrPermission
	ErrRateLimited      = core.ErrRateLimited
	ErrProjectNotFound  = core.ErrProjectNotFound
	ErrProjectAmbiguous = core.ErrProjectAmbiguous
	ErrMissingProjectID = core.ErrMissingProjectID
	ErrInvalidConfig    = core.ErrInvalidConfig

	WithHTTPClient        = core.WithHTTPClient
	WithTransport         = core.WithTransport
	WithTimeout           = core.WithTimeout
	WithRetry             = core.WithRetry
	WithUserAgent         = core.WithUserAgent
	WithLogger            = core.WithLogger
	WithEndpointOverrides = core.WithEndpointOverrides
	WithResponseCapture   = core.WithResponseCapture

	IsNotFound         = core.IsNotFound
	IsPermissionDenied = core.IsPermissionDenied
	IsRateLimited      = core.IsRateLimited
	IsRetryable        = core.IsRetryable
	ErrorCode          = core.ErrorCode
	IsProjectNotFound  = core.IsProjectNotFound
	IsProjectAmbiguous = core.IsProjectAmbiguous
)

type Client struct {
	*core.Client

	Compute            *compute.Service
	Network            *network.Service
	Volume             *volume.Service
	LoadBalancer       *loadbalancer.Service
	GlobalLoadBalancer *glb.Service
	DNS                *dns.Service
	ContainerRegistry  *containerregistry.Service
	Portal             *portal.Service
}

func NewClient(ctx context.Context, cfg Config, opts ...ClientOption) (*Client, error) {
	_ = ctx
	base, err := core.NewClient(cfg, opts...)
	if err != nil {
		return nil, err
	}
	c := &Client{Client: base}
	c.Compute = compute.New(base)
	c.Network = network.New(base)
	c.Volume = volume.New(base)
	c.LoadBalancer = loadbalancer.New(base)
	c.GlobalLoadBalancer = glb.New(base)
	c.DNS = dns.New(base)
	c.ContainerRegistry = containerregistry.New(base)
	c.Portal = portal.New(base)
	return c, nil
}
