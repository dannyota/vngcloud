package monitor

import "context"

type checkGate struct {
	token chan struct{}
	users int
}

func (c *Client) acquireCheck(ctx context.Context, id string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.checkMu.Lock()
	if c.checkGates == nil {
		c.checkGates = make(map[string]*checkGate)
	}
	gate := c.checkGates[id]
	if gate == nil {
		gate = &checkGate{token: make(chan struct{}, 1)}
		c.checkGates[id] = gate
	}
	gate.users++
	c.checkMu.Unlock()

	drop := func() {
		c.checkMu.Lock()
		defer c.checkMu.Unlock()
		gate.users--
		if gate.users == 0 {
			delete(c.checkGates, id)
		}
	}
	select {
	case gate.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate.token
			drop()
			return nil, err
		}
		return func() { <-gate.token; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}
