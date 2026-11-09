package vngcloud

import (
	"danny.vn/vngcloud/internal/core"
)

// Ptr returns a pointer to v, for setting an optional field of an update
// Input without a temporary variable.
func Ptr[T any](v T) *T { return &v }

// Config holds resolved shared client configuration.
type Config = core.Config

// LoadOption configures how LoadConfig resolves a profile.
type LoadOption = core.Option

// EndpointOverrides supplies explicit service endpoints.
type EndpointOverrides = core.EndpointOverrides

// IAMUserAuth holds IAM user authentication settings.
type IAMUserAuth = core.IAMUserAuth

// TOTPProvider supplies time-based one-time passwords.
type TOTPProvider = core.TOTPProvider

// TOTPFunc adapts a function to TOTPProvider.
type TOTPFunc = core.TOTPFunc

// SecretTOTP generates time-based codes from a secret.
type SecretTOTP = core.SecretTOTP

// Token holds an access token and its expiration.
type Token = core.Token

// CredentialsProvider supplies and invalidates access tokens.
type CredentialsProvider = core.CredentialsProvider

// ResponseCapture holds one captured response.
type ResponseCapture = core.ResponseCapture

// ResponseCaptureFunc receives captured responses.
type ResponseCaptureFunc = core.ResponseCaptureFunc

// APIError describes a GreenNode API failure.
type APIError = core.APIError

// LoginError describes an IAM user login failure.
type LoginError = core.LoginError

// ServiceAccountLoginError describes a service-account login failure.
type ServiceAccountLoginError = core.ServiceAccountLoginError

const (
	// DefaultPage is the default one-based page number.
	DefaultPage = core.DefaultPage
	// DefaultPageSize is the default list page size.
	DefaultPageSize = core.DefaultPageSize
)

var (
	// ErrAuth marks an authentication failure.
	ErrAuth = core.ErrAuth
	// ErrTokenCache marks a service-account token cache failure.
	ErrTokenCache = core.ErrTokenCache
	// ErrNotFound marks a missing resource.
	ErrNotFound = core.ErrNotFound
	// ErrPermission marks a permission failure.
	ErrPermission = core.ErrPermission
	// ErrRateLimited marks a rate limit response.
	ErrRateLimited = core.ErrRateLimited
	// ErrProjectNotFound marks an empty regional project discovery.
	ErrProjectNotFound = core.ErrProjectNotFound
	// ErrProjectAmbiguous marks project discovery with multiple matches.
	ErrProjectAmbiguous = core.ErrProjectAmbiguous
	// ErrMissingProjectID marks a missing required project ID.
	ErrMissingProjectID = core.ErrMissingProjectID
	// ErrInvalidConfig marks an invalid configuration.
	ErrInvalidConfig = core.ErrInvalidConfig
	// ErrInvalidInput marks an invalid operation input.
	ErrInvalidInput = core.ErrInvalidInput
	// ErrHistoryIncomplete marks a history walk whose completeness is unconfirmed.
	ErrHistoryIncomplete = core.ErrHistoryIncomplete
	// ErrNoCredentials marks missing credentials.
	ErrNoCredentials = core.ErrNoCredentials
	// ErrCredentialsFile marks a credentials file failure.
	ErrCredentialsFile = core.ErrCredentialsFile
	// ErrPriceAboveMax marks a quote above the allowed price.
	ErrPriceAboveMax = core.ErrPriceAboveMax
	// ErrUnpriced marks a quote that cannot price a paid resource.
	ErrUnpriced = core.ErrUnpriced

	// NewConfig builds a configuration from options.
	NewConfig = core.NewConfig
	// LoadConfig resolves a Config from LoadOption values, environment
	// variables, and profile files, highest precedence first; see the
	// Configuration wiki page for the full precedence order and file format.
	LoadConfig = core.LoadConfig
	// WithRegion sets the service region.
	WithRegion = core.WithRegion
	// WithProjectID sets the project ID and bypasses project discovery.
	WithProjectID = core.WithProjectID
	// WithIAMUser sets IAM user credentials.
	WithIAMUser = core.WithIAMUser
	// WithCredentialsProvider sets a custom credentials provider.
	WithCredentialsProvider = core.WithCredentialsProvider
	// WithTokenCache sets the service-account token cache directory.
	WithTokenCache = core.WithTokenCache
	// WithProfile selects a configuration profile.
	WithProfile = core.WithProfile
	// WithConfigFile sets the configuration file path.
	WithConfigFile = core.WithConfigFile
	// WithSharedCredentialsFile sets the shared credentials file path.
	WithSharedCredentialsFile = core.WithSharedCredentialsFile
	// WithHTTPClient sets the HTTP client.
	WithHTTPClient = core.WithHTTPClient
	// WithTransport sets the HTTP round tripper.
	WithTransport = core.WithTransport
	// WithTimeout sets the HTTP request timeout.
	WithTimeout = core.WithTimeout
	// WithRetry sets the retry count and interval.
	WithRetry = core.WithRetry
	// WithUserAgent sets the HTTP user agent.
	WithUserAgent = core.WithUserAgent
	// WithLogger sets the debug logger.
	WithLogger = core.WithLogger
	// WithEndpointOverrides sets explicit service endpoints.
	WithEndpointOverrides = core.WithEndpointOverrides
	// WithResponseCapture sets a response capture hook.
	WithResponseCapture = core.WithResponseCapture
	// WithStaticToken sets a fixed bearer token.
	WithStaticToken = core.WithStaticToken
	// WithCDNAPIKey sets a CDN API key.
	WithCDNAPIKey = core.WithCDNAPIKey
	// IsNotFound reports whether an error indicates a missing resource.
	IsNotFound = core.IsNotFound
	// IsPermissionDenied reports whether an error indicates denied permission.
	IsPermissionDenied = core.IsPermissionDenied
	// IsRateLimited reports whether an error indicates rate limiting.
	IsRateLimited = core.IsRateLimited
	// IsRetryable reports whether an API error is marked retryable.
	IsRetryable = core.IsRetryable
	// ErrorCode returns an API error code when available.
	ErrorCode = core.ErrorCode
	// IsProjectNotFound reports whether project discovery found no project.
	IsProjectNotFound = core.IsProjectNotFound
	// IsProjectAmbiguous reports whether project discovery found multiple projects.
	IsProjectAmbiguous = core.IsProjectAmbiguous
)
