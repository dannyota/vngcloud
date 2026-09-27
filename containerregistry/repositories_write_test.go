package containerregistry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// withInstantSleep replaces c's sleep and now with fakes that never really
// wait, so a test exercising a wait's full bound runs in milliseconds
// rather than the real repoPollBound. The fake clock advances by exactly
// the duration each sleep call is asked to wait, so the wait's bound is
// still reached after the same number of iterations a real clock would
// take. It still reports ctx's own error from sleep, so a canceled-context
// test still behaves correctly.
func withInstantSleep(c *Client) *Client {
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		clock = clock.Add(d)
		return ctx.Err()
	}
	return c
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(data) == 0 {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode body: %v, raw = %s", err, data)
	}
	return body
}

// repoBody builds a bare RepositoryDto JSON object for "repo-1", with
// imageCount as given: GetRepository, CreateRepository, and
// DeleteRepository all decode this same shape directly, with no envelope
// and no status field, matching the live API.
func repoBody(imageCount int) string {
	b, err := json.Marshal(map[string]any{
		"uuid":         "repo-1",
		"name":         "app-test",
		"accessLevel":  "PRIVATE",
		"quotaLimit":   1,
		"imageCount":   imageCount,
		"attachedUser": 0,
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// --- GetRepository ---

func TestGetRepositoryDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/repository/repo-1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/containerregistry/get_repository.json")
	}))

	out, err := c.GetRepository(context.Background(), &GetRepositoryInput{RepositoryID: "repo-1"})
	if err != nil {
		t.Fatalf("GetRepository() error = %v", err)
	}
	repo := out.Repository
	if repo.ID != "repo-1" || repo.Name != "app-test" || repo.AccessLevel != "PRIVATE" ||
		repo.QuotaLimitGB != 1 || repo.ImageCount != 0 {
		t.Fatalf("unexpected repository: %+v", repo)
	}
}

func TestGetRepositoryRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.GetRepository(context.Background(), &GetRepositoryInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestGetRepositoryPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "a/b", "a?b", ""} {
		if _, err := c.GetRepository(context.Background(), &GetRepositoryInput{RepositoryID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("RepositoryID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestGetRepositoryBare404IsNotFoundWithNoListCall(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/repository/repo-x" {
			t.Fatalf("unexpected request: %s %s: a plain 404 needs no list confirm", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := c.GetRepository(context.Background(), &GetRepositoryInput{RepositoryID: "repo-x"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
}

func TestGetRepository500AbsentFromListReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/repository/repo-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/repository":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.GetRepository(context.Background(), &GetRepositoryInput{RepositoryID: "repo-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
}

func TestGetRepository500ListedReturnsOriginal500(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/repository/repo-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/repository":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"uuid":"repo-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.GetRepository(context.Background(), &GetRepositoryInput{RepositoryID: "repo-1"})
	if err == nil {
		t.Fatal("err = nil, want the original 500")
	}
	if errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want the original 500, not NotFound: the repository is still listed", err)
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want a 500 *core.APIError", err)
	}
}

func TestGetRepository500ListFailureReturnsOriginal500(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/repository/repo-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/repository":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"list also failed"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.GetRepository(context.Background(), &GetRepositoryInput{RepositoryID: "repo-1"})
	if errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want the original 500, not NotFound: the list call itself failed", err)
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want the original 500 *core.APIError", err)
	}
}

func TestGetRepository500ListIncompleteReturnsOriginal500(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/repository/repo-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/repository":
			// repo-1 is absent from this page, but the list has more items
			// than this one page returned, so absence here is inconclusive.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"uuid":"repo-2"}],"page":1,"pageSize":1,"totalPage":2,"totalItem":2}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.GetRepository(context.Background(), &GetRepositoryInput{RepositoryID: "repo-1"})
	if errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want the original 500, not NotFound: the list has more than one page", err)
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want the original 500 *core.APIError", err)
	}
}

// --- CreateRepository ---

func TestCreateRepositoryRequestBody(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/repository" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body := decodeBody(t, r)
		if body["repoName"] != "app-test" || body["quotaLimit"] != float64(3) || body["isPublic"] != false {
			t.Fatalf("body = %+v, want repoName=app-test quotaLimit=3 isPublic=false", body)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(repoBody(0)))
	}))

	out, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{Name: "app-test", QuotaLimitGB: 3, NoWait: true})
	if err != nil {
		t.Fatalf("CreateRepository() error = %v", err)
	}
	if out.Repository.ID != "repo-1" {
		t.Fatalf("ID = %q, want repo-1", out.Repository.ID)
	}
}

func TestCreateRepositoryDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		testutil.WriteFixture(t, w, "../testdata/containerregistry/create_repository.json")
	}))

	out, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{Name: "app-test", QuotaLimitGB: 1, NoWait: true})
	if err != nil {
		t.Fatalf("CreateRepository() error = %v", err)
	}
	if out.Repository.ID != "repo-1" || out.Repository.Name != "app-test" {
		t.Fatalf("unexpected repository: %+v", out.Repository)
	}
}

func TestCreateRepositoryRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{QuotaLimitGB: 1}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput (missing Name)", err)
	}
	if _, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{Name: "app"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput (missing QuotaLimitGB)", err)
	}
}

func TestCreateRepositoryNameRule(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	rejected := []string{
		"abcd",                  // 4 chars: too short
		"abcde",                 // 5 chars: too short
		strings.Repeat("a", 21), // 21 chars: too long
		"ABCdef",                // uppercase not allowed
		"-abcdef",               // leading '-' not allowed
		"_abcdef",               // leading '_' not allowed
		"abc def",               // space not allowed
		"abc.def",               // '.' not allowed
	}
	for _, name := range rejected {
		if _, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{Name: name, QuotaLimitGB: 1}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("Name %q: err = %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestCreateRepositoryNameRuleAllowedEdgeCases(t *testing.T) {
	allowed := []string{
		"abcdef",                // 6 chars: the shortest allowed
		strings.Repeat("a", 20), // 20 chars: the longest allowed
		"0abcde",                // starts with a digit
		"abc_de",                // contains '_'
		"abc-de",                // contains '-', not leading
	}
	for _, name := range allowed {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(repoBody(0)))
		}))
		if _, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{Name: name, QuotaLimitGB: 1, NoWait: true}); err != nil {
			t.Errorf("Name %q: err = %v, want nil", name, err)
		}
	}
}

func TestCreateRepositoryQuotaLimitBelowOne(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{Name: "app-test", QuotaLimitGB: -1}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreateRepositoryNoRetryAfter502(t *testing.T) {
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"upstream error"}`))
	}))

	_, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{Name: "app-test", QuotaLimitGB: 1, NoWait: true})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a create must never be retried after a 5xx", calls.Load())
	}
	if got := err.Error(); !strings.Contains(got, "list-repositories") {
		t.Fatalf("err = %v, want a hint to list-repositories before creating again", err)
	}
}

func TestCreateRepositoryConflictReturnsAPIError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"repository already exists"}`))
	}))

	_, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{Name: "app-test", QuotaLimitGB: 1, NoWait: true})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 409 {
		t.Fatalf("err = %v, want a 409 *core.APIError", err)
	}
	if errors.Is(err, ErrNotSettled) {
		t.Fatal("err wraps ErrNotSettled, want the raw APIError since the server never acted")
	}
}

func TestCreateRepositoryNoIDFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"name":"app"}`))
	}))

	_, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{Name: "app-test", QuotaLimitGB: 1, NoWait: true})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "list-repositories") {
		t.Fatalf("err = %v, want an APIError naming list-repositories before creating again", err)
	}
}

func TestCreateRepositoryConfirmSucceedsAtOnce(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(repoBody(0)))
			return
		}
		getCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(repoBody(0)))
	})))

	out, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{Name: "app-test", QuotaLimitGB: 1})
	if err != nil {
		t.Fatalf("CreateRepository() error = %v", err)
	}
	if out.Repository.ID != "repo-1" {
		t.Fatalf("ID = %q, want repo-1", out.Repository.ID)
	}
	if getCalls.Load() != 1 {
		t.Fatalf("GET calls = %d, want 1: the confirm read succeeds at once when the repository is already visible", getCalls.Load())
	}
}

func TestCreateRepositoryConfirmTolerates404(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(repoBody(0)))
			return
		}
		if getCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(repoBody(0)))
	})))

	out, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{Name: "app-test", QuotaLimitGB: 1})
	if err != nil {
		t.Fatalf("CreateRepository() error = %v", err)
	}
	if out.Repository.ID != "repo-1" {
		t.Fatalf("ID = %q, want repo-1", out.Repository.ID)
	}
	if getCalls.Load() < 2 {
		t.Fatalf("GET calls = %d, want at least 2: a 404 during the confirm must keep polling", getCalls.Load())
	}
}

func TestCreateRepositoryConfirmBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(repoBody(0)))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	})))

	_, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{Name: "app-test", QuotaLimitGB: 1})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

func TestCreateRepositoryNoWaitSkipsConfirm(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(repoBody(0)))
			return
		}
		getCalls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(repoBody(0)))
	})))

	out, err := c.CreateRepository(context.Background(), &CreateRepositoryInput{Name: "app-test", QuotaLimitGB: 1, NoWait: true})
	if err != nil {
		t.Fatalf("CreateRepository() error = %v", err)
	}
	if out.Repository.ID != "repo-1" {
		t.Fatalf("ID = %q, want repo-1: NoWait must return the create response unconfirmed", out.Repository.ID)
	}
	if getCalls.Load() != 0 {
		t.Fatalf("GET calls = %d, want 0: NoWait must skip the post-create confirm", getCalls.Load())
	}
}

func TestCreateRepositoryConfirmCanceledContext(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(repoBody(0)))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	})))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.CreateRepository(ctx, &CreateRepositoryInput{Name: "app-test", QuotaLimitGB: 1})
	if err == nil {
		t.Fatal("err = nil, want an error from the canceled confirm")
	}
}

// TestWaitRepositoryConfirmedPollParameters checks the literal interval and
// bound waitRepositoryConfirmed passes to poll, so that swapping the
// 2-second interval or the 60-second bound with another value fails this
// test: the handler never confirms the repository, so the wait always runs
// to its bound.
func TestWaitRepositoryConfirmedPollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if _, err := c.waitRepositoryConfirmed(context.Background(), "op", "repo-1"); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 30 {
		t.Fatalf("sleep calls = %d, want 30 (a 2s interval over a 60s bound)", len(sleeps))
	}
	for _, d := range sleeps {
		if d != 2*time.Second {
			t.Fatalf("sleep duration = %s, want 2s", d)
		}
	}
}

// --- DeleteRepository ---

func TestDeleteRepositoryGuardImageCount(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(repoBody(3)))
	}))

	_, err := c.DeleteRepository(context.Background(), &DeleteRepositoryInput{RepositoryID: "repo-1"})
	if !errors.Is(err, ErrRepositoryNotEmpty) {
		t.Fatalf("err = %v, want ErrRepositoryNotEmpty", err)
	}
}

func TestDeleteRepositoryAttachedUsersDoNotBlock(t *testing.T) {
	var deleteCalled atomic.Bool
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			b, _ := json.Marshal(map[string]any{"uuid": "repo-1", "imageCount": 0, "attachedUser": 5})
			_, _ = w.Write(b)
		case http.MethodDelete:
			deleteCalled.Store(true)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(repoBody(0)))
		}
	}))

	if _, err := c.DeleteRepository(context.Background(), &DeleteRepositoryInput{RepositoryID: "repo-1", NoWait: true}); err != nil {
		t.Fatalf("DeleteRepository() error = %v", err)
	}
	if !deleteCalled.Load() {
		t.Fatal("DELETE was never sent: attached users must not block a delete")
	}
}

func TestDeleteRepositoryPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, id := range []string{"..", ".", "a/b", "a?b", ""} {
		if _, err := c.DeleteRepository(context.Background(), &DeleteRepositoryInput{RepositoryID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("RepositoryID %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
}

func TestDeleteRepositoryUnknownReturnsNotFoundWithNoDelete(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			t.Fatal("no DELETE expected: the guard read already shows the repository gone")
		case r.URL.Path == "/v1/repository/repo-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.URL.Path == "/v1/repository":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteRepository(context.Background(), &DeleteRepositoryInput{RepositoryID: "repo-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
}

func TestDeleteRepositoryDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(repoBody(0)))
		case http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
			testutil.WriteFixture(t, w, "../testdata/containerregistry/delete_repository.json")
		}
	}))

	if _, err := c.DeleteRepository(context.Background(), &DeleteRepositoryInput{RepositoryID: "repo-1", NoWait: true}); err != nil {
		t.Fatalf("DeleteRepository() error = %v", err)
	}
}

// TestDeleteRepositoryWaitSettlesAbsent covers the wait's two-source
// confirm: the guard read (call 1) and the first poll (call 2) find the
// repository still present, and the second poll (call 3) gets a plain 404,
// which GetRepository maps to core.ErrNotFound with no list call.
func TestDeleteRepositoryWaitSettlesAbsent(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(repoBody(0)))
		case r.URL.Path == "/v1/repository/repo-1":
			getCalls++
			if getCalls <= 2 {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(repoBody(0)))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	})))

	if _, err := c.DeleteRepository(context.Background(), &DeleteRepositoryInput{RepositoryID: "repo-1"}); err != nil {
		t.Fatalf("DeleteRepository() error = %v", err)
	}
	if getCalls < 3 {
		t.Fatalf("GET calls = %d, want at least 3: the guard read, then polls until the repository 404s", getCalls)
	}
}

func TestDeleteRepositoryWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(repoBody(0)))
		case r.URL.Path == "/v1/repository/repo-1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(repoBody(0)))
		}
	})))

	_, err := c.DeleteRepository(context.Background(), &DeleteRepositoryInput{RepositoryID: "repo-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

func TestDeleteRepositoryNoWaitSkipsWait(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(repoBody(0)))
		case r.URL.Path == "/v1/repository/repo-1":
			getCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(repoBody(0)))
		}
	})))

	if _, err := c.DeleteRepository(context.Background(), &DeleteRepositoryInput{RepositoryID: "repo-1", NoWait: true}); err != nil {
		t.Fatalf("DeleteRepository() error = %v", err)
	}
	if getCalls != 1 {
		t.Fatalf("GET calls = %d, want 1: NoWait must skip the post-delete wait, leaving only the guard read", getCalls)
	}
}

// TestWaitRepositoryAbsentPollParameters checks the literal interval and
// bound waitRepositoryAbsent passes to poll, so that swapping the 2-second
// interval or the 60-second bound with another value fails this test: the
// handler always reports the repository present, so the wait always runs to
// its bound.
func TestWaitRepositoryAbsentPollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(repoBody(0)))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if err := c.waitRepositoryAbsent(context.Background(), "op", "repo-1"); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 30 {
		t.Fatalf("sleep calls = %d, want 30 (a 2s interval over a 60s bound)", len(sleeps))
	}
	for _, d := range sleeps {
		if d != 2*time.Second {
			t.Fatalf("sleep duration = %s, want 2s", d)
		}
	}
}
