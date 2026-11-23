package network

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestEndpointDeleteRefusesCaseAliasedAttachmentFields(t *testing.T) {
	base := epRow(epID, "demo", "ACTIVE", epSubnet)
	for name, row := range map[string]string{
		"resource alias":   strings.Replace(base, `"category":{}`, `"category":{},"EndpointResource":{"a":1}`, 1),
		"domains alias":    strings.Replace(base, `"category":{}`, `"category":{},"EndpointDomains":["a.example"]`, 1),
		"domains alias up": strings.Replace(base, `"category":{}`, `"category":{},"ENDPOINTDOMAINS":[]`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			f := epDeleteFake(row)
			h := newEPHarness(t, f)
			out, err := h.c.DeleteEndpoint(context.Background(), epDeleteInput())
			if err == nil || out != nil || f.count(http.MethodDelete, "/"+epID) != 0 {
				t.Fatalf("out=%v err=%v", out, err)
			}
		})
	}
}

func TestEndpointInventoryRequiresCanonicalIDs(t *testing.T) {
	upper := strings.ToUpper(strings.TrimPrefix(epOtherID, "enp-"))
	for name, row := range map[string]string{
		"empty vpcId":       strings.Replace(epRow(epOtherID, "demo", "ACTIVE", epSubnet), `"vpcId":"`+epVPC+`"`, `"vpcId":""`, 1),
		"malformed vpcId":   strings.Replace(epRow(epOtherID, "demo", "ACTIVE", epSubnet), `"vpcId":"`+epVPC+`"`, `"vpcId":"vpc-1"`, 1),
		"uppercase row id":  epRow("enp-"+upper, "other", "ACTIVE", epSubnet),
		"uppercase subnet":  epRow(epOtherID, "other", "ACTIVE", strings.ToUpper(epSubnet)),
		"malformed package": strings.Replace(epRow(epOtherID, "other", "ACTIVE", epSubnet), `"packageId":{"uuid":"`+epPackage, `"packageId":{"uuid":"pk`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			f := newEPFake()
			f.list = func(int) string { return epList(row) }
			h := newEPHarness(t, f)
			out, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
			if err == nil || out != nil || f.count(http.MethodPost, epBase) != 0 {
				t.Fatalf("out=%v err=%v posts=%d", out, err, f.count(http.MethodPost, epBase))
			}
		})
	}
	for _, in := range []*CreateEndpointInput{
		{Name: "demo", VPCID: strings.ToUpper(epVPC), SubnetID: epSubnet},
		{Name: "demo", VPCID: epVPC, SubnetID: "sub-0000000A-0000-0000-0000-00000000000a"},
	} {
		f := newEPFake()
		h := newEPHarness(t, f)
		if _, err := h.c.CreateEndpoint(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) || f.total() != 0 {
			t.Fatalf("err=%v requests=%d", err, f.total())
		}
	}
}

func TestEndpointDeleteConfirmationRefusesCaseVariantOfID(t *testing.T) {
	row := epRow(epID, "demo", "ACTIVE", epSubnet)
	variant := epRow("enp-"+strings.ToUpper(strings.TrimPrefix(epID, "enp-")), "demo", "ACTIVE", epSubnet)
	f := newEPFake()
	deleted := false
	f.onDelete = func() { deleted = true }
	f.list = func(int) string {
		if !deleted {
			return epList(row)
		}
		return epList(variant)
	}
	h := newEPHarness(t, f)
	out, err := h.c.DeleteEndpoint(context.Background(), epDeleteInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Deleted {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestEndpointPagingMustMatchSizeAndTotals(t *testing.T) {
	row := func(i int) string {
		return epRow("enp-00000000-0000-0000-0000-"+pad12(i), "n"+itoaT(i), "ACTIVE", epSubnet)
	}
	envelope := func(page, size, pages, total int, rows ...string) string {
		return `{"success":true,"page":` + itoaT(page) + `,"size":` + itoaT(size) + `,"totalPage":` + itoaT(pages) + `,"total":` + itoaT(total) + `,"data":[` + strings.Join(rows, ",") + `]}`
	}
	for _, tc := range []struct {
		name string
		list func(n int) string
		ok   bool
	}{
		{"exact pages", func(n int) string {
			if n == 1 {
				return envelope(1, 2, 2, 3, row(1), row(2))
			}
			return envelope(2, 2, 2, 3, row(3))
		}, true},
		{"short first page", func(n int) string {
			if n == 1 {
				return envelope(1, 2, 2, 3, row(1))
			}
			return envelope(2, 2, 2, 3, row(2), row(3))
		}, false},
		{"size larger than rows with two pages", func(n int) string {
			if n == 1 {
				return envelope(1, 1000, 2, 2, row(1))
			}
			return envelope(2, 1000, 2, 2, row(2))
		}, false},
		{"page count wrong", func(int) string { return envelope(1, 2, 2, 2, row(1), row(2)) }, false},
		{"last page too long", func(n int) string {
			if n == 1 {
				return envelope(1, 2, 2, 3, row(1), row(2))
			}
			return envelope(2, 2, 2, 3, row(3), row(4))
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEPFake()
			f.list = tc.list
			h := newEPHarness(t, f)
			_, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
			if tc.ok != errors.Is(err, ErrNotSettled) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestEndpointRefusesCredentialInRequest(t *testing.T) {
	f := newEPFake()
	h := newEPHarness(t, f)
	in := epCreateInput()
	in.Name = "x-" + epBearer
	for label, call := range map[string]func() error{
		"create": func() error { _, err := h.c.CreateEndpoint(context.Background(), in); return err },
		"quote":  func() error { _, err := h.c.QuoteCreateEndpoint(context.Background(), in); return err },
	} {
		err := call()
		if err == nil || strings.Contains(err.Error(), epBearer) {
			t.Fatalf("%s err=%v", label, err)
		}
	}
	if f.count(http.MethodPost, "/price") != 0 || f.count(http.MethodPost, epBase) != 0 {
		t.Fatal("request carried the credential")
	}
	bad := epCreateInput()
	bad.Name = "a\xffb"
	f = newEPFake()
	h = newEPHarness(t, f)
	if _, err := h.c.CreateEndpoint(context.Background(), bad); !errors.Is(err, vngcloud.ErrInvalidInput) || f.total() != 0 {
		t.Fatalf("err=%v requests=%d", err, f.total())
	}
}

func TestEndpointRequiresVNDPackage(t *testing.T) {
	for name, repl := range map[string]string{
		"usd":     `"currencyUnit":"USD"`,
		"missing": `"currencyUnitX":"VND"`,
		"null":    `"currencyUnit":null`,
	} {
		t.Run(name, func(t *testing.T) {
			f := epCreateFake()
			f.packages = strings.Replace(f.packages, `"currencyUnit":"VND"`, repl, 1)
			h := newEPHarness(t, f)
			out, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
			if err == nil || out != nil || f.count(http.MethodPost, epBase) != 0 {
				t.Fatalf("out=%v err=%v", out, err)
			}
		})
	}
}

func TestEndpointIgnoresCachedProject(t *testing.T) {
	f := newEPFake()
	h := newEPHarness(t, f)
	if _, err := h.c.c.RequireProjectID(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.projects = `{"projects":[{"projectId":"` + epProject + `"},{"projectId":"pro-00000000-0000-0000-0000-0000000000ff"}]}`
	if _, err := h.c.QuoteCreateEndpoint(context.Background(), epCreateInput()); !errors.Is(err, vngcloud.ErrProjectAmbiguous) {
		t.Fatalf("err=%v", err)
	}
}

func TestEndpointClassifiesByObservedStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   error
	}{
		{401, `{"code":"InvalidResponse","success":false}`, vngcloud.ErrAuth},
		{403, `{"code":"InvalidResponse"}`, vngcloud.ErrPermission},
		{400, `{"success":fal`, nil},
		{422, `{"a":`, nil},
	} {
		f := epCreateFake()
		f.createStatus, f.createBody = tc.status, tc.body
		h := newEPHarness(t, f)
		out, err := h.c.CreateEndpoint(context.Background(), epCreateInput())
		if out != nil || err == nil || errors.Is(err, ErrNotSettled) || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Fatalf("status %d out=%v err=%v", tc.status, out, err)
		}
	}
	f := epCreateFake()
	f.createStatus, f.createBody = 408, `{"code":"x"}`
	h := newEPHarness(t, f)
	if _, err := h.c.CreateEndpoint(context.Background(), epCreateInput()); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("408 err=%v", err)
	}
}

func TestEndpointConfirmation404IsNotProvenAbsence(t *testing.T) {
	row := epRow(epID, "demo", "ACTIVE", epSubnet)
	f := newEPFake()
	deleted := false
	f.onDelete = func() { deleted = true }
	f.list = func(int) string {
		if !deleted {
			return epList(row)
		}
		return `{"success":false}`
	}
	f.listStatus = func(int) int {
		if !deleted {
			return 200
		}
		return 404
	}
	h := newEPHarness(t, f)
	out, err := h.c.DeleteEndpoint(context.Background(), epDeleteInput())
	if !errors.Is(err, ErrNotSettled) || errors.Is(err, vngcloud.ErrNotFound) || vngcloud.IsNotFound(err) || out == nil || out.Deleted {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestAppendDistinctCredentials(t *testing.T) {
	got := appendDistinct([]string{"a"}, "a", "b", "", "b", "c")
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("%v", got)
	}
}

func TestEndpointConfirmationWindowStartsAfterDispatch(t *testing.T) {
	var h *epHarness
	f := newEPFake()
	created := false
	f.onCreate = func() { created = true; h.clock = h.clock.Add(14 * time.Minute) }
	f.list = func(int) string {
		if !created {
			return epList()
		}
		return epList(epRow(epID, "demo", "PROVISIONING", epSubnet))
	}
	h = newEPHarness(t, f)
	if _, err := h.c.CreateEndpoint(context.Background(), epCreateInput()); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err=%v", err)
	}
	var total time.Duration
	for _, d := range h.slept {
		total += d
	}
	if total != 15*time.Minute {
		t.Fatalf("waited %v", total)
	}

	row := epRow(epID, "demo", "ACTIVE", epSubnet)
	f = newEPFake()
	f.list = func(int) string { return epList(row) }
	h = newEPHarness(t, f)
	f.onDelete = func() { h.clock = h.clock.Add(9 * time.Minute) }
	if _, err := h.c.DeleteEndpoint(context.Background(), epDeleteInput()); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err=%v", err)
	}
	total = 0
	for _, d := range h.slept {
		total += d
	}
	if total != 10*time.Minute {
		t.Fatalf("delete waited %v", total)
	}
}

type cancelBody struct {
	cancel context.CancelFunc
	ctx    context.Context
}

func (b *cancelBody) Read([]byte) (int, error) {
	b.cancel()
	return 0, b.ctx.Err()
}
func (*cancelBody) Close() error { return nil }

func cancelCreateBody(f *epFake, cancel context.CancelFunc, ctx context.Context) {
	f.rewrap = func(r *http.Request, resp *http.Response) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, epBase) {
			resp.Body = &cancelBody{cancel: cancel, ctx: ctx}
			resp.ContentLength = -1
		}
	}
}

func TestEndpointCreateCancelDuringReplyBodyKeepsContextCause(t *testing.T) {
	f := epCreateFake()
	ctx, cancel := context.WithCancel(context.Background())
	cancelCreateBody(f, cancel, ctx)
	h := newEPHarness(t, f)
	out, err := h.c.CreateEndpoint(ctx, epCreateInput())
	if !errors.Is(err, ErrNotSettled) || !errors.Is(err, context.Canceled) || out == nil || out.EndpointID != "" {
		t.Fatalf("out=%#v err=%v", out, err)
	}
	if f.count(http.MethodPost, epBase) != 1 {
		t.Fatal("create resent")
	}
}

func TestEndpointCreateRefusalCancelledDuringBodyStaysDefinitive(t *testing.T) {
	f := epCreateFake()
	f.createStatus = 403
	ctx, cancel := context.WithCancel(context.Background())
	cancelCreateBody(f, cancel, ctx)
	h := newEPHarness(t, f)
	out, err := h.c.CreateEndpoint(ctx, epCreateInput())
	if out != nil || errors.Is(err, ErrNotSettled) || !errors.Is(err, vngcloud.ErrPermission) || !errors.Is(err, context.Canceled) {
		t.Fatalf("out=%#v err=%v", out, err)
	}
	f = epCreateFake()
	f.createStatus = 408
	ctx, cancel = context.WithCancel(context.Background())
	cancelCreateBody(f, cancel, ctx)
	h = newEPHarness(t, f)
	if _, err = h.c.CreateEndpoint(ctx, epCreateInput()); !errors.Is(err, ErrNotSettled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("408 err=%v", err)
	}
}
