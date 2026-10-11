package vngcloud

import "danny.vn/vngcloud/internal/core"

// ServiceAccountAuth holds machine credentials for GreenNode authentication.
type ServiceAccountAuth struct {
	ClientID     string
	ClientSecret Secret
}

// WithServiceAccount snapshots auth when the option is built. Nil is unset;
// a non-nil value selects service-account auth even when its fields are empty.
// Providers and static tokens take precedence over service-account credentials.
func WithServiceAccount(auth *ServiceAccountAuth) LoadOption {
	if auth == nil {
		return core.WithServiceAccountCredentials("", "", false)
	}
	return core.WithServiceAccountCredentials(auth.ClientID, auth.ClientSecret.Reveal(), true)
}
