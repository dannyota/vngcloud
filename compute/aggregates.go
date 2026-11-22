package compute

import (
	"context"

	"danny.vn/vngcloud/internal/core"
)

// ListServerSecurityGroupsOutput holds ServerSecurityGroup items.
type ListServerSecurityGroupsOutput = core.List[ServerSecurityGroup]

// ListServerSecurityGroups reads server security groups.
func (c *Client) ListServerSecurityGroups(ctx context.Context, _ *ListServerSecurityGroupsInput) (out *ListServerSecurityGroupsOutput, err error) {
	ctx, scope := serverOperationContext(ctx)
	defer finishServerOperation(scope, "compute.ListServerSecurityGroups", &out, &err)
	servers, err := walkComputePages(ctx, "compute.ListServerSecurityGroups", func(page int) (*ListServersOutput, error) { return c.ListServers(ctx, &ListServersInput{Page: page}) }, func(server Server) string { return server.UUID })
	if err != nil {
		return nil, err
	}
	items := make([]ServerSecurityGroup, 0)
	for _, server := range servers {
		if !validComputeChildren("compute.ListServerSecurityGroups", server.SecurityGroups, func(group ServerSecgroup) string { return group.UUID }) {
			return nil, invalidServerResponse("compute.ListServerSecurityGroups", 200, nil)
		}
		for _, secgroup := range server.SecurityGroups {
			items = append(items, ServerSecurityGroup{
				ServerID: server.UUID,
				Name:     secgroup.Name,
				UUID:     secgroup.UUID,
			})
		}
	}
	return &ListServerSecurityGroupsOutput{Items: items}, nil
}

// ListServerGroupMembersOutput holds ServerGroupMembership items.
type ListServerGroupMembersOutput = core.List[ServerGroupMembership]

// ListServerGroupMembers reads server group members.
func (c *Client) ListServerGroupMembers(ctx context.Context, _ *ListServerGroupMembersInput) (out *ListServerGroupMembersOutput, err error) {
	ctx, scope := serverOperationContext(ctx)
	defer finishServerOperation(scope, "compute.ListServerGroupMembers", &out, &err)
	groups, err := c.allServerGroups(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]ServerGroupMembership, 0)
	for _, group := range groups {
		if !validComputeChildren("compute.ListServerGroupMembers", group.Servers, func(server ServerGroupMember) string { return server.UUID }) {
			return nil, invalidServerResponse("compute.ListServerGroupMembers", 200, nil)
		}
		for _, server := range group.Servers {
			items = append(items, ServerGroupMembership{
				ServerGroupID: group.UUID,
				Name:          server.Name,
				UUID:          server.UUID,
			})
		}
	}
	return &ListServerGroupMembersOutput{Items: items}, nil
}

func validComputeChildren[T any](op string, items []T, identity func(T) string) bool {
	if items == nil {
		return false
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		id := identity(item)
		if core.CheckPathID(op, "resource ID", id) != nil || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
