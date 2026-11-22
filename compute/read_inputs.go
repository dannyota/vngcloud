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
	// Offset is the zero-based index of the first item; nonpositive values use zero.
	Offset int
	// Limit is the maximum item count; nonpositive values use the default.
	Limit int
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
