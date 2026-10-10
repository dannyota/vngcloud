//go:build livewrite

package livetest_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

func TestStorageAutoRenewCleanupAccountingFailures(t *testing.T) {
	for _, cashFails := range []bool{false, true} {
		calls := 0
		result := &storage.PutProjectAutoRenewOutput{State: &storage.ProjectAutoRenew{Enabled: vngcloud.Ptr(false)}}
		_, err := autoRenewToggle(true, map[string]any{}, func() (float64, error) {
			if cashFails {
				return 0, errors.New("cash unavailable")
			}
			return 100, nil
		}, func() bool { return false }, func() (*storage.PutProjectAutoRenewOutput, error) { calls++; return result, nil })
		if calls != 1 || err == nil {
			t.Fatal("accounting failure blocked cleanup or was hidden")
		}
	}
}

func TestStorageAutoRenewCapturePrivacy(t *testing.T) {
	for _, path := range []string{"/gateway/api/v1/home/user-info", "/gateway/api/v1/resources/autoRenew"} {
		c := &autoRenewLiveTransport{dir: t.TempDir(), projectID: "project-1", approved: true, base: autoRenewLiveHTTP(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":200,"data":{"accountId":12345},"message":"synthetic-private-token 12345"}`))}, nil
		})}
		method := "GET"
		body := ""
		if strings.HasSuffix(path, "autoRenew") {
			method = "PUT"
			body = `[{"product":"vstorage","artifactType":"object-storage","artifactId":"project-1","autoRenewInfo":{"isEnable":false}}]`
		}
		req, err := http.NewRequestWithContext(context.Background(), method, "https://synthetic.invalid"+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Body.Close() != nil {
			t.Fatal("close failed")
		}
		if !strings.Contains(string(raw), "synthetic-private-token") {
			t.Fatal("SDK response was altered")
		}
		files, err := os.ReadDir(c.dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			data, err := os.ReadFile(filepath.Join(c.dir, file.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "12345") || strings.Contains(string(data), "synthetic-private-token") || strings.Contains(string(data), "accountId") {
				t.Fatal("capture contains private identity or credential")
			}
		}
	}
}

func TestStorageAutoRenewCaptureFailureDoesNotBlockCleanup(t *testing.T) {
	c := &autoRenewLiveTransport{dir: filepath.Join(t.TempDir(), "absent"), base: autoRenewLiveHTTP(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"success":true,"datas":[]}`))}, nil
	})}
	req, err := http.NewRequestWithContext(context.Background(), "GET", "https://synthetic.invalid/internal/v1/projects", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.RoundTrip(req)
	if err != nil {
		t.Fatal("capture failure blocked state read")
	}
	if resp.Body.Close() != nil {
		t.Fatal("close failed")
	}
	if c.captureFailure() == nil {
		t.Fatal("capture failure hidden")
	}
}

func TestStorageAutoRenewDisableSentTracking(t *testing.T) {
	c := &autoRenewLiveTransport{dir: t.TempDir(), projectID: "project-1", approved: true, base: autoRenewLiveHTTP(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":200,"data":{"successAll":true,"errorAutoRenewResources":[]}}`))}, nil
	})}
	if c.disableSent() {
		t.Fatal("disable marked before PUT")
	}
	req, err := http.NewRequestWithContext(context.Background(), "GET", "https://synthetic.invalid/gateway/api/v1/home/user-info", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body.Close() != nil {
		t.Fatal("close failed")
	}
	if c.disableSent() {
		t.Fatal("read counted as disable")
	}
	req, err = http.NewRequestWithContext(context.Background(), "PUT", "https://synthetic.invalid/gateway/api/v1/resources/autoRenew", strings.NewReader(`[{"product":"vstorage","artifactType":"object-storage","artifactId":"project-1","autoRenewInfo":{"isEnable":false}}]`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err = c.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body.Close() != nil {
		t.Fatal("close failed")
	}
	if !c.disableSent() {
		t.Fatal("sent disable not tracked")
	}
}

func TestStorageAutoRenewPreSendFailureAllowsCleanup(t *testing.T) {
	calls := 0
	c := &autoRenewLiveTransport{dir: t.TempDir(), projectID: "project-1", approved: true, base: autoRenewLiveHTTP(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method == "GET" {
			return nil, errors.New("pre-send read failed")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":200,"data":{"successAll":true,"errorAutoRenewResources":[]}}`))}, nil
	})}
	req, err := http.NewRequestWithContext(context.Background(), "GET", "https://synthetic.invalid/gateway/api/v1/home/user-info", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.RoundTrip(req); err == nil {
		t.Fatal("mock read succeeded")
	}
	err = autoRenewCleanup(&storage.ProjectAutoRenew{Enabled: vngcloud.Ptr(true)}, c.disableSent(), func() error {
		request, requestErr := http.NewRequestWithContext(context.Background(), "PUT", "https://synthetic.invalid/gateway/api/v1/resources/autoRenew", strings.NewReader(`[{"product":"vstorage","artifactType":"object-storage","artifactId":"project-1","autoRenewInfo":{"isEnable":false}}]`))
		if requestErr != nil {
			return requestErr
		}
		resp, writeErr := c.RoundTrip(request)
		if writeErr != nil {
			return writeErr
		}
		return resp.Body.Close()
	})
	if err != nil || calls != 2 || !c.disableSent() {
		t.Fatal("pre-send failure blocked first cleanup disable")
	}
	if autoRenewCleanup(&storage.ProjectAutoRenew{Enabled: vngcloud.Ptr(true)}, c.disableSent(), func() error { t.Error("disable repeated"); return nil }) == nil {
		t.Fatal("sent disable retried")
	}
}
