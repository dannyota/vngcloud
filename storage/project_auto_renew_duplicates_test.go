package storage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func TestProjectAutoRenewDuplicateKeysBlockPUT(t *testing.T) {
	resource := strings.ReplaceAll(testutil.FixtureBody(t, "../testdata/billing/ListResources.json"), "project-1", "new-1")
	for _, tc := range []struct{ name, path, body string }{
		{"renewal", "/gateway/api/v1/resources", strings.Replace(resource, `"isRenewing": false`, `"isRenewing":true,"isRenewing":null`, 1)},
		{"user", "/gateway/api/v1/home/user-info", `{"code":200,"data":{"accountId":12345,"userId":54321,"userId":12345}}`},
		{"account", "/gateway/api/v1/home/user-info", `{"code":200,"data":{"accountId":54321,"accountId":12345,"userId":12345}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &autoRenewServer{overrides: map[string]string{tc.path: tc.body}}
			_, err := autoRenewClient(t, s).PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
			var api *core.APIError
			if !errors.As(err, &api) || api.Code != "InvalidResponse" || s.puts != 0 {
				t.Fatalf("error %v PUTs %d", err, s.puts)
			}
		})
	}
}

func TestProjectAutoRenewDuplicatePUTReply(t *testing.T) {
	for _, body := range []string{
		`{"code":500,"code":200,"data":{"successAll":true,"errorAutoRenewResources":[]}}`,
		`{"code":200,"data":null,"data":{"successAll":true,"errorAutoRenewResources":[]}}`,
		`{"code":200,"data":{"successAll":false,"successAll":true,"errorAutoRenewResources":[]}}`,
		`{"code":200,"data":{"successAll":true,"errorAutoRenewResources":[{}],"errorAutoRenewResources":[]}}`,
		`{"code":200,"data":{"successAll":true,"errorAutoRenewResources":[],"extra":{"a":1,"a":2}}}`,
	} {
		s := &autoRenewServer{overrides: map[string]string{"/gateway/api/v1/resources/autoRenew": body}}
		out, err := autoRenewClient(t, s).PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
		var api *core.APIError
		if !errors.Is(err, ErrNotSettled) || !errors.As(err, &api) || api.Code != "InvalidResponse" || s.puts != 1 || out == nil || out.Changed {
			t.Fatalf("error %v PUTs %d output %+v", err, s.puts, out)
		}
	}
}

func TestProjectAutoRenewDuplicateProjectKeys(t *testing.T) {
	row := newProjectJSON
	for _, body := range []string{
		projectList(strings.Replace(row, `"enableAutoRenew":false`, `"enableAutoRenew":true,"enableAutoRenew":false`, 1)),
		projectList(strings.TrimSuffix(row, "}") + `,"extra":{"a":1,"a":2}}`),
		strings.Replace(projectList(row), `"success":true`, `"success":false,"success":true`, 1),
	} {
		s := &autoRenewServer{overrides: map[string]string{"/internal/v1/projects": body}}
		_, err := autoRenewClient(t, s).GetProjectAutoRenew(context.Background(), &GetProjectAutoRenewInput{ProjectID: "new-1"})
		var api *core.APIError
		if !errors.As(err, &api) || api.Message != "project list is malformed" {
			t.Fatalf("duplicate accepted: %v", err)
		}
	}
}
