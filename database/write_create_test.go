package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func assertNoSecret(t *testing.T, label string, values ...any) {
	t.Helper()
	for _, v := range values {
		var text strings.Builder
		fmt.Fprintf(&text, "%v %+v %#v", v, v, v)
		if err, ok := v.(error); ok {
			for e := err; e != nil; e = errors.Unwrap(e) {
				text.WriteString(" " + e.Error())
			}
		}
		if strings.Contains(text.String(), testPassword) {
			t.Fatalf("%s leaks the password: %s", label, text.String())
		}
	}
}

func TestCreateWire(t *testing.T) {
	s := &script{detail: []string{detailBody("ACTIVE", nil)}}
	h := newHarness(t, s.handler())
	out, err := h.client.CreateDatabaseInstance(context.Background(), validCreateInput())
	if err != nil || out.Instance.ID != "db-1" || out.Instance.Status != "ACTIVE" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
	var create *recorded
	for _, r := range h.log() {
		if r.Method == http.MethodPost {
			if create != nil {
				t.Fatal("more than one POST")
			}
			r := r
			create = &r
		}
	}
	if create == nil || create.Path != pathCreate || create.Query != "" {
		t.Fatalf("create request %#v", create)
	}
	if create.Header.Get("user-type") != "IAM_USER" || create.Header.Get("Content-Type") != "application/json" || create.Header.Get("Authorization") != "Bearer bearer-synthetic" {
		t.Fatalf("headers %v", create.Header)
	}
	want := `{"user":{"name":"admin_user","password":"` + testPassword + `","databases":[{"name":"appdb"}]},"databases":[{"name":"appdb","characterSet":"utf8mb4","collate":"utf8mb4_general_ci"}],"name":"demo-db","volumeType":"Gen2-NVMe2-IOPS3000","volumeSize":20,"datastoreType":"MySQL","datastoreVersion":"8.0","netIds":["subnet-1"],"publicAccess":false,"backupAuto":false,"packageId":"211","poc":false,"locateZoneId":"zone-1"}`
	var gotJSON, wantJSON any
	if err := json.Unmarshal([]byte(create.Body), &gotJSON); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(want), &wantJSON)
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatalf("body %s", create.Body)
	}
	for _, r := range h.log() {
		if r.Method == http.MethodGet && (r.Header.Get("user-type") != "" || strings.Contains(r.Body, testPassword) || strings.Contains(r.Query, testPassword)) {
			t.Fatalf("read leaked: %#v", r)
		}
	}
	if h.count("GET", pathDetail) != 1 {
		t.Fatalf("detail reads %d", h.count("GET", pathDetail))
	}
	assertNoSecret(t, "output", out)
}

func TestCreateInputValidationSendsNothing(t *testing.T) {
	cases := []struct {
		name  string
		field string
		edit  func(*CreateDatabaseInstanceInput)
	}{
		{"empty name", "Name", func(in *CreateDatabaseInstanceInput) { in.Name = "" }},
		{"bad name", "Name", func(in *CreateDatabaseInstanceInput) { in.Name = "bad name!" }},
		{"21 character name", "Name", func(in *CreateDatabaseInstanceInput) { in.Name = strings.Repeat("a", 21) }},
		{"hyphen first name", "Name", func(in *CreateDatabaseInstanceInput) { in.Name = "-abc" }},
		{"empty zone", "ZoneID", func(in *CreateDatabaseInstanceInput) { in.ZoneID = "" }},
		{"bad zone", "ZoneID", func(in *CreateDatabaseInstanceInput) { in.ZoneID = "a/b" }},
		{"zero flavor", "FlavorID", func(in *CreateDatabaseInstanceInput) { in.FlavorID = 0 }},
		{"negative flavor", "FlavorID", func(in *CreateDatabaseInstanceInput) { in.FlavorID = -1 }},
		{"empty storage type", "StorageTypeID", func(in *CreateDatabaseInstanceInput) { in.StorageTypeID = "" }},
		{"zero size", "StorageSize", func(in *CreateDatabaseInstanceInput) { in.StorageSize = 0 }},
		{"negative size", "StorageSize", func(in *CreateDatabaseInstanceInput) { in.StorageSize = -5 }},
		{"bad vpc", "VPCID", func(in *CreateDatabaseInstanceInput) { in.VPCID = "v pc" }},
		{"empty subnet", "SubnetID", func(in *CreateDatabaseInstanceInput) { in.SubnetID = "" }},
		{"bad database name", "DatabaseName", func(in *CreateDatabaseInstanceInput) { in.DatabaseName = "app;db" }},
		{"empty username", "Username", func(in *CreateDatabaseInstanceInput) { in.Username = "" }},
		{"bad username", "Username", func(in *CreateDatabaseInstanceInput) { in.Username = "a'b" }},
		{"empty password", "Password", func(in *CreateDatabaseInstanceInput) { in.Password = "" }},
		{"control password", "Password", func(in *CreateDatabaseInstanceInput) { in.Password = "abc\ndef" }},
		{"invalid utf8 password", "Password", func(in *CreateDatabaseInstanceInput) { in.Password = vngcloud.Secret("abc\xff") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, (&script{}).handler())
			in := validCreateInput()
			tc.edit(in)
			out, err := h.client.CreateDatabaseInstance(context.Background(), in)
			if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) || !strings.Contains(err.Error(), tc.field) || len(h.log()) != 0 {
				t.Fatalf("out=%v err=%v requests=%d", out, err, len(h.log()))
			}
			assertNoSecret(t, "error", err)
		})
	}
	t.Run("nil input", func(t *testing.T) {
		h := newHarness(t, (&script{}).handler())
		if _, err := h.client.CreateDatabaseInstance(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) || len(h.log()) != 0 {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestCreateCatalogMismatchSendsNoPost(t *testing.T) {
	cases := []struct {
		name string
		edit func(*CreateDatabaseInstanceInput)
		wrap func(http.ResponseWriter, *http.Request) bool
	}{
		{"no mysql 8.0", nil, func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == pathVersions {
				writeJSON(w, 200, `{"code":200,"data":[{"name":"MySQL","type":"MySQL","version":"5.7"},{"type":"PostgreSQL","version":"8.0"}]}`)
				return true
			}
			return false
		}},
		{"unknown zone", func(in *CreateDatabaseInstanceInput) { in.ZoneID = "zone-9" }, nil},
		{"unknown flavor", func(in *CreateDatabaseInstanceInput) { in.FlavorID = 999 }, nil},
		{"unknown storage type", func(in *CreateDatabaseInstanceInput) { in.StorageTypeID = "SATA" }, nil},
		{"size below minimum", func(in *CreateDatabaseInstanceInput) { in.StorageSize = 10 }, nil},
		{"size above maximum", func(in *CreateDatabaseInstanceInput) { in.StorageSize = 1001 }, nil},
		{"unknown vpc", func(in *CreateDatabaseInstanceInput) { in.VPCID = "vpc-9" }, nil},
		{"subnet not in vpc", func(in *CreateDatabaseInstanceInput) { in.SubnetID = "subnet-9" }, nil},
		{"catalog read fails", nil, func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == pathFlavors {
				writeJSON(w, 500, `{"message":"boom"}`)
				return true
			}
			return false
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &script{}
			base := s.handler()
			h := newHarness(t, func(w http.ResponseWriter, r *http.Request, h *harness) {
				if tc.wrap != nil && tc.wrap(w, r) {
					return
				}
				base(w, r, h)
			})
			in := validCreateInput()
			if tc.edit != nil {
				tc.edit(in)
			}
			out, err := h.client.CreateDatabaseInstance(context.Background(), in)
			if out != nil || err == nil || h.posts() != 0 || errors.Is(err, ErrNotSettled) {
				t.Fatalf("out=%v err=%v posts=%d", out, err, h.posts())
			}
			assertNoSecret(t, "error", err)
		})
	}
}

type testProvider struct{ calls int }

func (p *testProvider) Token(context.Context) (vngcloud.Token, error) {
	p.calls++
	return vngcloud.Token{}, nil
}
func (*testProvider) Invalidate(string) {}

func TestWritesRejectUntrustedConfigBeforeRequests(t *testing.T) {
	provider := &testProvider{}
	for _, tc := range []struct {
		name    string
		options []vngcloud.LoadOption
	}{
		{"vserver", []vngcloud.LoadOption{vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: "https://invalid.example/"})}},
		{"database", []vngcloud.LoadOption{vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Database: "https://invalid.example/"})}},
		{"signin", []vngcloud.LoadOption{vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Signin: "https://invalid.example"})}},
		{"token", []vngcloud.LoadOption{vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Token: "https://invalid.example"})}},
		{"dashboard", []vngcloud.LoadOption{vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Dashboard: "https://invalid.example"})}},
		{"static", []vngcloud.LoadOption{vngcloud.WithStaticToken("synthetic")}},
		{"provider", []vngcloud.LoadOption{vngcloud.WithCredentialsProvider(provider)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			rt := roundTrip(func(*http.Request) (*http.Response, error) { requests++; return nil, errors.New("unexpected request") })
			c := New(iamConfig(t, rt, tc.options...))
			_, createErr := c.CreateDatabaseInstance(context.Background(), validCreateInput())
			_, deleteErr := c.DeleteDatabaseInstance(context.Background(), &DeleteDatabaseInstanceInput{ID: "db-1"})
			if !errors.Is(createErr, vngcloud.ErrInvalidConfig) || !errors.Is(deleteErr, vngcloud.ErrInvalidConfig) || requests != 0 || provider.calls != 0 {
				t.Fatalf("create=%v delete=%v requests=%d provider=%d", createErr, deleteErr, requests, provider.calls)
			}
		})
	}
	t.Run("static token with loopback endpoint", func(t *testing.T) {
		requests := 0
		cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic"), vngcloud.WithTransport(roundTrip(func(*http.Request) (*http.Response, error) { requests++; return nil, errors.New("x") })))
		if err != nil {
			t.Fatal(err)
		}
		_, err = New(cfg).CreateDatabaseInstance(context.Background(), validCreateInput())
		if !errors.Is(err, vngcloud.ErrInvalidConfig) || requests != 0 {
			t.Fatalf("err=%v requests=%d", err, requests)
		}
	})
}

func TestCreateSendsOnce(t *testing.T) {
	cases := []struct {
		name     string
		respond  func(w http.ResponseWriter, r *http.Request)
		wantAPI  int
		settled  bool
		wantCode string
	}{
		{"401", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 401, `{"message":"no"}`) }, 401, false, "Unauthorized"},
		{"429", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 429, `{"message":"slow"}`) }, 429, false, ""},
		{"400", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 400, `{"message":"Insufficient credits"}`) }, 400, false, "BadRequest"},
		{"500", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 500, `{"message":"boom"}`) }, 0, true, ""},
		{"503", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 503, `{"message":"boom"}`) }, 0, true, ""},
		{"302", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://"+r.Host+"/elsewhere")
			w.WriteHeader(302)
		}, 0, true, ""},
		{"307", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://"+r.Host+pathCreate)
			w.WriteHeader(307)
		}, 0, true, ""},
		{"202 without body", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(202) }, 0, true, ""},
		{"dropped connection", func(w http.ResponseWriter, _ *http.Request) {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		}, 0, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &script{create: tc.respond}
			h := newHarness(t, s.handler())
			out, err := h.client.CreateDatabaseInstance(context.Background(), validCreateInput())
			if h.posts() != 1 || (h.count("POST", "/elsewhere")) != 0 {
				t.Fatalf("posts=%d", h.posts())
			}
			if tc.settled {
				if !errors.Is(err, ErrNotSettled) || out != nil && out.Instance.ID != "" {
					t.Fatalf("out=%v err=%v", out, err)
				}
			} else {
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.StatusCode != tc.wantAPI || errors.Is(err, ErrNotSettled) || (tc.wantCode != "" && api.Code != tc.wantCode) {
					t.Fatalf("err=%v", err)
				}
			}
			assertNoSecret(t, "error", err)
		})
	}
}

func TestCreateClientErrorMessagesAreFixedText(t *testing.T) {
	for _, body := range []string{
		`{"message":"Insufficient credits"}`,
		`{"message":"Name demo-db is invalid"}`,
		`{"message":"bad password ` + testPassword + ` rejected"}`,
		`{"message":"bad ` + strings.Replace(testPassword, "C", "\\u0043", 1) + `"}`,
		`{"message":"token bearer-synthetic"}`,
		`{"message":"` + strings.Repeat("x", 600) + `"}`,
	} {
		s := &script{create: func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 400, body) }}
		h := newHarness(t, s.handler())
		_, err := h.client.CreateDatabaseInstance(context.Background(), validCreateInput())
		var api *vngcloud.APIError
		if !errors.As(err, &api) || api.StatusCode != 400 || api.Message != "request rejected by the API" {
			t.Fatalf("body %s err=%v", body, err)
		}
		assertNoSecret(t, "error", err)
	}
}

func TestCreateRejectsUnsafeAcceptance(t *testing.T) {
	cases := []struct{ name, body string }{
		{"empty ID", `{"code":200,"data":[{"resourceId":"","orderId":"o"}]}`},
		{"missing ID", `{"code":200,"data":[{"orderId":"o"}]}`},
		{"unsafe ID", `{"code":200,"data":[{"resourceId":"a/b"}]}`},
		{"dot ID", `{"code":200,"data":[{"resourceId":".."}]}`},
		{"password ID", `{"code":200,"data":[{"resourceId":"` + testPassword + `"}]}`},
		{"escaped password ID", `{"code":200,"data":[{"resourceId":"` + strings.Replace(testPassword, "C", "\\u0043", 1) + `"}]}`},
		{"password inside ID", `{"code":200,"data":[{"resourceId":"db-` + testPassword + `-x"}]}`},
		{"escaped password in another field", `{"code":200,"message":"` + strings.Replace(testPassword, "C", "\\u0043", 1) + `","data":[{"resourceId":"db-1"}]}`},
		{"bearer inside ID", `{"code":200,"data":[{"resourceId":"x-bearer-synthetic-x"}]}`},
		{"bearer ID", `{"code":200,"data":[{"resourceId":"bearer-synthetic"}]}`},
		{"reflected in order", `{"code":200,"data":[{"resourceId":"db-1","orderId":"` + testPassword + `"}]}`},
		{"reflected in message", `{"code":200,"message":"` + testPassword + `","data":[{"resourceId":"db-1"}]}`},
		{"two rows", `{"code":200,"data":[{"resourceId":"db-1"},{"resourceId":"db-2"}]}`},
		{"no rows", `{"code":200,"data":[]}`},
		{"null data", `{"code":200,"data":null}`},
		{"wrong code", `{"code":201,"data":[{"resourceId":"db-1"}]}`},
		{"missing code", `{"data":[{"resourceId":"db-1"}]}`},
		{"duplicate key", `{"code":200,"data":[{"resourceId":"db-1","resourceId":"db-2"}]}`},
		{"case variant", `{"code":200,"data":[{"ResourceId":"db-1"}]}`},
		{"checkout only", `{"code":200,"data":[{"orderUrl":"https://pay.example/x","orderId":"o"}]}`},
		{"not json", `<html>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &script{create: func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, tc.body) }}
			h := newHarness(t, s.handler())
			out, err := h.client.CreateDatabaseInstance(context.Background(), validCreateInput())
			if !errors.Is(err, ErrNotSettled) || (out != nil && out.Instance.ID != "") || h.posts() != 1 || h.count("GET", pathDetail) != 0 {
				t.Fatalf("out=%v err=%v posts=%d", out, err, h.posts())
			}
			assertNoSecret(t, "error", err, out)
		})
	}
}

func TestCreateWaits(t *testing.T) {
	building := detailBody("BUILDING", nil)
	active := detailBody("ACTIVE", nil)
	cases := []struct {
		name   string
		detail []string
		def    string
		check  func(t *testing.T, h *harness, out *CreateDatabaseInstanceOutput, err error)
		cancel int
	}{
		{name: "active after building", detail: []string{building, detailBody("BUILD", nil), building, active}, check: func(t *testing.T, h *harness, out *CreateDatabaseInstanceOutput, err error) {
			if err != nil || out.Instance.Status != "ACTIVE" || h.count("GET", pathDetail) != 4 || len(h.clock.sleeps) != 3 {
				t.Fatalf("out=%v err=%v reads=%d sleeps=%v", out, err, h.count("GET", pathDetail), h.clock.sleeps)
			}
			for _, d := range h.clock.sleeps {
				if d != 10*time.Second {
					t.Fatalf("sleep %v", d)
				}
			}
		}},
		{name: "error state", detail: []string{building, detailBody("ERROR", nil)}, check: func(t *testing.T, _ *harness, out *CreateDatabaseInstanceOutput, err error) {
			if !errors.Is(err, ErrFailed) || out == nil || !reflect.DeepEqual(out.Instance, Instance{ID: "db-1"}) {
				t.Fatalf("out=%v err=%v", out, err)
			}
		}},
		{name: "unknown state", detail: []string{detailBody("WEIRD", nil)}, check: notSettled},
		{name: "timeout", def: building, check: func(t *testing.T, h *harness, out *CreateDatabaseInstanceOutput, err error) {
			notSettled(t, h, out, err)
			var total time.Duration
			for _, d := range h.clock.sleeps {
				total += d
			}
			if total != 30*time.Minute || h.posts() != 1 {
				t.Fatalf("total %v posts %d", total, h.posts())
			}
		}},
		{name: "read fails", detail: []string{building, "not json"}, check: notSettled},
		{name: "not visible then active", detail: []string{"404", building, active}, check: func(t *testing.T, h *harness, out *CreateDatabaseInstanceOutput, err error) {
			if err != nil || out.Instance.Status != "ACTIVE" || h.count("GET", pathDetail) != 3 || h.posts() != 1 {
				t.Fatalf("out=%v err=%v reads=%d", out, err, h.count("GET", pathDetail))
			}
		}},
		{name: "redirect to a 404", detail: []string{"302"}, check: func(t *testing.T, h *harness, out *CreateDatabaseInstanceOutput, err error) {
			notSettled(t, h, out, err)
			if h.count("GET", "/elsewhere") != 0 {
				t.Fatal("followed the redirect")
			}
		}},
		{name: "never visible", def: "404", check: func(t *testing.T, h *harness, out *CreateDatabaseInstanceOutput, err error) {
			notSettled(t, h, out, err)
			if len(h.clock.sleeps) != 180 || h.posts() != 1 {
				t.Fatalf("sleeps=%d posts=%d", len(h.clock.sleeps), h.posts())
			}
		}},
		{name: "wrong flavor", detail: []string{detailBody("ACTIVE", map[string]any{"quotaPackageId": "999"})}, check: notSettled},
		{name: "wrong engine", detail: []string{detailBody("ACTIVE", map[string]any{"datastoreType": "PostgreSQL"})}, check: notSettled},
		{name: "wrong version", detail: []string{detailBody("ACTIVE", map[string]any{"datastoreVersion": "5.7"})}, check: notSettled},
		{name: "backup on", detail: []string{detailBody("ACTIVE", map[string]any{"backupAuto": true})}, check: notSettled},
		{name: "backup unknown", detail: []string{detailBody("ACTIVE", map[string]any{"backupAuto": nil})}, check: notSettled},
		{name: "public", detail: []string{detailBody("ACTIVE", map[string]any{"publicAccess": true})}, check: notSettled},
		{name: "other id", detail: []string{detailBody("ACTIVE", map[string]any{"id": "db-2"})}, check: notSettled},
		{name: "password reflected", detail: []string{detailBody("ACTIVE", map[string]any{"name": testPassword})}, check: func(t *testing.T, h *harness, out *CreateDatabaseInstanceOutput, err error) {
			notSettled(t, h, out, err)
			assertNoSecret(t, "out", out, err)
		}},
		{name: "cancel", def: building, cancel: 2, check: func(t *testing.T, h *harness, out *CreateDatabaseInstanceOutput, err error) {
			notSettled(t, h, out, err)
			if h.posts() != 1 {
				t.Fatal("resent")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &script{detail: tc.detail, detailDefault: tc.def}
			h := newHarness(t, s.handler())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel > 0 {
				h.clock.onSlp = func(n int) {
					if n == tc.cancel {
						cancel()
					}
				}
			}
			out, err := h.client.CreateDatabaseInstance(ctx, validCreateInput())
			tc.check(t, h, out, err)
		})
	}
}

func notSettled(t *testing.T, _ *harness, out *CreateDatabaseInstanceOutput, err error) {
	t.Helper()
	if !errors.Is(err, ErrNotSettled) || errors.Is(err, ErrFailed) || out == nil || !reflect.DeepEqual(out.Instance, Instance{ID: "db-1"}) {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestCreateConfirmationNeverShowsCredentialsInDetail(t *testing.T) {
	escaped := strings.Replace(testPassword, "C", "\\u0043", 1)
	for name, patch := range map[string]string{
		"bearer in name":           "x-bearer-synthetic-y",
		"password in name":         "x" + testPassword,
		"escaped password in name": "x" + escaped,
	} {
		t.Run(name, func(t *testing.T) {
			body := strings.Replace(detailBody("ACTIVE", nil), `"demo-db"`, `"`+patch+`"`, 1)
			if name == "escaped password in name" {
				body = strings.Replace(detailBody("ACTIVE", nil), `"demo-db"`, `"x`+escaped+`"`, 1)
			}
			h := newHarness(t, (&script{detail: []string{body}}).handler())
			out, err := h.client.CreateDatabaseInstance(context.Background(), validCreateInput())
			notSettled(t, h, out, err)
			text := fmt.Sprintf("%#v %v", out, err)
			if strings.Contains(text, "bearer-synthetic") || strings.Contains(text, testPassword) || strings.Contains(text, "anarycanary") {
				t.Fatalf("leak: %s", text)
			}
		})
	}
}

func TestCreateExpiredCallerContextDoesNotResend(t *testing.T) {
	s := &script{detailDefault: detailBody("BUILDING", nil)}
	h := newHarness(t, s.handler())
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	out, err := h.client.CreateDatabaseInstance(ctx, validCreateInput())
	if out != nil && out.Instance.ID != "" || err == nil || h.posts() != 0 {
		t.Fatalf("out=%v err=%v posts=%d", out, err, h.posts())
	}
}

func TestCreateOutputsAndErrorsNeverShowPassword(t *testing.T) {
	s := &script{
		create: func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, `{"code":200,"data":[{"resourceId":"db-1"}]}`)
		},
		detail: []string{detailBody("ERROR", map[string]any{"name": "demo-db"})},
	}
	h := newHarness(t, s.handler())
	out, err := h.client.CreateDatabaseInstance(context.Background(), validCreateInput())
	assertNoSecret(t, "result", out, err)
	for _, r := range h.log() {
		if r.Method == http.MethodGet && (strings.Contains(r.Path, testPassword) || strings.Contains(r.Query, testPassword)) {
			t.Fatal("password in a read")
		}
	}
}

func TestCreateLogsAndCapturesNeverCarryPassword(t *testing.T) {
	var logs bytes.Buffer
	captured := 0
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := &script{detail: []string{detailBody("ACTIVE", nil)}, create: func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 400, `{"message":"password `+testPassword+`"}`)
	}}
	h := newHarness(t, s.handler(), vngcloud.WithLogger(logger), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captured++ }))
	_, _ = h.client.CreateDatabaseInstance(context.Background(), validCreateInput())
	s.create = nil
	_, _ = h.client.CreateDatabaseInstance(context.Background(), validCreateInput())
	if strings.Contains(logs.String(), testPassword) || logs.Len() == 0 {
		t.Fatalf("logs: %s", logs.String())
	}
	if captured != 0 {
		t.Fatalf("captured %d responses", captured)
	}
}

func TestCreatePasswordRule(t *testing.T) {
	cases := []struct {
		name, password string
		ok             bool
	}{
		{"7 characters", "Abcdef1", false},
		{"8 characters", "Abcdefg1", true},
		{"32 characters", "A" + strings.Repeat("b", 30) + "1", true},
		{"33 characters", "A" + strings.Repeat("b", 31) + "1", false},
		{"starts with digit", "1bcdefgh", false},
		{"starts with symbol", "$bcdefgh", false},
		{"ends with symbol", "Abcdefg_", false},
		{"ends with digit", "Abcdefg9", true},
		{"allowed symbols inside", "A$^_<>bc1", true},
		{"hyphen", "Abcd-efg1", false},
		{"space", "Abcd efg1", false},
		{"non-ascii", "Abcdéfg12", false},
		{"quote", "Abcd'efg1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, (&script{detail: []string{detailBody("ACTIVE", nil)}}).handler())
			in := validCreateInput()
			in.Password = vngcloud.Secret(tc.password)
			_, err := h.client.CreateDatabaseInstance(context.Background(), in)
			if tc.ok {
				if err != nil {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if !errors.Is(err, vngcloud.ErrInvalidInput) || !strings.Contains(err.Error(), "Password") || strings.Contains(err.Error(), tc.password) || len(h.log()) != 0 {
				t.Fatalf("err=%v requests=%d", err, len(h.log()))
			}
		})
	}
}

func TestCreateNameLengthLimit(t *testing.T) {
	for _, name := range []string{"a", strings.Repeat("a", 9), strings.Repeat("a", 10), "a-" + strings.Repeat("b", 18)} {
		h := newHarness(t, (&script{detail: []string{detailBody("ACTIVE", nil)}}).handler())
		in := validCreateInput()
		in.Name = name
		if _, err := h.client.CreateDatabaseInstance(context.Background(), in); err != nil {
			t.Fatalf("%d characters: %v", len(name), err)
		}
	}
}

func TestCreate400EchoingTheRequestNeverLeaksThePassword(t *testing.T) {
	var logs bytes.Buffer
	captured := 0
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	echo := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req any
		_ = json.Unmarshal(body, &req)
		reply, _ := json.Marshal(map[string]any{"code": 400, "message": "in_valid", "fieldError": []any{
			map[string]any{"field": "name", "message": "Name is too long", "rejectedValue": "demo-db"},
			map[string]any{"field": "password", "message": "This password must be right format", "rejectedValue": testPassword},
			map[string]any{"field": "databases[0].name", "message": "bad " + testPassword, "rejectedValue": testPassword},
			map[string]any{"field": testPassword, "message": "x"},
			map[string]any{"field": "a", "message": "1"}, map[string]any{"field": "b", "message": "2"}, map[string]any{"field": "c", "message": "3"}, map[string]any{"field": "d", "message": "4"},
		}, "request": req, "raw": string(body)})
		writeJSON(w, 400, string(reply))
	}
	h := newHarness(t, (&script{create: echo}).handler(), vngcloud.WithLogger(logger), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captured++ }))
	out, err := h.client.CreateDatabaseInstance(context.Background(), validCreateInput())
	var api *vngcloud.APIError
	if out != nil || !errors.As(err, &api) || api.StatusCode != 400 || h.posts() != 1 {
		t.Fatalf("out=%v err=%v", out, err)
	}
	assertNoSecret(t, "error", err, out, api)
	if api.Message != "invalid input rejected by the API: Name, Password, DatabaseName" {
		t.Fatalf("message %q", api.Message)
	}
	for _, want := range []string{"Name", "Password"} {
		if !strings.Contains(api.Message, want) {
			t.Fatalf("message %q lacks %q", api.Message, want)
		}
	}
	if strings.Contains(api.Message, "bad ") || strings.Contains(api.Message, "rejectedValue") || strings.Contains(api.Message, "demo-db") || strings.Contains(api.Message, "too long") || strings.Contains(api.Message, "in_valid") {
		t.Fatalf("message %q carries echoed or excess values", api.Message)
	}
	if strings.Contains(logs.String(), testPassword) || captured != 0 {
		t.Fatalf("logs=%s captured=%d", logs.String(), captured)
	}
}
