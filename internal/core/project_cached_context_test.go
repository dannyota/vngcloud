package core

import (
	"context"
	"errors"
	"testing"
)

func TestProjectIDContextCancelsWhileDiscoveryHeld(t *testing.T) {
	c := &Client{}
	c.projectMu.Lock()
	defer c.projectMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.ProjectIDContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want canceled", err)
	}
}
