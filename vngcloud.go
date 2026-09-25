package vngcloud

import (
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/glb"
	"danny.vn/vngcloud/internal/loadbalancer"
	"danny.vn/vngcloud/internal/sdk"
)

// Ptr returns a pointer to v, for setting an optional field of an update
// Input without a temporary variable.
func Ptr[T any](v T) *T { return &v }

type Client = sdk.Client
type Config = core.Config
type LoadOption = core.Option
type EndpointOverrides = core.EndpointOverrides

type IAMUserAuth = core.IAMUserAuth
type TOTPProvider = core.TOTPProvider
type TOTPFunc = core.TOTPFunc
type SecretTOTP = core.SecretTOTP

type ResponseCapture = core.ResponseCapture
type ResponseCaptureFunc = core.ResponseCaptureFunc

type APIError = core.APIError
type ListOptions = core.ListOptions
type Page = core.Page
type ListResult[T any] = core.ListResult[T]

type Project = core.Project
type ListProjectsOptions = core.ListProjectsOptions

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
	ErrInvalidInput     = core.ErrInvalidInput

	NewConfig             = core.NewConfig
	NewClient             = sdk.NewClient
	WithRegion            = core.WithRegion
	WithProjectID         = core.WithProjectID
	WithIAMUser           = core.WithIAMUser
	WithHTTPClient        = core.WithHTTPClient
	WithTransport         = core.WithTransport
	WithTimeout           = core.WithTimeout
	WithRetry             = core.WithRetry
	WithUserAgent         = core.WithUserAgent
	WithLogger            = core.WithLogger
	WithEndpointOverrides = core.WithEndpointOverrides
	WithResponseCapture   = core.WithResponseCapture
	WithStaticToken       = core.WithStaticToken

	IsNotFound         = core.IsNotFound
	IsPermissionDenied = core.IsPermissionDenied
	IsRateLimited      = core.IsRateLimited
	IsRetryable        = core.IsRetryable
	ErrorCode          = core.ErrorCode
	IsProjectNotFound  = core.IsProjectNotFound
	IsProjectAmbiguous = core.IsProjectAmbiguous
)
