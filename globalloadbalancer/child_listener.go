package globalloadbalancer

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
)

// CreateListener sends one TCP listener create request bound to the load
// balancer's only pool, then waits for exact read-back. See
// docs/design/glb-pool.md.
func (c *Client) CreateListener(ctx context.Context, in *CreateListenerInput) (*CreateListenerOutput, error) {
	const op = "globalloadbalancer.CreateListener"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	for field, value := range map[string]string{"LoadBalancerID": in.LoadBalancerID, "PoolID": in.PoolID, "BackendServerID": in.BackendServerID} {
		if err := validID(op, field, value); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(in.Name) == "" || in.Port < 1 || in.Port > 65535 {
		return nil, refuse(op + " requires a nonblank name and a port from 1 through 65535")
	}
	cidrs, err := canonicalCIDRs(in.AllowedCIDRs)
	if err != nil {
		return nil, err
	}
	if err := c.childProvenance(); err != nil {
		return nil, err
	}
	r := c.newRun(op)
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
	if err := r.requireNoListeners(ctx, in.LoadBalancerID); err != nil {
		return nil, err
	}
	spec := chainSpec{glb: in.LoadBalancerID, poolID: in.PoolID}
	got, err := r.readChain(ctx, spec)
	if err != nil {
		return nil, err
	}
	if got.verdict != stateSuccess || got.group.Region != regionID {
		return nil, refuse("the pool and member group are not settled in the verified region")
	}
	spec.groupID = got.group.ID
	row := got.group.Members[0]
	place, err := r.placement(ctx, project, in.BackendServerID, got.group.VPCID, row.SubnetID)
	if err != nil {
		return nil, err
	}
	if place.Address != row.Address {
		return nil, refuse("the pool backend address does not match the server")
	}
	body := listenerBody{Name: in.Name, Protocol: wireProtocol, Port: in.Port, GlobalPoolID: in.PoolID, AllowedCIDRs: cidrs, TimeoutClient: wireTimeoutCli, TimeoutConnection: wireTimeoutConn, TimeoutMember: wireTimeoutMem}
	var raw json.RawMessage
	if err := r.send(ctx, http.MethodPost, c.glbChildURL(in.LoadBalancerID, "global-listeners"), body, &raw); err != nil {
		return nil, err
	}
	var accept wireAccept
	if r.decode(raw, &accept) != nil || validID(op, "ListenerID", accept.ID) != nil || accept.GlobalLoadBalancerID != in.LoadBalancerID || accept.GlobalPoolID != in.PoolID {
		return nil, notSettled("create response lacks a valid listener")
	}
	out := &CreateListenerOutput{Listener: Listener{ID: accept.ID}}
	wait, cancel := context.WithTimeout(ctx, createWait)
	defer cancel()
	for {
		l, err := r.listener(wait, in.LoadBalancerID, accept.ID)
		var all []wireListener
		if err == nil {
			all, err = r.listeners(wait, in.LoadBalancerID)
		}
		var again chain
		if err == nil {
			again, err = r.readChain(wait, spec)
		}
		if err != nil || len(all) != 1 || all[0].ID != accept.ID || again.verdict != stateSuccess || again.group.Members[0].Address != place.Address ||
			l.ID != accept.ID || l.GlobalLoadBalancerID != in.LoadBalancerID || l.GlobalPoolID != in.PoolID || l.Name != in.Name || l.Protocol != wireProtocol || l.Port != in.Port ||
			l.TimeoutClient != wireTimeoutCli || l.TimeoutConnection != wireTimeoutConn || l.TimeoutMember != wireTimeoutMem || !cidrSetEqual(l.AllowedCIDRs, in.AllowedCIDRs) {
			return out, notSettled("create confirmation could not be proven")
		}
		switch createState(l.Status) {
		case stateSuccess:
			out.Listener = l.public()
			return out, nil
		case stateFailed:
			out.Listener = l.public()
			return out, ErrFailed
		case stateTransitional:
			out.Listener = l.public()
		default:
			return out, notSettled("create status unrecognized")
		}
		if !r.pause(wait) {
			return out, notSettled("create confirmation interrupted")
		}
	}
}
