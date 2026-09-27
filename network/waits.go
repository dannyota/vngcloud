package network

import (
	"context"
	"errors"
	"fmt"
	"time"

	"danny.vn/vngcloud/internal/core"
)

// pollInterval and pollBound set CreateSecurityGroup's post-create wait, as
// in vDNS's own waits: a plain GetSecurityGroup at once and then every
// pollInterval, honoring ctx, until pollBound has elapsed since the wait
// began.
const (
	pollInterval = 2 * time.Second
	pollBound    = 60 * time.Second
)

// sleepFunc waits for d or ctx's end, whichever comes first, returning
// ctx.Err() when ctx ends first. Tests inject a fake one so the real
// 2-second and 60-second bounds never really elapse.
type sleepFunc func(ctx context.Context, d time.Duration) error

// clockFunc reads the current time. poll uses it, alongside a sleepFunc, to
// bound a wait by elapsed wall time rather than by counting poll intervals,
// so a slow read itself counts against the bound. Tests inject a fake
// clock; production uses time.Now.
type clockFunc func() time.Time

// contextSleep is the real sleepFunc.
func contextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// poll runs step at once, then again every interval, until step reports
// stop true or bound has elapsed, by now, since poll's first call to step.
// Elapsed time is read from now rather than counted in interval steps, so a
// step that itself takes real time, such as a slow read, counts against
// the bound instead of only the sleeps between steps; a test injects both a
// fake clock and a sleepFunc that returns quickly. Each write's own
// interval and bound come from its design; see vpcs_write.go and
// subnets_write.go for the values this package uses today. This is the
// same shape as vDNS's own unexported poll, duplicated here rather than
// shared, since neither package imports the other.
func poll(ctx context.Context, now clockFunc, sleep sleepFunc, interval, bound time.Duration, step func(ctx context.Context) (stop bool, err error), onTimeout func() error) error {
	deadline := now().Add(bound)
	for {
		stop, err := step(ctx)
		if stop {
			return err
		}
		if !now().Before(deadline) {
			return onTimeout()
		}
		if err := sleep(ctx, interval); err != nil {
			return err
		}
	}
}

// waitSecurityGroupActive is CreateSecurityGroup's post-create wait unless
// NoWait is set: it reads groupID with GetSecurityGroup until its Status
// reaches securityGroupStatusActive or securityGroupStatusError; any other
// status, CREATING or one this SDK does not recognize, keeps it polling. A
// 404 during the wait also keeps polling rather than failing at once, since
// a group just created may not be readable yet; any other read failure
// stops the wait and is returned as is.
//
// It returns the last group a read returned alongside the outcome: nil
// error once ACTIVE, an error wrapping ErrFailed on ERROR, or an error
// wrapping ErrNotSettled once the bound runs out or a read or a sleep
// fails, such as from a canceled ctx. The returned group is nil only when
// no read ever succeeded, in which case the caller falls back to whatever
// the create response itself produced.
func (c *Client) waitSecurityGroupActive(ctx context.Context, op, groupID string) (*SecurityGroup, error) {
	var group *SecurityGroup
	err := poll(ctx, c.now, c.sleep, pollInterval, pollBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetSecurityGroup(ctx, &GetSecurityGroupInput{SecurityGroupID: groupID})
			if err != nil {
				if core.IsNotFound(err) {
					return false, nil
				}
				return true, err
			}
			group = &out.SecurityGroup
			switch group.Status {
			case securityGroupStatusActive:
				return true, nil
			case securityGroupStatusError:
				return true, fmt.Errorf("%w: %s: security group %s is ERROR", ErrFailed, op, groupID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: security group %s did not reach ACTIVE within %s; the group exists and this create must not be repeated", ErrNotSettled, op, groupID, pollBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: security group %s: %w", ErrNotSettled, op, groupID, err)
	}
	return group, err
}
