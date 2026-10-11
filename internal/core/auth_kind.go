package core

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
