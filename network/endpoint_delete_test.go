package network

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
)

func epDeleteInput() *DeleteEndpointInput {
	return &DeleteEndpointInput{EndpointID: epID, VPCID: epVPC}
}

func epDeleteFake(row string) *epFake {
	f := newEPFake()
	deleted := false
	f.onDelete = func() { deleted = true }
	f.list = func(n int) string {
		if !deleted {
			return epList(row)
		}
		if n < 4 {
			return epList(strings.Replace(row, `"status":"ACTIVE"`, `"status":"DELETING"`, 1))
		}
		return epList()
	}
	return f
}

func TestEndpointDeleteHappyPath(t *testing.T) {
	f := epDeleteFake(epRow(epID, "demo", "ACTIVE", epSubnet))
	h := newEPHarness(t, f)
	out, err := h.c.DeleteEndpoint(context.Background(), epDeleteInput())
	if err != nil || out == nil || out.EndpointID != epID || !out.Deleted {
		t.Fatalf("out=%#v err=%v", out, err)
	}
	req := f.find(http.MethodDelete, "/"+epID)
	if req == nil || f.count(http.MethodDelete, "/"+epID) != 1 || req.Path != epBase+"/"+epID {
		t.Fatalf("delete request %#v", req)
	}
	want := `{"endpointUuid":"` + epID + `","vpcUuid":"` + epVPC + `","endpointServiceUuid":"` + epService + `","regionUuid":"` + epRegion + `","projectUuid":"` + epProject + `"}`
	if !reflect.DeepEqual(decodeMap(t, req.Body), decodeMap(t, want)) {
		t.Fatalf("body %s", req.Body)
	}
	if len(h.slept) == 0 {
		t.Fatal("no polling")
	}
	for _, d := range h.slept {
		if d != 5*time.Second {
			t.Fatalf("interval %v", d)
		}
	}
}

func TestEndpointDeletePreflightRefusals(t *testing.T) {
	r := strings.NewReplacer
	base := epRow(epID, "demo", "ACTIVE", epSubnet)
	for _, tc := range []struct {
		name string
		rows []string
		want error
	}{
		{"absent", nil, core.ErrNotFound},
		{"other id only", []string{epRow(epOtherID, "x", "ACTIVE", epSubnet)}, core.ErrNotFound},
		{"other vpc", []string{r(`"vpcId":"`+epVPC, `"vpcId":"net-00000000-0000-0000-0000-0000000000ff`).Replace(base)}, core.ErrInvalidInput},
		{"other service", []string{r(`"endpointServiceId":"`+epService, `"endpointServiceId":"00000000-0000-0000-0000-0000000000a2`).Replace(base)}, core.ErrInvalidInput},
		{"other package", []string{r(`"packageId":{"uuid":"`+epPackage, `"packageId":{"uuid":"00000000-0000-0000-0000-0000000000b2`).Replace(base)}, core.ErrInvalidInput},
		{"other zone", []string{r(`"zoneUuid":"HCM03-1C"`, `"zoneUuid":"HCM03-1B"`).Replace(base)}, core.ErrInvalidInput},
		{"provisioning", []string{r(`"status":"ACTIVE"`, `"status":"PROVISIONING"`).Replace(base)}, core.ErrInvalidInput},
		{"deleting", []string{r(`"status":"ACTIVE"`, `"status":"DELETING"`).Replace(base)}, core.ErrInvalidInput},
		{"resource set", []string{r(`"endpointResource":null`, `"endpointResource":{"id":"x"}`).Replace(base)}, errInUse},
		{"resource empty object", []string{r(`"endpointResource":null`, `"endpointResource":{}`).Replace(base)}, errInUse},
		{"resource missing", []string{r(`"endpointResource":null,`, ``).Replace(base)}, nil},
		{"domains set", []string{r(`"endpointDomains":[]`, `"endpointDomains":["a.example"]`).Replace(base)}, errInUse},
		{"domains missing", []string{r(`,"endpointDomains":[]`, ``).Replace(base)}, nil},
		{"domains null", []string{r(`"endpointDomains":[]`, `"endpointDomains":null`).Replace(base)}, nil},
		{"domains object", []string{r(`"endpointDomains":[]`, `"endpointDomains":{}`).Replace(base)}, nil},
		{"duplicate rows", []string{base, base}, core.ErrInvalidInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEPFake()
			f.list = func(int) string { return epList(tc.rows...) }
			h := newEPHarness(t, f)
			out, err := h.c.DeleteEndpoint(context.Background(), epDeleteInput())
			if out != nil || err == nil || errors.Is(err, ErrNotSettled) {
				t.Fatalf("out=%v err=%v", out, err)
			}
			if f.count(http.MethodDelete, "/"+epID) != 0 {
				t.Fatal("deleted after refusal")
			}
			if tc.want != nil && tc.name != "duplicate rows" && !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}

var errInUse = ErrInUse

func TestEndpointDeleteRejectsInputBeforeRequests(t *testing.T) {
	for _, in := range []*DeleteEndpointInput{
		{EndpointID: "", VPCID: epVPC},
		{EndpointID: "enp-1", VPCID: epVPC},
		{EndpointID: epID, VPCID: "vpc"},
		{EndpointID: "../" + epID, VPCID: epVPC},
	} {
		f := newEPFake()
		h := newEPHarness(t, f)
		if _, err := h.c.DeleteEndpoint(context.Background(), in); !errors.Is(err, core.ErrInvalidInput) || f.total() != 0 {
			t.Fatalf("in=%v err=%v requests=%d", in, err, f.total())
		}
	}
}

func TestEndpointDeleteResponses(t *testing.T) {
	row := epRow(epID, "demo", "ACTIVE", epSubnet)
	for _, status := range []int{400, 401, 403, 404, 409, 429} {
		f := epDeleteFake(row)
		f.deleteStatus, f.deleteBody = status, `{"message":"server-canary"}`
		h := newEPHarness(t, f)
		out, err := h.c.DeleteEndpoint(context.Background(), epDeleteInput())
		if out != nil || err == nil || errors.Is(err, ErrNotSettled) || strings.Contains(err.Error(), "server-canary") || f.count(http.MethodDelete, "/"+epID) != 1 {
			t.Fatalf("status %d out=%v err=%v", status, out, err)
		}
	}
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"408", 408, `{}`},
		{"500", 500, `{"message":"server-canary"}`},
		{"201", 201, `{"code":0,"success":true}`},
		{"204", 204, ``},
		{"success false", 200, `{"code":0,"success":false}`},
		{"code nonzero", 200, `{"code":3,"success":true}`},
		{"no success", 200, `{"code":0}`},
		{"not json", 200, `ok`},
		{"duplicate keys", 200, `{"code":0,"code":0,"success":true}`},
		{"reflects bearer", 200, `{"code":0,"success":true,"message":"` + epBearer + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := epDeleteFake(row)
			f.deleteStatus, f.deleteBody = tc.status, tc.body
			f.list = func(int) string { return epList(row) }
			h := newEPHarness(t, f)
			out, err := h.c.DeleteEndpoint(context.Background(), epDeleteInput())
			if !errors.Is(err, ErrNotSettled) || out == nil || out.Deleted || out.EndpointID != epID {
				t.Fatalf("out=%#v err=%v", out, err)
			}
			if strings.Contains(err.Error(), "server-canary") || strings.Contains(err.Error(), epBearer) {
				t.Fatalf("leak %v", err)
			}
			if f.count(http.MethodDelete, "/"+epID) != 1 {
				t.Fatal("resent")
			}
		})
	}
}

func TestEndpointDeleteConfirmation(t *testing.T) {
	row := epRow(epID, "demo", "ACTIVE", epSubnet)
	for _, tc := range []struct {
		name    string
		after   func(n int) string
		deleted bool
	}{
		{"absent with data", func(int) string { return epList(epRow(epOtherID, "x", "ACTIVE", epSubnet)) }, true},
		{"empty list", func(int) string { return epList() }, true},
		{"no data total zero", func(int) string { return `{"success":true,"page":1,"size":1000,"totalPage":0,"total":0}` }, true},
		{"no data total nonzero", func(int) string { return `{"success":true,"page":1,"size":1000,"totalPage":1,"total":1}` }, false},
		{"no data no totals", func(int) string { return `{"success":true}` }, false},
		{"still active", func(int) string { return epList(row) }, false},
		{"still deleting", func(int) string { return epList(strings.Replace(row, `"status":"ACTIVE"`, `"status":"DELETING"`, 1)) }, false},
		{"error state", func(int) string { return epList(strings.Replace(row, `"status":"ACTIVE"`, `"status":"ERROR"`, 1)) }, false},
		{"inconsistent", func(int) string {
			return `{"success":true,"page":1,"size":1000,"totalPage":1,"total":2,"data":[` + epRow(epOtherID, "x", "ACTIVE", epSubnet) + `]}`
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEPFake()
			deleted := false
			f.onDelete = func() { deleted = true }
			f.list = func(n int) string {
				if !deleted {
					return epList(row)
				}
				return tc.after(n)
			}
			h := newEPHarness(t, f)
			out, err := h.c.DeleteEndpoint(context.Background(), epDeleteInput())
			if tc.deleted {
				if err != nil || !out.Deleted {
					t.Fatalf("out=%v err=%v", out, err)
				}
				return
			}
			if !errors.Is(err, ErrNotSettled) || out == nil || out.Deleted || out.EndpointID != epID {
				t.Fatalf("out=%#v err=%v", out, err)
			}
		})
	}
}

func TestEndpointDeleteWaitBoundAndCancellation(t *testing.T) {
	row := epRow(epID, "demo", "ACTIVE", epSubnet)
	f := epDeleteFake(row)
	f.list = func(int) string { return epList(row) }
	h := newEPHarness(t, f)
	out, err := h.c.DeleteEndpoint(context.Background(), epDeleteInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Deleted {
		t.Fatalf("out=%v err=%v", out, err)
	}
	var total time.Duration
	for _, d := range h.slept {
		total += d
	}
	if total != 10*time.Minute {
		t.Fatalf("waited %v", total)
	}

	f = newEPFake()
	deleted := false
	f.onDelete = func() { deleted = true }
	f.list = func(int) string {
		if !deleted {
			return epList(row)
		}
		return epList(strings.Replace(row, `"status":"ACTIVE"`, `"status":"DELETING"`, 1))
	}
	h = newEPHarness(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	f.listDelay = func(n int) {
		if n == 3 {
			cancel()
		}
	}
	out, err = h.c.DeleteEndpoint(ctx, epDeleteInput())
	if !errors.Is(err, ErrNotSettled) || !errors.Is(err, context.Canceled) || out == nil || out.Deleted {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestEndpointDeleteNoLateSuccessAfterDeadline(t *testing.T) {
	row := epRow(epID, "demo", "ACTIVE", epSubnet)
	f := newEPFake()
	deleted := false
	f.onDelete = func() { deleted = true }
	var h *epHarness
	f.list = func(int) string {
		if !deleted {
			return epList(row)
		}
		h.clock = h.clock.Add(11 * time.Minute)
		return epList()
	}
	h = newEPHarness(t, f)
	out, err := h.c.DeleteEndpoint(context.Background(), epDeleteInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Deleted {
		t.Fatalf("out=%v err=%v", out, err)
	}
}
