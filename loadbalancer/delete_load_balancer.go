package loadbalancer

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// lbDeletePollInterval and lbDeleteBound time DeleteLoadBalancer's
// post-delete wait, per the design's wait table.
const (
	lbDeletePollInterval = 10 * time.Second
	lbDeleteBound        = 15 * time.Minute
)

// DeleteLoadBalancerInput identifies the load balancer to delete.
type DeleteLoadBalancerInput struct {
	LoadBalancerID string `vngcloud:"required"`

	NoWait bool
}

type DeleteLoadBalancerOutput struct{}

// DeleteLoadBalancer deletes a load balancer. It reads the load balancer
// first: a 404 there is returned as core.ErrNotFound, and a load balancer
// already lbStatusDeleting is waited on below without a second DELETE, since
// an earlier call already sent it. What the server does with the load
// balancer's listeners and pools, if any exist, is not decided by this SDK;
// the server's own behavior applies.
//
// The DELETE keeps the transport's normal retries (DELETE is idempotent); a
// retry that finds the load balancer already gone returns core.ErrNotFound.
//
// Without NoWait, DeleteLoadBalancer then waits up to 15 minutes, polling
// every 10 seconds, for GetLoadBalancer to return 404. If a read shows
// lbStatusError instead, the returned error wraps ErrFailed; if the bound
// runs out, or a read or a sleep fails, such as from a canceled ctx, it
// wraps ErrNotSettled, whose message says a rerun is safe: DeleteLoadBalancer
// always reads first.
func (c *Client) DeleteLoadBalancer(ctx context.Context, in *DeleteLoadBalancerInput) (*DeleteLoadBalancerOutput, error) {
	const op = "loadbalancer.DeleteLoadBalancer"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}

	unlock, err := c.lockLoadBalancer(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	current, err := c.GetLoadBalancer(ctx, &GetLoadBalancerInput{LoadBalancerID: in.LoadBalancerID})
	if err != nil {
		return nil, err
	}

	if current.LoadBalancer.ProgressStatus != lbStatusDeleting {
		projectID, err := c.c.RequireProjectID(ctx)
		if err != nil {
			return nil, err
		}
		req := transport.Request{
			Operation: op,
			Method:    http.MethodDelete,
			URL:       c.lbURL([]string{projectID, "loadBalancers", in.LoadBalancerID}, nil),
			OK:        httpStatusOKWrite,
		}
		if err := c.c.DoJSON(ctx, req, nil); err != nil {
			return nil, err
		}
	}

	if in.NoWait {
		return &DeleteLoadBalancerOutput{}, nil
	}
	if err := c.waitLoadBalancerDeleted(ctx, op, in.LoadBalancerID); err != nil {
		return &DeleteLoadBalancerOutput{}, err
	}
	return &DeleteLoadBalancerOutput{}, nil
}

// waitLoadBalancerDeleted is DeleteLoadBalancer's post-delete wait unless
// NoWait is set: it reads id with GetLoadBalancer until that read reports
// core.ErrNotFound (settled) or the load balancer's ProgressStatus is
// lbStatusError (failed); any other status, or any other read outcome,
// keeps it polling.
func (c *Client) waitLoadBalancerDeleted(ctx context.Context, op, id string) error {
	err := poll(ctx, c.now, c.sleep, 0, lbDeletePollInterval, lbDeleteBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetLoadBalancer(ctx, &GetLoadBalancerInput{LoadBalancerID: id})
			if err != nil {
				if core.IsNotFound(err) {
					return true, nil
				}
				return true, err
			}
			if out.LoadBalancer.ProgressStatus == lbStatusError {
				return true, fmt.Errorf("%w: %s: load balancer %s is ERROR", ErrFailed, op, id)
			}
			return false, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: load balancer %s did not reach 404 within %s; delete was sent and a rerun is safe",
				ErrNotSettled, op, id, lbDeleteBound)
		},
	)
	return wrapNotSettled(op, "load balancer "+id, err)
}
