package core

// UsesIAMUserLogin reports SDK-owned login provenance, without inspecting tokens.
// Service accounts, static tokens, and custom providers never establish
// IAM-user provenance, even when a provider uses IAM.
func (c *Client) UsesIAMUserLogin() bool {
	return c.iamUserLogin
}
