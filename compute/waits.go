package compute

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
)

// sleepFunc waits for d or ctx's end, whichever comes first, returning
// ctx.Err() when ctx ends first. Tests inject a fake one so the waits below
// never really elapse.
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

// poll runs step at once, then again every serverPollInterval, until step
// reports stop true or bound has elapsed, by now, since poll's first call
// to step. Every compute wait uses the same 5-second interval, so it is not
// a parameter here, unlike vDNS's, network's, and volume's own unexported
// poll, whose shape this otherwise copies; only bound varies per operation.
func poll(ctx context.Context, now clockFunc, sleep sleepFunc, bound time.Duration, step func(ctx context.Context) (stop bool, err error), onTimeout func() error) error {
	deadline := now().Add(bound)
	for {
		stop, err := step(ctx)
		if stop {
			return err
		}
		if !now().Before(deadline) {
			return onTimeout()
		}
		if err := sleep(ctx, serverPollInterval); err != nil {
			return err
		}
	}
}

// Server statuses the waits below observe, from the design's status table.
const (
	serverStatusActive  = "ACTIVE"
	serverStatusStopped = "STOPPED"
	serverStatusError   = "ERROR"
	serverStatusDeleted = "DELETED"
)

// isServerError reports whether status is the server ERROR status.
func isServerError(status string) bool {
	return strings.EqualFold(status, serverStatusError)
}

// isServerDeleted reports whether status is the server DELETED status: a
// delete wait settles on either this or a 404 from GetServer.
func isServerDeleted(status string) bool {
	return strings.EqualFold(status, serverStatusDeleted)
}

// Poll timing per the design's wait table.
const (
	serverPollInterval   = 5 * time.Second
	serverCreateBound    = 15 * time.Minute
	serverStartStopBound = 5 * time.Minute
	serverRebootBound    = 5 * time.Minute
	serverDeleteBound    = 10 * time.Minute
	serverResizeBound    = 15 * time.Minute
	serverRebootMinWait  = 10 * time.Second
)

// waitServerActive is CreateServer's post-create wait unless NoWait is set:
// it reads serverID with GetServer until its Status reaches ACTIVE or
// ERROR; any other status, including a 404 (a server just created may not
// be readable at once), keeps it polling.
func (c *Client) waitServerActive(ctx context.Context, op, serverID string) (*Server, error) {
	var server *Server
	err := poll(ctx, c.now, c.sleep, serverCreateBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetServer(ctx, &GetServerInput{ServerID: serverID})
			if err != nil {
				if core.IsNotFound(err) {
					return false, nil
				}
				return true, err
			}
			server = &out.Server
			switch {
			case strings.EqualFold(server.Status, serverStatusActive):
				return true, nil
			case isServerError(server.Status):
				return true, fmt.Errorf("%w: %s: server %s is ERROR", ErrFailed, op, serverID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: server %s did not reach ACTIVE within %s; the server exists and this create must not be repeated",
				ErrNotSettled, op, serverID, serverCreateBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: server %s: %w", ErrNotSettled, op, serverID, err)
	}
	return server, err
}

// waitServerStatus is StartServer and StopServer's post-toggle wait: it
// reads serverID with GetServer until its Status reaches target or ERROR;
// any other status keeps it polling.
func (c *Client) waitServerStatus(ctx context.Context, op, serverID, target string) (*Server, error) {
	var server *Server
	err := poll(ctx, c.now, c.sleep, serverStartStopBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetServer(ctx, &GetServerInput{ServerID: serverID})
			if err != nil {
				return true, err
			}
			server = &out.Server
			switch {
			case strings.EqualFold(server.Status, target):
				return true, nil
			case isServerError(server.Status):
				return true, fmt.Errorf("%w: %s: server %s is ERROR", ErrFailed, op, serverID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: server %s did not reach %s within %s; the toggle was sent, run this operation again to check",
				ErrNotSettled, op, serverID, target, serverStartStopBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: server %s: %w", ErrNotSettled, op, serverID, err)
	}
	return server, err
}

// waitServerRebooted is RebootServer's post-reboot wait: it reads serverID
// with GetServer until it reads ACTIVE at least serverRebootMinWait after
// sentAt (the reboot PUT's own send time), so a read that still shows the
// pre-reboot ACTIVE state before REBOOTING even appears is not mistaken for
// settled. ERROR fails the wait at once, at any time.
func (c *Client) waitServerRebooted(ctx context.Context, op, serverID string, sentAt time.Time) (*Server, error) {
	var server *Server
	err := poll(ctx, c.now, c.sleep, serverRebootBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetServer(ctx, &GetServerInput{ServerID: serverID})
			if err != nil {
				return true, err
			}
			server = &out.Server
			if isServerError(server.Status) {
				return true, fmt.Errorf("%w: %s: server %s is ERROR", ErrFailed, op, serverID)
			}
			if strings.EqualFold(server.Status, serverStatusActive) && c.now().Sub(sentAt) >= serverRebootMinWait {
				return true, nil
			}
			return false, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: server %s did not confirm reboot within %s; run this operation again to check",
				ErrNotSettled, op, serverID, serverRebootBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: server %s: %w", ErrNotSettled, op, serverID, err)
	}
	return server, err
}

// waitServerDeleted is DeleteServer's post-delete wait unless NoWait is
// set: it reads serverID with GetServer until that read reports NotFound or
// Status DELETED (settled), or ERROR (failed); any other status keeps it
// polling.
func (c *Client) waitServerDeleted(ctx context.Context, op, serverID string) error {
	err := poll(ctx, c.now, c.sleep, serverDeleteBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetServer(ctx, &GetServerInput{ServerID: serverID})
			if err != nil {
				if core.IsNotFound(err) {
					return true, nil
				}
				return true, err
			}
			switch {
			case isServerDeleted(out.Server.Status):
				return true, nil
			case isServerError(out.Server.Status):
				return true, fmt.Errorf("%w: %s: server %s is ERROR", ErrFailed, op, serverID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: server %s did not reach 404 or DELETED within %s; delete was sent and a rerun is safe",
				ErrNotSettled, op, serverID, serverDeleteBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: server %s: %w", ErrNotSettled, op, serverID, err)
	}
	return err
}
