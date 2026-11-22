package cli

import (
	"context"
	"errors"
	"testing"
)

func TestRenameRetriesSharingFailure(t *testing.T) {
	sharing := errors.New("sharing violation")
	attempts := 0
	err := renameWithRetry(context.Background(), "old", "new", func(_, _ string) error {
		attempts++
		if attempts == 1 {
			return sharing
		}
		return nil
	}, func(err error) bool { return errors.Is(err, sharing) })
	if err != nil || attempts != 2 {
		t.Fatalf("error %v, attempts %d", err, attempts)
	}
}

func TestRenameRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sharing := errors.New("sharing violation")
	err := renameWithRetry(ctx, "old", "new", func(_, _ string) error { cancel(); return sharing }, func(err error) bool { return errors.Is(err, sharing) })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
