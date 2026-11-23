package globalloadbalancer

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
)

// CreatePool sends one pool create request with one private member group and
// one vServer backend, then waits for exact read-back. See
// docs/design/glb-pool.md.
func (c *Client) CreatePool(ctx context.Context, in *CreatePoolInput) (*CreatePoolOutput, error) {
	r := c.newRun("globalloadbalancer.CreatePool")
	out, err := c.createPool(ctx, r, in)
	return finishOutput(r, out, err)
}

func (c *Client) createPool(ctx context.Context, r *run, in *CreatePoolInput) (*CreatePoolOutput, error) {
	const op = "globalloadbalancer.CreatePool"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	for field, value := range map[string]string{"LoadBalancerID": in.LoadBalancerID, "BackendServerID": in.BackendServerID, "BackendVPCID": in.BackendVPCID, "BackendSubnetID": in.BackendSubnetID} {
		if err := validID(op, field, value); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.MemberName) == "" || in.BackendPort < 1 || in.BackendPort > 65535 {
		return nil, refuse(op + " requires nonblank names and a port from 1 through 65535")
	}
	if err := c.childProvenance(); err != nil {
		return nil, err
	}
	project, err := r.project(ctx, in.BackendProjectID)
	if err != nil {
		return nil, err
	}
	parent, err := r.parent(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	regionID, err := r.region(ctx, parent.Package)
	if err != nil {
		return nil, err
	}
	place, err := r.placement(ctx, project, in.BackendServerID, in.BackendVPCID, in.BackendSubnetID)
	if err != nil {
		return nil, err
	}
	if err := r.requireEmpty(ctx, in.LoadBalancerID); err != nil {
		return nil, err
	}
	body := poolBody{Name: in.Name, Protocol: wireProtocol, Algorithm: wireAlgorithm,
		Health: healthBody{Protocol: wireProtocol, HealthyThreshold: wireHealthy, UnhealthyThreshold: wireUnhealthy, Interval: wireInterval, Timeout: wireHealthTime},
		Groups: []groupBody{{Name: in.MemberName, Region: regionID, Type: wireGroupType, VPCID: in.BackendVPCID, TrafficDial: wireTrafficDial,
			Members: []rowBody{{Name: in.MemberName, Address: place.Address, SubnetID: in.BackendSubnetID, Port: in.BackendPort, MonitorPort: in.BackendPort, Weight: wireWeight}}}}}
	var raw json.RawMessage
	if err := r.send(ctx, http.MethodPost, c.glbChildURL(in.LoadBalancerID, "global-pools"), body, &raw); err != nil {
		return nil, err
	}
	var accept wireAccept
	if r.decode(raw, &accept) != nil || validID(op, "PoolID", accept.ID) != nil || accept.GlobalLoadBalancerID != in.LoadBalancerID {
		return nil, notSettled("create response lacks a valid pool")
	}
	out := &CreatePoolOutput{Pool: Pool{ID: accept.ID}}
	if len(accept.Groups) != 1 || validID(op, "PoolMemberID", accept.Groups[0].ID) != nil || len(accept.Groups[0].Members) > 1 {
		return out, notSettled("create response lacks a valid member group")
	}
	groupID := accept.Groups[0].ID
	rowID := ""
	if len(accept.Groups[0].Members) == 1 {
		rowID = accept.Groups[0].Members[0].ID
		if validID(op, "PoolMemberID", rowID) != nil {
			return out, notSettled("create response has an invalid backend")
		}
	}
	out.PoolMember = PoolMember{ID: groupID}
	want := &backendWant{Name: in.MemberName, Region: regionID, VPC: in.BackendVPCID, Subnet: in.BackendSubnetID, Address: place.Address, Port: in.BackendPort}
	spec := chainSpec{glb: in.LoadBalancerID, poolID: accept.ID, groupID: groupID, poolName: in.Name, backend: want}
	wait, cancel := context.WithTimeout(ctx, createWait)
	defer cancel()
	for {
		parentClass, err := r.parentWhileCreating(wait, in.LoadBalancerID)
		var got chain
		if err == nil {
			got, err = r.readChain(wait, spec)
		}
		if err == nil {
			err = r.requireNoListeners(wait, in.LoadBalancerID)
		}
		if err == nil && rowID != "" && got.group.Members[0].ID != rowID {
			err = refuse("backend ID mismatch")
		}
		if err != nil {
			return out, notSettled("create confirmation could not be proven")
		}
		if got.verdict == stateSuccess && parentClass == stateTransitional {
			got.verdict = stateTransitional
		}
		switch got.verdict {
		case stateSuccess:
			out.Pool, out.PoolMember = got.pool.public(), got.group.public()
			return out, nil
		case stateFailed:
			return out, ErrFailed
		case stateTransitional:
		default:
			return out, notSettled("create status unrecognized")
		}
		if !r.pause(wait) {
			return out, notSettled("create confirmation interrupted")
		}
	}
}

func (r *run) requireEmpty(ctx context.Context, glb string) error {
	pools, err := r.pools(ctx, glb)
	if err != nil {
		return err
	}
	if len(pools) != 0 {
		return refuse("the load balancer already has a pool")
	}
	listeners, err := r.listeners(ctx, glb)
	if err != nil {
		return err
	}
	if len(listeners) != 0 {
		return refuse("the load balancer already has a listener")
	}
	return nil
}

func (r *run) requireNoListeners(ctx context.Context, glb string) error {
	listeners, err := r.listeners(ctx, glb)
	if err != nil {
		return err
	}
	if len(listeners) != 0 {
		return refuse("the load balancer has a listener")
	}
	return nil
}
