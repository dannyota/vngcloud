package containerregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func TestRegistryAbsenceNeedsExplicitList(t *testing.T) {
	for _, resource := range []string{"repository", "user"} {
		for _, body := range []string{`{"totalPage":0,"totalItem":0}`, `{"data":null,"totalPage":0,"totalItem":0}`, `{"data":[],"listData":[{"uuid":"other"}],"totalPage":0,"totalItem":0}`, `{"data":[],"totalPage":-1,"totalItem":0}`, `{"data":[],"totalPage":2,"totalItem":0}`} {
			t.Run(resource+body, func(t *testing.T) {
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/"+resource) {
						_, _ = io.WriteString(w, body)
						return
					}
					w.WriteHeader(500)
				}))
				var err error
				if resource == "repository" {
					_, err = c.GetRepository(context.Background(), &GetRepositoryInput{RepositoryID: "repo-1"})
				} else {
					_, err = c.DeleteUser(context.Background(), &DeleteUserInput{UserID: "ra-1"})
				}
				if err == nil || core.IsNotFound(err) {
					t.Fatalf("malformed evidence proved absence: %v", err)
				}
			})
		}
	}
}

func TestCreatedRegistryNameIsNotOwnership(t *testing.T) {
	var created atomic.Bool
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			created.Store(true)
			_, _ = io.WriteString(w, `{"secretKey":"issued-secret"}`)
		case strings.HasSuffix(r.URL.Path, "permissions"):
			_, _ = io.WriteString(w, `[{"uuid":"p-1","action":"Pull Images"}]`)
		case created.Load():
			_, _ = io.WriteString(w, `{"data":[{"uuid":"ra-concurrent","name":"app-ci"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`)
		default:
			_, _ = io.WriteString(w, `{"data":[],"page":1,"pageSize":10000,"totalPage":1,"totalItem":0}`)
		}
	}))
	out, err := c.CreateUser(context.Background(), &CreateUserInput{Name: "app-ci", Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"Pull Images"}}}})
	if out == nil || out.SecretKey.Reveal() != "issued-secret" || out.User.ID != "" || !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("name match became cleanup authority: output=%v error=%v", out, err)
	}
}

func TestIssuedRegistrySecretWithheldThroughLookup(t *testing.T) {
	for _, status := range []int{200, 403} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var created atomic.Bool
			var captures atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost:
					created.Store(true)
					_, _ = io.WriteString(w, `{"secretKey":"issued-secret"}`)
				case strings.HasSuffix(r.URL.Path, "permissions"):
					_, _ = io.WriteString(w, `[{"uuid":"p-1","action":"Pull Images"}]`)
				case created.Load():
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"data":[{"uuid":"ra-1","name":"app-ci","description":"issued-secret"}],"totalPage":1,"totalItem":1,"code":"issued-secret","message":"issued-secret"}`)
				default:
					_, _ = io.WriteString(w, `{"data":[],"totalPage":1,"totalItem":0}`)
				}
			}))
			defer server.Close()
			cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{VCR: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), Capture: func(transport.Capture) {
				if created.Load() {
					captures.Add(1)
				}
			}}))
			out, err := New(cfg).CreateUser(context.Background(), &CreateUserInput{Name: "app-ci", Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"Pull Images"}}}})
			raw, _ := json.Marshal(out)
			if err == nil || out == nil || out.SecretKey.Reveal() != "issued-secret" || strings.Contains(string(raw), "issued-secret") || strings.Contains(fmt.Sprintf("%+v", err), "issued-secret") || captures.Load() != 0 {
				t.Fatalf("lookup privacy failed: output=%s error=%v captures=%d", raw, err, captures.Load())
			}
		})
	}
}

func TestRegistryPollRejectsLateAndCanceled(t *testing.T) {
	now := time.Now()
	start := now
	err := poll(context.Background(), func() time.Time { return now }, contextSleep, func(ctx context.Context) (bool, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing operation deadline")
		}
		now = start.Add(repoPollBound)
		return true, nil
	}, func() error { return ErrNotSettled })
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("late success=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = poll(ctx, time.Now, contextSleep, func(context.Context) (bool, error) { t.Error("canceled caller polled"); return true, nil }, func() error { return ErrNotSettled })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled poll=%v", err)
	}
}

func TestRepositoriesReportIncompletePaging(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"uuid":"repo-1"}],"page":1,"pageSize":1,"totalPage":2,"totalItem":2}`)
	}))
	out, err := c.ListRepositories(context.Background(), nil)
	if out == nil || out.TotalPage != 2 || err == nil {
		t.Fatalf("incomplete paging: output=%v error=%v", out, err)
	}
}

func TestRegistryLookupNameCannotEchoIssuedSecret(t *testing.T) {
	var created atomic.Bool
	var followups atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			created.Store(true)
			_, _ = io.WriteString(w, `{"secretKey":"app-ci"}`)
		case strings.HasSuffix(r.URL.Path, "permissions"):
			_, _ = io.WriteString(w, `[{"uuid":"p-1","action":"Pull Images"}]`)
		case created.Load():
			followups.Add(1)
			w.WriteHeader(403)
			_, _ = io.WriteString(w, `{}`)
		default:
			_, _ = io.WriteString(w, `{"data":[],"totalPage":1,"totalItem":0}`)
		}
	}))
	out, err := c.CreateUser(context.Background(), &CreateUserInput{Name: "app-ci", Permissions: []UserPermission{{RepositoryID: "repo-1", Actions: []string{"Pull Images"}}}})
	if out == nil || out.SecretKey.Reveal() != "app-ci" || !errors.Is(err, ErrUserNotFound) || strings.Contains(fmt.Sprint(err), "app-ci") || followups.Load() != 0 {
		t.Fatalf("issued credential used as lookup identity: output=%v error=%v followups=%d", out, err, followups.Load())
	}
}

func TestRegistryConfirmRequiresMatchingRepository(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"uuid":"repo-2","imageCount":0}`)
	}))
	out, err := c.waitRepositoryConfirmed(context.Background(), "create", "repo-1")
	if out != nil || !errors.Is(err, ErrNotSettled) {
		t.Fatalf("swapped repository confirmed: output=%v error=%v", out, err)
	}
}

func TestRegistryMalformedRowsCannotProveAbsence(t *testing.T) {
	for _, body := range []string{`{"data":[{}],"totalPage":1,"totalItem":1}`, `{"data":[{"uuid":"other"},{"uuid":"other"}],"totalPage":1,"totalItem":2}`} {
		for _, resource := range []string{"repository", "user"} {
			t.Run(resource+body, func(t *testing.T) {
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/"+resource) {
						_, _ = io.WriteString(w, body)
						return
					}
					w.WriteHeader(500)
				}))
				var err error
				if resource == "repository" {
					_, err = c.GetRepository(context.Background(), &GetRepositoryInput{RepositoryID: "repo-1"})
				} else {
					_, err = c.DeleteUser(context.Background(), &DeleteUserInput{UserID: "ra-1"})
				}
				if err == nil || core.IsNotFound(err) {
					t.Fatalf("unproven rows proved absence: %v", err)
				}
			})
		}
	}
}

func TestRegistryPollingRequestStopsOnCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- poll(ctx, time.Now, contextSleep, func(ctx context.Context) (bool, error) { close(started); <-ctx.Done(); return true, nil }, func() error { return ErrNotSettled })
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled read succeeded: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled polling request ignored cancellation")
	}
}

func TestRegistryDeleteRejectsConflictingRepositoryFields(t *testing.T) {
	for _, body := range []string{
		`{"uuid":"repo-1","imageCount":2,"imageCount":0}`,
		`{"uuid":"repo-2","UUID":"repo-1","imageCount":0}`,
		`{"uuid":"repo-1","imageCount":2,"ImageCount":0}`,
	} {
		t.Run(body, func(t *testing.T) {
			var writes atomic.Int32
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, body)
					return
				}
				writes.Add(1)
				w.WriteHeader(202)
				_, _ = io.WriteString(w, `{}`)
			}))
			_, err := c.DeleteRepository(context.Background(), &DeleteRepositoryInput{RepositoryID: "repo-1", NoWait: true})
			if err == nil || writes.Load() != 0 {
				t.Fatalf("conflicting evidence authorized delete: error=%v writes=%d", err, writes.Load())
			}
		})
	}
}
