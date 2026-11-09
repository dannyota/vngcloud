package compute

// ListServersInput selects a page of servers.
type ListServersInput struct {
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// GetServerInput identifies a server to read.
type GetServerInput struct {
	// ServerID is required; use ASCII letters, digits, or hyphens.
	ServerID string `vngcloud:"required"`
}

// ListSSHKeysInput selects a page of SSH keys.
type ListSSHKeysInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// GetSSHKeyInput identifies an SSH key to read.
type GetSSHKeyInput struct {
	// SSHKeyID is required; use ASCII letters, digits, or hyphens.
	SSHKeyID string `vngcloud:"required"`
}

// ListServerGroupsInput selects an offset and limit for server groups.
type ListServerGroupsInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page is an offset, not a page number: it starts at 0, and each
	// value names the index of the first item to return.
	Page int
	// Size is the limit; a nonpositive value uses the default.
	Size int
}

// GetServerGroupInput identifies a server group to read.
type GetServerGroupInput struct {
	// ServerGroupID is required; use ASCII letters, digits, or hyphens.
	ServerGroupID string `vngcloud:"required"`
}

// ListServerSecurityGroupsInput has no filters.
type ListServerSecurityGroupsInput struct{}

// ListServerGroupMembersInput has no filters.
type ListServerGroupMembersInput struct{}

// ListServerGroupPoliciesInput has no filters.
type ListServerGroupPoliciesInput struct{}

// ListOSImagesInput optionally filters images by zone.
type ListOSImagesInput struct {
	// ZoneID is sent as the zoneId query parameter when nonempty.
	ZoneID string
}

// ListGPUImagesInput has no filters.
type ListGPUImagesInput struct{}

// ListUserImagesInput selects a page of user images.
type ListUserImagesInput struct {
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}
