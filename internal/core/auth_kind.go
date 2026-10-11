package core

// UsesIAMUserLogin reports SDK-owned login provenance, without inspecting tokens.
// A custom provider never establishes this provenance, even if it uses IAM.
func (c *Client) UsesIAMUserLogin() bool {
	return c.iamUserLogin
}
