package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/discovery"
)

func TestSharedWaitersCancelBeforeHolderFinishes(t *testing.T) {
	for _, mode := range []string{"project", "discovery-project", "iam-token", "iam-cache", "iam-invalidate", "service-account"} {
		t.Run(mode, func(t *testing.T) {
			c := &Client{}
			auth := &IAMUserAuth{}
			source := newServiceAccountTokenSource(nil, time.Second, nil, nil, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			var unlock func()
			switch mode {
			case "project", "discovery-project":
				c.projectMu.Lock()
				unlock = c.projectMu.Unlock
			case "service-account":
				source.mu.Lock()
				unlock = source.mu.Unlock
			default:
				auth.mu.Lock()
				unlock = auth.mu.Unlock
			}
			go func() {
				var err error
				switch mode {
				case "project":
					_, err = c.RequireProjectID(ctx)
				case "discovery-project":
					s, _ := discovery.New(ctx, 1)
					defer s.Close()
					_, err = c.RequireDiscoveryProjectID(s)
				case "iam-token":
					_, _, err = auth.token(ctx, loginEndpoints{}, nil)
				case "iam-cache":
					_, _, _, err = auth.cachedIfFresh(ctx)
				case "iam-invalidate":
					err = auth.invalidateContext(ctx, "never-sent")
				case "service-account":
					_, err = source.Token(ctx)
				}
				done <- err
			}()
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Error("waiter lost cancellation")
				}
			case <-time.After(time.Second):
				t.Error("waiter remained blocked by holder")
			}
			unlock()
		})
	}
}
