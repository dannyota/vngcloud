package globalloadbalancer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"danny.vn/vngcloud/internal/core"
)

type deletePlan struct {
	r         *run
	url       string
	id        string
	exact     func(context.Context) (id, status string, err error)
	listed    func(context.Context) (bool, error)
	survivors func(context.Context) error
	settled   func()
}

func (p deletePlan) execute(ctx context.Context) error {
	if err := p.r.send(ctx, http.MethodDelete, p.url, nil, nil); err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, deleteWait)
	defer cancel()
	for {
		present, err := p.listed(wait)
		if err != nil {
			return notSettled("delete confirmation interrupted")
		}
		id, status, exactErr := p.exact(wait)
		consistent := core.IsNotFound(exactErr) || (exactErr != nil && p.r.internalError)
		switch {
		case exactErr == nil && id != p.id:
			return notSettled("delete detail ID mismatch")
		case exactErr == nil && !deleteInProgress(status):
			return notSettled("delete status unrecognized")
		case exactErr != nil && !consistent:
			return notSettled("delete confirmation interrupted")
		case !present && exactErr != nil:
			if p.survivors(wait) != nil {
				return notSettled("delete absence could not be proven")
			}
			p.settled()
			return nil
		}
		if !p.r.pause(wait) {
			return notSettled("delete confirmation interrupted")
		}
	}
}

func (c *Client) deletePrelude(ctx context.Context, op, glb string, ids map[string]string) (*run, error) {
	for field, value := range ids {
		if err := validID(op, field, value); err != nil {
			return nil, err
		}
	}
	if err := c.childProvenance(); err != nil {
		return nil, err
	}
	r := c.newRun(op)
	if _, err := r.parent(ctx, glb); err != nil {
		return nil, err
	}
	return r, nil
}

func unsettled(err error) bool { return errors.Is(err, ErrNotSettled) }

func (r *run) inPool(ctx context.Context, glb, poolID string) (wirePool, error) {
	pools, err := r.pools(ctx, glb)
	if err != nil {
		return wirePool{}, err
	}
	if len(pools) == 0 {
		return wirePool{}, fmt.Errorf("%w: the load balancer holds no pool", core.ErrNotFound)
	}
	if len(pools) != 1 || pools[0].ID != poolID {
		return wirePool{}, refuse("the load balancer must hold exactly the expected pool")
	}
	p, err := r.pool(ctx, glb, poolID)
	if err != nil {
		return p, err
	}
	if !checkPool(p, glb, poolID, "") {
		return p, refuse("the pool does not match the supported TCP configuration")
	}
	return p, nil
}

func (r *run) survivors(ctx context.Context, glb, poolID string) error {
	if _, err := r.parent(ctx, glb); err != nil {
		return err
	}
	if poolID == "" {
		return nil
	}
	_, err := r.inPool(ctx, glb, poolID)
	return err
}

// DeleteListener deletes one listener of a pool-backed Layer 4 load balancer
// and confirms its absence. It never deletes the pool. See
// docs/design/glb-pool.md.
func (c *Client) DeleteListener(ctx context.Context, in *DeleteListenerInput) (*DeleteListenerOutput, error) {
	const op = "globalloadbalancer.DeleteListener"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	r, err := c.deletePrelude(ctx, op, in.LoadBalancerID, map[string]string{"LoadBalancerID": in.LoadBalancerID, "ListenerID": in.ListenerID})
	if err != nil {
		return nil, err
	}
	l, err := r.listener(ctx, in.LoadBalancerID, in.ListenerID)
	if err != nil {
		return nil, err
	}
	all, err := r.listeners(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	if l.ID != in.ListenerID || l.GlobalLoadBalancerID != in.LoadBalancerID || l.Protocol != wireProtocol || !deleteEligible(l.Status) || len(all) != 1 || all[0].ID != in.ListenerID || validID(op, "PoolID", l.GlobalPoolID) != nil {
		return nil, refuse("the listener is not an eligible sole TCP listener")
	}
	if _, err := r.inPool(ctx, in.LoadBalancerID, l.GlobalPoolID); err != nil {
		return nil, err
	}
	out := &DeleteListenerOutput{LoadBalancerID: in.LoadBalancerID, ListenerID: in.ListenerID}
	plan := deletePlan{r: r, url: c.glbChildURL(in.LoadBalancerID, "global-listeners", in.ListenerID), id: in.ListenerID,
		exact: func(ctx context.Context) (string, string, error) {
			got, err := r.listener(ctx, in.LoadBalancerID, in.ListenerID)
			return got.ID, got.Status, err
		},
		listed: func(ctx context.Context) (bool, error) {
			rest, err := r.listeners(ctx, in.LoadBalancerID)
			return slices.ContainsFunc(rest, func(x wireListener) bool { return x.ID == in.ListenerID }), err
		},
		survivors: func(ctx context.Context) error { return r.survivors(ctx, in.LoadBalancerID, l.GlobalPoolID) },
		settled:   func() { out.Deleted = true }}
	return finishDelete(out, plan.execute(ctx))
}

// DeletePool deletes one listener-free pool together with its one private
// member group and backend row, never the vServer, and confirms the pool's
// absence. See docs/design/glb-pool.md.
func (c *Client) DeletePool(ctx context.Context, in *DeletePoolInput) (*DeletePoolOutput, error) {
	const op = "globalloadbalancer.DeletePool"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	r, err := c.deletePrelude(ctx, op, in.LoadBalancerID, map[string]string{"LoadBalancerID": in.LoadBalancerID, "PoolID": in.PoolID})
	if err != nil {
		return nil, err
	}
	p, err := r.inPool(ctx, in.LoadBalancerID, in.PoolID)
	if err != nil {
		return nil, err
	}
	if !deleteEligible(p.Status) || p.Health == nil || !deleteEligible(p.Health.Status) {
		return nil, refuse("the pool is not in a stable state")
	}
	if err := r.requireNoListeners(ctx, in.LoadBalancerID); err != nil {
		return nil, err
	}
	groups, err := r.groups(ctx, in.LoadBalancerID, in.PoolID)
	if err != nil {
		return nil, err
	}
	if len(groups) != 1 {
		return nil, refuse("the pool must hold exactly one member group")
	}
	g, err := r.group(ctx, in.LoadBalancerID, in.PoolID, groups[0].ID)
	if err != nil {
		return nil, err
	}
	if !checkGroup(g, in.LoadBalancerID, in.PoolID, groups[0].ID, nil) || !memberHealth(g.Status) || !memberHealth(g.Members[0].Status) {
		return nil, refuse("the member group is not the expected private group with one backend")
	}
	out := &DeletePoolOutput{LoadBalancerID: in.LoadBalancerID, PoolID: in.PoolID}
	plan := deletePlan{r: r, url: c.glbChildURL(in.LoadBalancerID, "global-pools", in.PoolID), id: in.PoolID,
		exact: func(ctx context.Context) (string, string, error) {
			got, err := r.pool(ctx, in.LoadBalancerID, in.PoolID)
			return got.ID, got.Status, err
		},
		listed: func(ctx context.Context) (bool, error) {
			rest, err := r.pools(ctx, in.LoadBalancerID)
			return slices.ContainsFunc(rest, func(x wirePool) bool { return x.ID == in.PoolID }), err
		},
		survivors: func(ctx context.Context) error { return r.survivors(ctx, in.LoadBalancerID, "") },
		settled:   func() { out.Deleted = true }}
	return finishDelete(out, plan.execute(ctx))
}

func finishDelete[T any](out *T, err error) (*T, error) {
	if err == nil {
		return out, nil
	}
	if unsettled(err) {
		return out, err
	}
	return nil, err
}
