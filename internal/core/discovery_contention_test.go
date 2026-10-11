package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/tokencache"
)

func TestCanceledTokenAcquisitionDoesNotCommitDiskCache(t *testing.T) {
	signinURL, tokenURL, logins := fakeIAMServers(t)
	auth := fakeIAMUser(signinURL, tokenURL)
	dir := filepath.Join(t.TempDir(), "cache")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key := tokencache.Key{
		RootEmail: auth.RootEmail, Username: auth.Username,
		SigninURL: signinURL, TokenURL: tokenURL,
	}
	source := &iamTokenSource{
		auth: auth,
		endpoints: loginEndpoints{
			signin: signinURL, token: tokenURL, dashboard: tokenURL + "/",
		},
		cacheKey: key,
		// An empty cache calls the clock after login, before committing the
		// acquired token. Cancel at that boundary without timing a race.
		cache: tokencache.New(dir, func() time.Time {
			cancel()
			return time.Now()
		}),
	}
	_, err := source.Token(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Token error = %v, want context.Canceled", err)
	}
	if logins.Load() != 1 {
		t.Errorf("logins = %d, want 1", logins.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, key.Hash()+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("disk cache stat error = %v, want os.ErrNotExist", err)
	}
	if auth.cachedToken != "" {
		t.Error("canceled acquisition committed the shared IAM token")
	}
}

func TestCanceledProjectWaiterDoesNotUseCache(t *testing.T) {
	c := &Client{projectID: "p-1"}
	c.projectMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.RequireProjectID(ctx); done <- err }()
	cancel()
	c.projectMu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if c.projectID != "p-1" {
		t.Fatal("changed holder's project")
	}
}

func TestCanceledSharedIAMWaiterDoesNotUseToken(t *testing.T) {
	a := &IAMUserAuth{cachedToken: "holder-token", expiresAt: time.Now().Add(time.Hour)}
	a.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, err := a.token(ctx, loginEndpoints{}, nil); done <- err }()
	cancel()
	a.mu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if a.cachedToken != "holder-token" {
		t.Fatal("changed holder's token")
	}
}

func TestCanceledSharedIAMInvalidationPreservesToken(t *testing.T) {
	auth := &IAMUserAuth{cachedToken: "sent-token", expiresAt: time.Now().Add(time.Hour), obtainedAt: time.Now().Add(-time.Hour)}
	source := &iamTokenSource{auth: auth}
	auth.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- source.InvalidateContext(ctx, "sent-token") }()
	cancel()
	auth.mu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("invalidation error=%v", err)
	}
	if auth.cachedToken != "sent-token" || source.rejected != "" {
		t.Fatal("canceled invalidation changed shared token")
	}
}

func TestDiscoveryProjectWaiterDeadline(t *testing.T) {
	c := &Client{projectID: "holder-project"}
	c.projectMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	s, err := discovery.New(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	done := make(chan error, 1)
	go func() { _, err := c.RequireDiscoveryProjectID(s); done <- err }()
	// The parent closes Done before propagating cancellation to the session.
	<-s.Context().Done()
	c.projectMu.Unlock()
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if c.projectID != "holder-project" || s.Requests() != 0 {
		t.Fatal("expired waiter changed project state or scheduled work")
	}
}

func TestSharedIAMWaiterDeadline(t *testing.T) {
	auth := &IAMUserAuth{cachedToken: "holder-token", expiresAt: time.Now().Add(time.Hour)}
	source := &iamTokenSource{auth: auth}
	auth.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := source.Token(ctx); done <- err }()
	<-ctx.Done()
	auth.mu.Unlock()
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter=%v", err)
	}
	if auth.cachedToken != "holder-token" {
		t.Fatal("expired waiter changed shared IAM token")
	}
}
