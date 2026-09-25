package vngcloud

import (
	"danny.vn/vngcloud/internal/core"
)

// Ptr returns a pointer to v, for setting an optional field of an update
// Input without a temporary variable.
func Ptr[T any](v T) *T { return &v }

type Config = core.Config
type LoadOption = core.Option
type EndpointOverrides = core.EndpointOverrides

type IAMUserAuth = core.IAMUserAuth
type TOTPProvider = core.TOTPProvider
type TOTPFunc = core.TOTPFunc
type SecretTOTP = core.SecretTOTP

type Token = core.Token
type CredentialsProvider = core.CredentialsProvider

type ResponseCapture = core.ResponseCapture
type ResponseCaptureFunc = core.ResponseCaptureFunc

type APIError = core.APIError
type LoginError = core.LoginError

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
	ErrNoCredentials    = core.ErrNoCredentials
	ErrCredentialsFile  = core.ErrCredentialsFile

	NewConfig = core.NewConfig
	// LoadConfig resolves a Config from LoadOption values, environment
	// variables, and profile files, highest precedence first; see the
	// Configuration wiki page for the full precedence order and file format.
	LoadConfig                = core.LoadConfig
	WithRegion                = core.WithRegion
	WithProjectID             = core.WithProjectID
	WithIAMUser               = core.WithIAMUser
	WithCredentialsProvider   = core.WithCredentialsProvider
	WithTokenCache            = core.WithTokenCache
	WithProfile               = core.WithProfile
	WithConfigFile            = core.WithConfigFile
	WithSharedCredentialsFile = core.WithSharedCredentialsFile
	WithHTTPClient            = core.WithHTTPClient
	WithTransport             = core.WithTransport
	WithTimeout               = core.WithTimeout
	WithRetry                 = core.WithRetry
	WithUserAgent             = core.WithUserAgent
	WithLogger                = core.WithLogger
	WithEndpointOverrides     = core.WithEndpointOverrides
	WithResponseCapture       = core.WithResponseCapture
	WithStaticToken           = core.WithStaticToken

	IsNotFound         = core.IsNotFound
	IsPermissionDenied = core.IsPermissionDenied
	IsRateLimited      = core.IsRateLimited
	IsRetryable        = core.IsRetryable
	ErrorCode          = core.ErrorCode
	IsProjectNotFound  = core.IsProjectNotFound
	IsProjectAmbiguous = core.IsProjectAmbiguous
)
