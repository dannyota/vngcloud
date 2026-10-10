package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

const cliNewStorageProject = `{"projectId":"new-1","projectName":"backups","regionId":"region-hcm","regionName":"HCM04","status":1,"totalQuota":30,"projectType":1,"purchaseTypeId":4,"enableAutoRenew":false,"autoRenewPeriod":0}`

func storageProjectWriteRoutes(t *testing.T, orders, deletes, prices *int) map[string]func(http.ResponseWriter, *http.Request) {
	t.Helper()
	routes := storageProjectPricingRoutes(t, "region-hcm", 30, prices)
	routes["/internal/v1/billing/project_types"] = jsonHandler(http.StatusOK, strings.Replace(storageProjectTypesBody, `"title":"Gold Type"`, `"title":"Gold Type","group":"Gold"`, 1))
	routes["/internal/v1/projects"] = func(w http.ResponseWriter, r *http.Request) {
		body := `{"success":true,"datas":[]}`
		if *orders > 0 {
			body = `{"success":true,"datas":[` + cliNewStorageProject + `]}`
		}
		jsonHandler(http.StatusOK, body)(w, r)
	}
	routes["/internal/v2/orders"] = func(w http.ResponseWriter, r *http.Request) {
		*orders++
		if r.Method != http.MethodPost || r.URL.Query().Get("region_id") != "region-hcm" || r.Header.Get("region") != "region-hcm" || r.Header.Get("region_id") != "region-hcm" || *prices != 1 {
			t.Error("order method, region, or quote count differs from contract")
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		want := map[string]any{"resourceType": "object_storage", "action": "create", "paymentType": "auto", "resourceInfo": map[string]any{"projectName": "backups", "quota": float64(30), "purchaseTypeId": float64(4), "projectType": float64(1), "projectTypeGroup": "Gold", "archivePeriod": float64(0), "billingTimeType": "block", "isTrial": false, "isPoc": false, "enableAutoRenew": false, "autoRenewPeriod": float64(0)}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("order body = %#v, want %#v", got, want)
		}
		jsonHandler(http.StatusOK, `{"success":true,"data":`+cliNewStorageProject+`}`)(w, r)
	}
	routes["/internal/v1/ceph/projects/new-1"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("limit") != "1000" {
			t.Error("bucket guard method or limit")
		}
		jsonHandler(http.StatusOK, `{"success":true,"datas":[]}`)(w, r)
	}
	routes["/internal/v1/projects/new-1"] = func(w http.ResponseWriter, r *http.Request) {
		*deletes++
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "{}" || r.Method != http.MethodDelete || r.URL.Query().Get("region_id") != "region-hcm" || r.Header.Get("region") != "region-hcm" || r.Header.Get("region_id") != "region-hcm" {
			t.Errorf("delete request: method %s body %s error %v", r.Method, body, err)
		}
		jsonHandler(http.StatusOK, `{"success":true}`)(w, r)
	}
	return routes
}

func storageProjectCreateArgs() []string {
	return []string{"storage", "create-project", "--name", "backups", "--type", "Gold", "--quota-gb", "30", "--max-price", "30000"}
}

func TestStorageProjectCreateOrderAndOutput(t *testing.T) {
	for _, noWait := range []bool{false, true} {
		t.Run(strconv.FormatBool(noWait), func(t *testing.T) {
			orders, deletes, prices := 0, 0, 0
			args := storageProjectCreateArgs()
			if noWait {
				args = append(args, "--no-wait")
			}
			r := runStorage(t, storageProjectWriteRoutes(t, &orders, &deletes, &prices), args...)
			if r.err != nil || orders != 1 || deletes != 0 || !strings.Contains(r.stdout, `"ID": "new-1"`) || !strings.Contains(r.stdout, `"TotalPrice": 30000`) {
				t.Fatalf("error %v orders %d deletes %d output %s", r.err, orders, deletes, r.stdout)
			}
		})
	}
}

func TestStorageProjectCreatePriceGuards(t *testing.T) {
	for _, cap := range []string{"0", "29999", ""} {
		orders, deletes, prices := 0, 0, 0
		args := storageProjectCreateArgs()
		args = args[:len(args)-2]
		if cap != "" {
			args = append(args, "--max-price", cap)
		}
		r := runStorage(t, storageProjectWriteRoutes(t, &orders, &deletes, &prices), args...)
		if r.err == nil || classify(r.err).Code != "PriceAboveMax" || exitCode(r.err) != 1 || orders != 0 || prices != 1 {
			t.Fatalf("cap %q: error %v orders %d prices %d", cap, r.err, orders, prices)
		}
	}
}

func TestStorageProjectRequiredFlagsBeforeRequests(t *testing.T) {
	for _, missing := range []string{"name", "type", "quota-gb", "project-id", "yes"} {
		t.Run(missing, func(t *testing.T) {
			args := storageProjectCreateArgs()
			switch missing {
			case "project-id":
				args = []string{"storage", "delete-project", "--yes"}
			case "yes":
				args = []string{"storage", "delete-project", "--project-id", "new-1"}
			default:
				for i := 2; i < len(args); i += 2 {
					if args[i] == "--"+missing {
						args = append(args[:i], args[i+2:]...)
						break
					}
				}
			}
			r := runStorage(t, map[string]func(http.ResponseWriter, *http.Request){}, args...)
			if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "--"+missing) || r.fixture.requestCount() != 0 {
				t.Fatalf("error %v requests %d", r.err, r.fixture.requestCount())
			}
		})
	}
}

func TestStorageProjectWritesReadOnlyProfile(t *testing.T) {
	for _, op := range []string{"create-project", "delete-project"} {
		t.Run(op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)
			root := newRootCmd(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			args := storageProjectCreateArgs()
			if op == "delete-project" {
				args = []string{"storage", op, "--project-id", "new-1", "--yes"}
			}
			root.SetArgs(append([]string{"--profile", "agent"}, args...))
			err := root.ExecuteContext(context.Background())
			if err == nil || exitCode(err) != 2 || classify(err).Code != "ReadOnly" || fixture.requestCount() != 0 {
				t.Fatalf("error %v requests %d", err, fixture.requestCount())
			}
		})
	}
}

func TestStorageProjectDeleteGuardAndRequest(t *testing.T) {
	for _, mode := range []string{"wait", "no-wait", "buckets"} {
		t.Run(mode, func(t *testing.T) {
			orders, deletes, prices := 0, 0, 0
			routes := storageProjectWriteRoutes(t, &orders, &deletes, &prices)
			routes["/internal/v1/projects"] = func(w http.ResponseWriter, r *http.Request) {
				body := `{"success":true,"datas":[]}`
				if deletes == 0 {
					body = `{"success":true,"datas":[` + cliNewStorageProject + `]}`
				}
				jsonHandler(http.StatusOK, body)(w, r)
			}
			if mode == "buckets" {
				routes["/internal/v1/ceph/projects/new-1"] = jsonHandler(http.StatusOK, storageBucketsBody)
			}
			args := []string{"storage", "delete-project", "--project-id", "new-1", "--yes"}
			if mode == "no-wait" {
				args = append(args, "--no-wait")
			}
			r := runStorage(t, routes, args...)
			if mode == "buckets" {
				if r.err == nil || classify(r.err).Code != "ProjectNotEmpty" || exitCode(r.err) != 1 || deletes != 0 {
					t.Fatalf("error %v deletes %d", r.err, deletes)
				}
			} else if r.err != nil || deletes != 1 || orders != 0 || prices != 0 {
				t.Fatalf("error %v deletes %d orders %d prices %d", r.err, deletes, orders, prices)
			}
		})
	}
}

func TestStorageProjectWriteErrorMappings(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{vngcloud.ErrPriceAboveMax, "PriceAboveMax"}, {vngcloud.ErrUnpriced, "Unpriced"},
		{storage.ErrPaymentRequired, "PaymentRequired"}, {storage.ErrNotSettled, "NotSettled"},
		{storage.ErrFailed, "WriteFailed"}, {storage.ErrProjectNotEmpty, "ProjectNotEmpty"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			h := newFakeHarness(t)
			var op Op[storage.Client]
			for _, candidate := range storageOps {
				if candidate.name == "create-project" {
					op = candidate
				}
			}
			if op.name == "" {
				t.Fatal("create-project is not registered")
			}
			op.call = func(_ *cobra.Command, _ *storage.Client, _ context.Context, _ any) (any, error) {
				return nil, fmt.Errorf("%w: %w", tc.err, &vngcloud.APIError{Code: "InnerError", StatusCode: 400})
			}
			root := newTestRoot(h.e)
			root.AddCommand(Service(h.e, "storage", "test", storage.New, op))
			err := execCmd(t, root, storageProjectCreateArgs())
			if !errors.Is(err, tc.err) || classify(err).Code != tc.code || exitCode(err) != 1 || h.stdout.Len() != 0 {
				t.Fatalf("error %v code %s exit %d output %s", err, classify(err).Code, exitCode(err), h.stdout.String())
			}
			var printed bytes.Buffer
			printError(&printed, err)
			var envelope struct {
				Error errorEnvelope `json:"error"`
			}
			if json.Unmarshal(printed.Bytes(), &envelope) != nil || envelope.Error.Code != tc.code {
				t.Fatalf("stderr %s", printed.String())
			}
		})
	}
}

func TestStorageProjectCreateResponseErrors(t *testing.T) {
	for _, code := range []string{"Unpriced", "PaymentRequired", "NotSettled"} {
		t.Run(code, func(t *testing.T) {
			orders, deletes, prices := 0, 0, 0
			routes := storageProjectWriteRoutes(t, &orders, &deletes, &prices)
			wantOrders := 1
			switch code {
			case "Unpriced":
				wantOrders = 0
				routes["/billing-api/v2/price"] = jsonHandler(http.StatusOK, `{"success":true,"data":{"optimumPrice":0}}`)
			case "PaymentRequired":
				routes["/internal/v2/orders"] = func(w http.ResponseWriter, r *http.Request) {
					orders++
					jsonHandler(http.StatusOK, `{"success":true,"data":{"redirectUrl":"https://checkout.example/?token=private-checkout"}}`)(w, r)
				}
				routes["/internal/v1/projects"] = jsonHandler(http.StatusOK, `{"success":true,"datas":[]}`)
			case "NotSettled":
				routes["/internal/v2/orders"] = func(w http.ResponseWriter, r *http.Request) {
					orders++
					jsonHandler(http.StatusOK, `{"success":true,"data":null}`)(w, r)
				}
			}
			r := runStorage(t, routes, append([]string{"--debug"}, storageProjectCreateArgs()...)...)
			if r.err == nil || classify(r.err).Code != code || exitCode(r.err) != 1 || orders != wantOrders {
				t.Fatalf("error %v code %s orders %d output %s", r.err, classify(r.err).Code, orders, r.stdout)
			}
			if code == "NotSettled" {
				var out storage.CreateProjectOutput
				if json.Unmarshal([]byte(r.stdout), &out) != nil || out.TotalPrice != 30000 || out.Project != nil {
					t.Fatalf("partial output = %s", r.stdout)
				}
			} else if r.stdout != "" {
				t.Fatalf("unexpected output = %s", r.stdout)
			}
			var printed bytes.Buffer
			printError(&printed, r.err)
			for _, secret := range []string{"private-checkout", "test-token"} {
				if strings.Contains(printed.String()+r.stdout+r.stderr, secret) {
					t.Fatalf("output exposes %s", secret)
				}
			}
		})
	}
}

func TestStorageProjectWriteExitPrecedence(t *testing.T) {
	for _, sentinel := range []error{storage.ErrNotSettled, storage.ErrFailed, storage.ErrPaymentRequired, storage.ErrProjectNotEmpty} {
		err := fmt.Errorf("%w: %w", sentinel, &vngcloud.APIError{StatusCode: 404, Code: "NotFound"})
		if exitCode(err) != 1 {
			t.Errorf("%v exits %d, want 1", err, exitCode(err))
		}
	}
}

func TestStorageProjectCreateNameRequiredInDocs(t *testing.T) {
	for _, op := range buildDocService("storage", storageOps).ops {
		if op.name != "create-project" {
			continue
		}
		for _, field := range op.fields {
			if field.name == "name" {
				if !field.required {
					t.Fatal("create-project name must be required in the flag table")
				}
				return
			}
		}
	}
	t.Fatal("create-project name is missing from the flag table")
}
