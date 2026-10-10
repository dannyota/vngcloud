package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestProjectDuplicateConfigurationKeys(t *testing.T) {
	for _, tc := range []struct{ key, first, last string }{
		{"vos_billing_normal_min_quota", "60", "30"},
		{"vos_billing_normal_max_quota", "20", "2000000"},
		{"max_project_per_user_per_region", "0", "20"},
		{"enable_iam_checkout", "false", "true"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			body := fmt.Sprintf(`{"success":true,"datas":[{"key":%q,"value":%q,"value":%q}]}`, tc.key, tc.first, tc.last)
			for _, method := range []string{"quote", "create", "auto renew"} {
				t.Run(method, func(t *testing.T) {
					s := &projectWriteServer{overrides: map[string]string{tc.key: body}, after: projectList(newProjectJSON)}
					c := newTestClient(t, s.handler(t))
					var err error
					switch method {
					case "quote":
						_, err = c.QuoteCreateProject(context.Background(), validProjectCreate())
					case "create":
						_, err = c.CreateProject(context.Background(), validProjectCreate())
					case "auto renew":
						renew := &autoRenewServer{overrides: s.overrides}
						_, err = autoRenewClient(t, renew).PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
						if renew.puts != 0 {
							t.Fatalf("PUTs = %d, want zero", renew.puts)
						}
					}
					var api *vngcloud.APIError
					if !errors.As(err, &api) || api.Message != "malformed catalog data" || s.orders != 0 {
						t.Fatalf("error = %v, orders = %d", err, s.orders)
					}
				})
			}
		})
	}
}

func TestProjectDuplicateBaselineKeys(t *testing.T) {
	for _, body := range []string{
		`{"success":true,"datas":[` + newProjectJSON + `],"datas":[]}`,
		projectList(strings.Replace(newProjectJSON, `"enableAutoRenew":false`, `"enableAutoRenew":true,"enableAutoRenew":false`, 1)),
	} {
		for _, create := range []bool{true, false} {
			s := &projectWriteServer{before: body, after: projectList(newProjectJSON)}
			c := newTestClient(t, s.handler(t))
			var err error
			if create {
				_, err = c.CreateProject(context.Background(), validProjectCreate())
			} else {
				_, err = c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
			}
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.Message != "list is null or malformed" || s.orders+s.deletes != 0 {
				t.Fatalf("create = %v, error = %v, writes = %d", create, err, s.orders+s.deletes)
			}
		}
	}
}

func TestProjectDuplicateRegionKeys(t *testing.T) {
	body := strings.Replace(testutil.FixtureBody(t, fixtures+"list_regions.json"), `"regionId": "<region-id-2>"`, `"regionId":"wrong","regionId":"<region-id-2>"`, 1)
	for _, quote := range []bool{true, false} {
		s := &projectWriteServer{}
		fallback := s.handler(t)
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/internal/v1/regions" {
				_, _ = w.Write([]byte(body))
				return
			}
			fallback.ServeHTTP(w, r)
		}))
		var err error
		if quote {
			_, err = c.QuoteCreateProject(context.Background(), validProjectCreate())
		} else {
			_, err = c.ListRegions(context.Background(), nil)
		}
		var api *vngcloud.APIError
		if !errors.As(err, &api) || api.Operation != "storage.ListRegions" || api.StatusCode != 200 || s.prices != 0 {
			t.Fatalf("error = %v, prices = %d", err, s.prices)
		}
		if len(c.regionIDs) != 0 {
			t.Fatal("malformed regions cached")
		}
	}
}

func TestProjectDuplicateWriteReplyKeys(t *testing.T) {
	for _, status := range []int{200, 400, 403} {
		for _, create := range []bool{true, false} {
			t.Run(strconv.Itoa(status)+"/"+strconv.FormatBool(create), func(t *testing.T) {
				duplicate := `{"success":true,"data":` + strings.Replace(newProjectJSON, `"enableAutoRenew":false`, `"enableAutoRenew":true,"enableAutoRenew":false`, 1) + `}`
				var unreadable error
				for _, body := range []string{
					`{"data":{}}`,
					duplicate,
					`{"success":false,"success":true}`,
					`{"success":false,"code":112,"code":403}`,
				} {
					s := &projectWriteServer{status: status, order: body}
					if create {
						s.after = projectList(newProjectJSON)
					} else {
						s.before = projectList(newProjectJSON)
					}
					c := newTestClient(t, s.handler(t))
					var err error
					if create {
						_, err = c.CreateProject(context.Background(), validProjectCreate())
					} else {
						_, err = c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
					}
					if !errors.Is(err, ErrNotSettled) || s.orders+s.deletes != 1 || s.lists != 1 {
						t.Fatalf("error = %v, writes = %d, lists = %d", err, s.orders+s.deletes, s.lists)
					}
					if unreadable == nil {
						unreadable = err
					} else if err.Error() != unreadable.Error() {
						t.Fatalf("error = %v, want %v", err, unreadable)
					}
				}
			})
		}
	}
}

func TestProjectDuplicateConfirmationKeys(t *testing.T) {
	for _, create := range []bool{true, false} {
		body := `{"success":true,"datas":[` + newProjectJSON + `],"datas":[]}`
		if create {
			body = projectList(strings.Replace(newProjectJSON, `"enableAutoRenew":false`, `"enableAutoRenew":true,"enableAutoRenew":false`, 1))
		}
		s := &projectWriteServer{after: body}
		if !create {
			s.before = projectList(newProjectJSON)
		}
		c := newTestClient(t, s.handler(t))
		fakeProjectClock(c)
		var err error
		if create {
			_, err = c.CreateProject(context.Background(), validProjectCreate())
		} else {
			_, err = c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
		}
		var api *vngcloud.APIError
		if !errors.Is(err, ErrNotSettled) || !errors.As(err, &api) || api.Message != "list is null or malformed" || s.orders+s.deletes != 1 {
			t.Fatalf("create = %v, error = %v, writes = %d", create, err, s.orders+s.deletes)
		}
	}
}
