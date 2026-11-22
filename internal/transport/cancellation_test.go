package transport

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRefreshWaitersCancelBeforeHolderFinishes(t *testing.T) {
	for _, mode := range []string{"initial", "unauthorized", "once"} {
		t.Run(mode, func(t *testing.T) {
			source := &heldDiscoverySource{entered: make(chan struct{}), release: make(chan struct{})}
			c := New(Config{TokenSource: source})
			if mode != "initial" {
				c.token = &Token{AccessToken: "sent-token", ExpiresAt: time.Now().Add(time.Hour)}
			}
			holder := make(chan error, 1)
			go func() {
				if mode == "initial" {
					holder <- c.refreshToken(context.Background())
				} else {
					holder <- c.invalidateAndRefresh(context.Background(), "sent-token")
				}
			}()
			<-source.entered
			ctx, cancel := context.WithCancel(context.Background())
			waiter := make(chan error, 1)
			go func() {
				switch mode {
				case "initial":
					waiter <- c.refreshToken(ctx)
				case "unauthorized":
					waiter <- c.invalidateAndRefresh(ctx, "sent-token")
				case "once":
					waiter <- c.invalidateOnceContext(ctx, "sent-token")
				}
			}()
			cancel()
			select {
			case err := <-waiter:
				if !errors.Is(err, context.Canceled) {
					t.Error("waiter lost cancellation")
				}
			case <-time.After(time.Second):
				t.Error("waiter remained blocked by holder")
			}
			close(source.release)
			if err := <-holder; err != nil {
				t.Fatal(err)
			}
			if source.calls.Load() != 1 || c.currentToken().AccessToken != "replacement-token" {
				t.Fatal("waiter changed holder state")
			}
		})
	}
}
