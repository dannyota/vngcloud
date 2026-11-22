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
