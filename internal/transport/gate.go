package transport

import (
	"context"
	"sync"
)

// Gate serializes shared work while allowing each waiter to cancel its wait.
// Its zero value is ready to use. Do not copy a Gate after first use.
type Gate struct {
	mu   sync.Mutex
	held chan struct{}
}

// LockContext acquires the gate unless ctx ends first.
func (g *Gate) LockContext(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		g.mu.Lock()
		if err := ctx.Err(); err != nil {
			g.mu.Unlock()
			return err
		}
		if g.held == nil {
			g.held = make(chan struct{})
			g.mu.Unlock()
			return nil
		}
		done := g.held
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
	}
}

// Lock acquires the gate without a cancellation deadline.
func (g *Gate) Lock() { _ = g.LockContext(context.Background()) }

// Unlock releases the gate and wakes its waiters.
func (g *Gate) Unlock() {
	g.mu.Lock()
	defer g.mu.Unlock()
	close(g.held)
	g.held = nil
}
