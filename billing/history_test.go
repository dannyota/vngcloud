package billing

import (
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

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

type historyOptions struct {
	start, end string
	page, size int
	all        bool
}

type historyResult struct {
	Items                                []map[string]any
	Page, PageSize, TotalPage, TotalItem int
}

// Exercise the public methods, normalizing only their exported outputs.
func historyRead(t *testing.T, ctx context.Context, c *Client, payments bool, in *historyOptions) (*historyResult, error) {
	t.Helper()
	var result any
	var err error
	if payments {
		var input *ListPaymentsInput
		if in != nil {
			input = &ListPaymentsInput{StartDate: in.start, EndDate: in.end, Page: in.page, Size: in.size, All: in.all}
		}
		out, callErr := c.ListPayments(ctx, input)
		err = callErr
		if out == nil {
			return nil, err
		}
		result = out
	} else {
		var input *ListTransactionsInput
		if in != nil {
			input = &ListTransactionsInput{StartDate: in.start, EndDate: in.end, Page: in.page, Size: in.size, All: in.all}
		}
		out, callErr := c.ListTransactions(ctx, input)
		err = callErr
		if out == nil {
			return nil, err
		}
		result = out
	}
	if err != nil {
		t.Fatal("history returned output on error")
	}
	data, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatal("history output could not be encoded")
	}
	var out historyResult
	if json.Unmarshal(data, &out) != nil {
		t.Fatal("history output had invalid shape")
	}
	return &out, err
}

// All bodies built by these helpers are synthetic, not API evidence.
func historyRow(payments bool, id int) map[string]any {
	if payments {
		return map[string]any{"paymentID": id, "transactionID": "synthetic-reference", "payGate": "use_balance", "paymentStatus": "1", "paymentAmount": 0, "paymentType": 8084, "description": nil, "createTimeStamp": "2024-11-11T18:00:00.123+00:00"}
	}
	return map[string]any{"transactionID": id, "transactionType": 5, "creditStatus": false, "transactionValue": 0, "cumulativeValue": -12.5, "sourceReference": nil, "description": nil, "userServiceReference": nil, "invoiceId": nil, "createTimeStamp": "2024-11-11T18:00:00.123+00:00"}
}

func historyBody(payments bool, page, size, total int, ids ...int) string {
	rows := make([]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, historyRow(payments, id))
	}
	pages := 0
	if total > 0 {
		pages = (total-1)/size + 1
	}
	return historyJSON(map[string]any{"code": 200, "message": "synthetic account text", "data": map[string]any{"content": rows, "pageable": map[string]any{"number": page, "size": size, "totalPages": pages, "totalElements": total}}})
}

func historyJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func historyObject(body string) map[string]any {
	var result map[string]any
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		panic(err)
	}
	return result
}

func historyKinds(t *testing.T, fn func(*testing.T, bool, string)) {
	t.Helper()
	for _, name := range []string{"transactions", "payments"} {
		t.Run(name, func(t *testing.T) { fn(t, name == "payments", name) })
	}
}

func TestHistoryRawFixtures(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, name string) {
		prefix := "ListTransactions"
		if payments {
			prefix = "ListPayments"
		}
		for _, tc := range []struct {
			suffix           string
			page, size, rows int
			fail             bool
		}{
			{"", 1, 10, 10, false}, {"Page2", 2, 10, 10, false},
			{"BeyondEnd", 14, 10, 0, false}, {"Empty", 1, 50, 0, false},
			{"InvalidPageSize", 1, 50, 0, true},
		} {
			t.Run(tc.suffix, func(t *testing.T) {
				body := testutil.FixtureBody(t, "../testdata/billing/"+prefix+tc.suffix+".json")
				if tc.suffix == "BeyondEnd" {
					meta := historyObject(body)["data"].(map[string]any)["pageable"].(map[string]any)
					tc.page = int(meta["number"].(float64))
				}
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/gateway/api/v1/"+name {
						t.Error("wrong history route")
					}
					_, _ = w.Write([]byte(body))
				}))
				out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-12-31", page: tc.page, size: tc.size})
				if tc.fail {
					var api *vngcloud.APIError
					if out != nil || !errors.As(err, &api) || api.StatusCode != 200 || api.Code != "400" || api.Retryable || errors.Is(err, vngcloud.ErrInvalidInput) {
						t.Fatal("validation envelope identity lost")
					}
					if strings.Contains(err.Error(), "pageSize") || strings.Contains(err.Error(), "less than") {
						t.Fatal("validation text escaped")
					}
					return
				}
				if err != nil || out == nil || len(out.Items) != tc.rows || out.Items == nil || out.Page != tc.page || out.PageSize != tc.size {
					t.Fatal("raw fixture did not decode with its metadata")
				}
				metadata := historyObject(body)["data"].(map[string]any)["pageable"].(map[string]any)
				if out.TotalPage != int(metadata["totalPages"].(float64)) || out.TotalItem != int(metadata["totalElements"].(float64)) {
					t.Fatal("raw fixture totals changed")
				}
				if tc.rows == 0 {
					return
				}
				// Compare every modeled field with the sanitized raw body.
				wire := historyObject(body)["data"].(map[string]any)["content"].([]any)
				mapping := map[string]string{"ID": "transactionID", "TypeCode": "transactionType", "Amount": "transactionValue", "Balance": "cumulativeValue", "Description": "description", "SourceReference": "sourceReference", "UserServiceReference": "userServiceReference", "InvoiceID": "invoiceId", "CreatedAt": "createTimeStamp"}
				if payments {
					mapping = map[string]string{"ID": "paymentID", "TypeCode": "paymentType", "Amount": "paymentAmount", "TransactionID": "transactionID", "Gateway": "payGate", "Status": "paymentStatus", "Description": "description", "CreatedAt": "createTimeStamp"}
				}
				for i, item := range out.Items {
					raw := wire[i].(map[string]any)
					for field, key := range mapping {
						if !reflect.DeepEqual(item[field], raw[key]) {
							t.Fatalf("modeled field %s did not match raw fixture", field)
						}
					}
					if !payments {
						direction := DirectionDebit
						if raw["creditStatus"] == true {
							direction = DirectionCredit
						}
						wantType := map[float64]string{1: TransactionPurchase, 2: TransactionRefund, 5: TransactionCharge}[raw["transactionType"].(float64)]
						if item["Direction"] != direction || item["Type"] != wantType {
							t.Fatal("transaction derivation failed")
						}
					}
				}
				if payments && wire[0].(map[string]any)["payGateName"] != nil {
					t.Fatal("raw unknown null field changed")
				}
			})
		}
	})
}

func TestHistoryRoutesAndNormalization(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, name string) {
		for _, tc := range []struct{ page, size, wantPage, wantSize int }{{0, 0, 1, 50}, {-1, -1, 1, 50}, {2, 100, 2, 50}, {1, 1, 1, 1}, {1, 49, 1, 49}} {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, readErr := io.ReadAll(r.Body)
				if readErr != nil || len(body) != 0 {
					t.Error("history sent a request body")
				}
				if r.Method != "GET" || r.ContentLength > 0 || r.URL.Path != "/gateway/api/v1/"+name {
					t.Error("history used a non-read route")
				}
				q := r.URL.Query()
				if len(q) != 4 || q.Get("pageNumber") != strconv.Itoa(tc.wantPage) || q.Get("pageSize") != strconv.Itoa(tc.wantSize) || q.Get("dateStart") != "29/02/2024" || q.Get("dateEnd") != "01/03/2024" || !strings.Contains(r.URL.RawQuery, "%2F") {
					t.Error("incorrect history query")
				}
				_, _ = w.Write([]byte(historyBody(payments, tc.wantPage, tc.wantSize, 0)))
			}))
			out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-02-29", end: "2024-02-29", page: tc.page, size: tc.size})
			if err != nil || out == nil {
				t.Fatal("normalized request failed")
			}
		}
	})
}

func TestHistoryInvalidInput(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		c := New(vngcloud.Config{}) // Invalid inputs must precede even configuration/authentication.
		for _, in := range []*historyOptions{nil, {}, {start: "2023-02-29"}, {start: "2024-2-01"}, {start: "2024-01-01T00:00:00Z"}, {start: "2024-01-01", end: "bad"}, {start: "2024-01-02", end: "2024-01-01"}, {start: "9999-12-31", end: "9999-12-31"}, {start: "2024-01-01", all: true, page: 2}} {
			out, err := historyRead(t, context.Background(), c, payments, in)
			if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatal("invalid input reached configuration or authentication")
			}
		}
	})
}

func TestHistoryStatusErrors(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, tc := range []struct {
			status   int
			code     string
			sentinel error
			retry    bool
		}{
			{400, "BadRequest", nil, false}, {401, "Unauthorized", vngcloud.ErrAuth, false}, {403, "Forbidden", vngcloud.ErrPermission, false}, {404, "NotFound", vngcloud.ErrNotFound, false}, {429, "Throttled", vngcloud.ErrRateLimited, true}, {500, "ServerError", nil, false}, {502, "ServerError", nil, true}, {503, "ServerError", nil, true}, {504, "ServerError", nil, true}, {201, "InvalidResponse", nil, false}, {204, "InvalidResponse", nil, false}, {302, "InvalidResponse", nil, false}, {409, "Conflict", nil, false}, {418, "ClientError", nil, false},
		} {
			t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(`{"code":"synthetic-account-code","message":"Budget not found synthetic-account-marker"}`))
				}))
				out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
				var api *core.APIError
				if out != nil || !errors.As(err, &api) || api.StatusCode != tc.status || api.Code != tc.code || api.Operation == "" || api.Retryable != tc.retry {
					t.Fatalf("wrong status identity for %d", tc.status)
				}
				if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
					t.Fatal("status sentinel lost")
				}
				for cause := err; cause != nil; cause = errors.Unwrap(cause) {
					if strings.Contains(cause.Error(), "synthetic-account") || strings.Contains(cause.Error(), "Budget not found") {
						t.Fatal("peer text escaped in error chain")
					}
				}
			})
		}
	})
}

func TestHistoryEnvelopeCodes(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, tc := range []struct {
			code string
			want string
		}{{"200", ""}, {`"200"`, ""}, {"null", ""}, {"201", ""}, {"400", "400"}, {`"403"`, "403"}, {"500", "500"}, {`"peer-account"`, "InvalidResponse"}, {"{}", "InvalidResponse"}, {"true", "InvalidResponse"}, {"1.5", "InvalidResponse"}} {
			body := strings.Replace(historyBody(payments, 1, 50, 0), `"code":200`, `"code":`+tc.code, 1)
			if tc.want != "" && tc.want != "InvalidResponse" {
				body = `{"code":` + tc.code + `,"data":[{"errorMsg":"synthetic-account-marker"}]}`
			}
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
			if tc.want == "" {
				if err != nil || out == nil {
					t.Fatal("accepted code failed")
				}
				continue
			}
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.Code != tc.want || api.StatusCode != 200 || api.Retryable || errors.Is(err, vngcloud.ErrPermission) || errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatal("wrong envelope failure identity")
			}
		}
	})
}

func TestHistoryMalformedFields(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		base := historyBody(payments, 1, 50, 1, 1)
		var bodies []string
		bodies = append(bodies, "", `{}`, `null`, `[]`, `{"code":200}`, `{"code":200,"data":null}`, `{"code":200,"data":[]}`, `{"data":{}}`)
		for _, field := range []string{"content", "pageable"} {
			for _, mode := range []string{"missing", "null", "wrong"} {
				obj := historyObject(base)
				data := obj["data"].(map[string]any)
				switch mode {
				case "missing":
					delete(data, field)
				case "null":
					data[field] = nil
				default:
					data[field] = "synthetic-account"
				}
				bodies = append(bodies, historyJSON(obj))
			}
		}
		for _, field := range []string{"number", "size", "totalPages", "totalElements"} {
			for _, value := range []any{nil, "synthetic-account", -1, 1.5} {
				obj := historyObject(base)
				obj["data"].(map[string]any)["pageable"].(map[string]any)[field] = value
				bodies = append(bodies, historyJSON(obj))
			}
			obj := historyObject(base)
			delete(obj["data"].(map[string]any)["pageable"].(map[string]any), field)
			bodies = append(bodies, historyJSON(obj))
		}
		for field := range historyRow(payments, 1) {
			for _, mode := range []string{"missing", "wrong", "null"} {
				obj := historyObject(base)
				row := obj["data"].(map[string]any)["content"].([]any)[0].(map[string]any)
				if mode == "null" && (field == "description" || field == "sourceReference" || field == "userServiceReference" || field == "invoiceId") {
					continue
				}
				switch mode {
				case "missing":
					delete(row, field)
				case "null":
					row[field] = nil
				default:
					row[field] = map[string]any{"synthetic-account": true}
				}
				bodies = append(bodies, historyJSON(obj))
			}
		}
		for _, change := range []map[string]any{{"createTimeStamp": "2024-01-01T00:00:00"}, {"createTimeStamp": "bad"}, {"paymentAmount": -1}, {"transactionValue": -1}, {"transactionID": 0}, {"paymentID": 0}, {"transactionType": 1, "creditStatus": false}, {"transactionType": 2, "creditStatus": false}, {"transactionType": 5, "creditStatus": true}} {
			if payments {
				if _, ok := change["transactionType"]; ok {
					continue
				}
				if _, ok := change["transactionValue"]; ok {
					continue
				}
				if _, ok := change["transactionID"]; ok {
					continue
				}
			} else {
				if _, ok := change["paymentAmount"]; ok {
					continue
				}
				if _, ok := change["paymentID"]; ok {
					continue
				}
			}
			obj := historyObject(base)
			row := obj["data"].(map[string]any)["content"].([]any)[0].(map[string]any)
			for k, v := range change {
				row[k] = v
			}
			bodies = append(bodies, historyJSON(obj))
		}
		bodies = append(bodies, strings.Replace(base, `"size":50`, `"size":9223372036854775808`, 1), strings.Replace(base, `"paymentAmount":0`, `"paymentAmount":1e999`, 1))
		if !payments {
			bodies = bodies[:len(bodies)-1]
			bodies = append(bodies, strings.Replace(base, `"transactionValue":0`, `"transactionValue":1e999`, 1))
		}
		for i, body := range bodies {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
				out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 {
					t.Fatal("malformed response accepted or identity lost")
				}
				if strings.Contains(err.Error(), "synthetic-account") {
					t.Fatal("malformed peer text escaped")
				}
			})
		}
	})
}

func TestHistoryUnknownAndNullableFields(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, value := range []any{nil, "", "synthetic description"} {
			obj := historyObject(historyBody(payments, 1, 50, 1, 1))
			row := obj["data"].(map[string]any)["content"].([]any)[0].(map[string]any)
			row["description"] = value
			if payments {
				row["payGate"] = "future gateway"
				row["paymentStatus"] = "future status"
				row["paymentType"] = -9
			} else {
				row["transactionType"] = -9
				row["sourceReference"] = value
				row["userServiceReference"] = value
				row["invoiceId"] = -2
			}
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(historyJSON(obj))) }))
			out, err := historyRead(t, context.Background(), c, payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
			if err != nil || out == nil || out.Items[0]["Description"] != value || out.Items[0]["TypeCode"] != float64(-9) {
				t.Fatal("nullable or unknown value lost")
			}
			if _, exists := out.Items[0]["Description"]; !exists {
				t.Fatal("nullable description omitted from JSON")
			}
			if payments {
				if out.Items[0]["Gateway"] != "future gateway" || out.Items[0]["Status"] != "future status" {
					t.Fatal("unknown payment strings lost")
				}
			} else if out.Items[0]["Type"] != "" || out.Items[0]["SourceReference"] != value || out.Items[0]["InvoiceID"] != float64(-2) {
				t.Fatal("unknown transaction or pointer lost")
			}
		}
	})
}

func TestHistoryCallerInputUnchanged(t *testing.T) {
	transaction := ListTransactionsInput{StartDate: "2024-01-01", EndDate: "2024-01-01", Page: -1, Size: 100, All: true}
	before := transaction
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(historyBody(false, 1, 50, 0))) }))
	if _, err := c.ListTransactions(context.Background(), &transaction); err != nil || transaction != before {
		t.Fatal("transaction input mutated or normalization failed")
	}
	payment := ListPaymentsInput(transaction)
	paymentBefore := payment
	c = newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(historyBody(true, 1, 50, 0))) }))
	if _, err := c.ListPayments(context.Background(), &payment); err != nil || payment != paymentBefore {
		t.Fatal("payment input mutated or normalization failed")
	}
}

func TestHistoryValidationArraysDoNotRetry(t *testing.T) {
	historyKinds(t, func(t *testing.T, payments bool, _ string) {
		for _, field := range []string{"pageSize", "pageNumber", "dateStart", "dateEnd"} {
			calls := 0
			cfg := testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				_, _ = fmt.Fprintf(w, `{"code":400,"message":"synthetic-account","data":[{"type":"Not_null","field":%q,"errorMsg":"synthetic-validation"}]}`, field)
			}))
			out, err := historyRead(t, context.Background(), New(cfg), payments, &historyOptions{start: "2024-01-01", end: "2024-01-01"})
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || calls != 1 || api.StatusCode != 200 || api.Code != "400" || api.Retryable || errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatal("validation array was decoded as success, retried, or misclassified")
			}
			if strings.Contains(err.Error(), "synthetic-") || strings.Contains(err.Error(), field) {
				t.Fatal("validation array escaped")
			}
		}
	})
}
