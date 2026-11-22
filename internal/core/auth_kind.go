package core

import "danny.vn/vngcloud/internal/endpoints"

// UsesDefaultAuthEndpoints reports whether IAM login uses built-in endpoints.
func (c *Client) UsesDefaultAuthEndpoints() bool { return c.defaultAuthEndpoints }

func defaultAuthEndpoints(auth *IAMUserAuth, actual endpoints.Set, region string) bool {
	if auth == nil {
		return false
	}
	expected := endpoints.ResolveIAMUser(region, endpoints.Overrides{})
	return actual.Signin == expected.Signin && actual.Token == expected.Token && actual.Dashboard == expected.Dashboard && firstNonEmpty(auth.SigninBaseURL, actual.Signin) == expected.Signin && firstNonEmpty(auth.TokenURL, actual.Token) == expected.Token && firstNonEmpty(auth.DashboardURI, actual.Dashboard) == expected.Dashboard
}

// UsesIAMUserLogin reports SDK-owned login provenance, without inspecting tokens.
// Service accounts, static tokens, and custom providers never establish
// IAM-user provenance, even when a provider uses IAM.
func (c *Client) UsesIAMUserLogin() bool {
	return c.iamUserLogin
}

// UsesServiceAccountLogin identifies the selected SDK-owned auth kind for
// internal CLI cache preflight. It performs no authentication or token reads.
// The public SDK does not expose this helper.
func UsesServiceAccountLogin(cfg Config) bool {
	return ClientOf(cfg).serviceAccountLogin
}
