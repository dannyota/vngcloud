package cdn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestListWebAcceleratorsDecodesFixture(t *testing.T) {
	h := newVCDN(t, testKey, reply(200, "application/json", readFixture(t, "webaccelerator-list.json")))
	out, err := h.ListWebAccelerators(context.Background(), &ListWebAcceleratorsInput{})
	if err != nil {
		t.Fatal(err)
	}
	want := WebAcceleratorSummary{
		CDNID: "<cdn-id>", DomainName: "<domain>", CDNDomain: "<cdn-domain>",
		CNames: []string{}, Status: StatusActive, StatusName: "ACTIVE",
	}
	if len(out.Items) != 1 || !reflect.DeepEqual(out.Items[0], want) {
		t.Fatalf("items = %+v, want [%+v]", out.Items, want)
	}
	text, _ := json.Marshal(out)
	if strings.Contains(string(text), "user-id") || strings.Contains(string(text), "userUuid") {
		t.Fatalf("output holds account data: %s", text)
	}
}

func TestListWebAcceleratorsEmptyForms(t *testing.T) {
	for _, data := range []string{`[]`, `null`} {
		h := newVCDN(t, testKey, reply(200, "application/json", `{"success":true,"code":200,"message":"Get CDN data successful.","data":`+data+`}`))
		out, err := h.ListWebAccelerators(context.Background(), nil)
		if err != nil || len(out.Items) != 0 {
			t.Errorf("data %s: out = %+v err = %v", data, out, err)
		}
	}
	h := newVCDN(t, testKey, reply(200, "application/json", `{"success":true,"code":200,"data":{"a":1}}`))
	if _, err := h.ListWebAccelerators(context.Background(), nil); apiError(t, err).Code != codeEmptyResponse {
		t.Fatalf("err = %v, want EmptyResponse", err)
	}
}

func TestListWebAcceleratorsSendsGETWithKey(t *testing.T) {
	h := newVCDN(t, testKey, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/vcdn-api/v1/cdn/list" || r.Header.Get("Authorization") != "Bearer "+testKey || r.Header.Get("Origin") != "" {
			t.Errorf("request = %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		jsonReply(w, `{"success":true,"code":200,"data":[]}`)
	})
	if _, err := h.ListWebAccelerators(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestGetWebAcceleratorDecodesFixture(t *testing.T) {
	h := newVCDN(t, testKey, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/vcdn-api/v1/cdn/detail/"+cdnID {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		jsonReply(w, readFixture(t, "webaccelerator-detail.json"))
	})
	out, err := h.GetWebAccelerator(context.Background(), &GetWebAcceleratorInput{CDNID: cdnID})
	if err != nil {
		t.Fatal(err)
	}
	wa := out.WebAccelerator
	if wa.CDNID != "<cdn-id>" || wa.Type != "webacc" || wa.DomainName != "<domain>" || wa.CDNDomain != "<cdn-domain>" ||
		wa.Status != StatusActive || wa.StatusName != "ACTIVE" || wa.CertificateID != "default" || wa.LBType != "rr" ||
		wa.OriginHostHeader != "" || wa.UseSSL || wa.UseSmallFile || wa.EnableGzip {
		t.Fatalf("wa = %+v", wa)
	}
	if !reflect.DeepEqual(wa.FailOverErrorCodes, []string{"500", "502", "503", "504"}) || len(wa.CNames) != 0 {
		t.Fatalf("lists = %v %v", wa.FailOverErrorCodes, wa.CNames)
	}
	if !reflect.DeepEqual(wa.Upstreams, []Upstream{{ID: "upstream-1", Priority: 10, IPAddress: "<ip>", Status: 1}}) {
		t.Fatalf("upstreams = %+v", wa.Upstreams)
	}
	if len(wa.DefaultRuleActions) != 12 {
		t.Fatalf("actions = %d, want 12", len(wa.DefaultRuleActions))
	}
	values := map[string]string{}
	for _, a := range wa.DefaultRuleActions {
		if a.ID == "" || a.Order != 0 {
			t.Errorf("action = %+v", a)
		}
		values[a.Name] = a.Value
	}
	if values["hsts"] != `{"hsts":"off","preload":"off","includeSubDomains":"off","maxAge":"0m"}` || values["minify"] != `["js","css","html"]` ||
		values["minimumTls"] != "TLS 1.2" || values["serverCache"] != "2w" {
		t.Fatalf("values = %v", values)
	}
	if string(wa.PageRules) != `[]` || !strings.Contains(string(wa.AdvancedRule), `"Default Rule"`) {
		t.Fatalf("rules = %s %s", wa.PageRules, wa.AdvancedRule)
	}
	text, _ := json.Marshal(out)
	for _, banned := range []string{"user-id", "userUuid", "customerId"} {
		if strings.Contains(string(text), banned) {
			t.Fatalf("output holds %q: %s", banned, text)
		}
	}
}

func TestStatusNamesAndConstants(t *testing.T) {
	for status, want := range map[int]string{0: "DISABLED", 1: "ACTIVE", 3: "DEPLOYING", 4: "DELETING", 5: "DISABLING", 2: "UNKNOWN(2)", 6: "UNKNOWN(6)", -1: "UNKNOWN(-1)"} {
		if got := StatusName(status); got != want {
			t.Errorf("StatusName(%d) = %q, want %q", status, got, want)
		}
	}
	if StatusDisabled != 0 || StatusActive != 1 || StatusDeploying != 3 || StatusDeleting != 4 || StatusDisabling != 5 {
		t.Fatal("status constants changed")
	}
	for _, name := range []string{"webaccelerator-detail-deploying.json", "webaccelerator-detail-disabled.json"} {
		h := newVCDN(t, testKey, reply(200, "application/json", readFixture(t, name)))
		out, err := h.GetWebAccelerator(context.Background(), &GetWebAcceleratorInput{CDNID: cdnID})
		if err != nil || out.WebAccelerator.StatusName != StatusName(out.WebAccelerator.Status) {
			t.Fatalf("%s: out = %+v err = %v", name, out, err)
		}
	}
}

func TestGetWebAcceleratorNotFoundForms(t *testing.T) {
	for name, body := range map[string]string{
		"empty data":  `{"success":false,"code":500,"message":null,"data":""}`,
		"null data":   `{"success":false,"code":500,"message":null,"data":null}`,
		"not found":   `{"success":false,"code":null,"message":"Not found cdn","data":""}`,
		"with a text": `{"success":false,"code":500,"message":"Not found cdn x","data":""}`,
	} {
		h := newVCDN(t, testKey, reply(200, "application/json", body))
		_, err := h.GetWebAccelerator(context.Background(), &GetWebAcceleratorInput{CDNID: cdnID})
		if !errors.Is(err, vngcloud.ErrNotFound) || !vngcloud.IsNotFound(err) || apiError(t, err).Code != "NotFound" {
			t.Errorf("%s: err = %v", name, err)
		}
		if h.requests.Load() != 1 {
			t.Errorf("%s: requests = %d, want 1", name, h.requests.Load())
		}
	}
}

func TestGetWebAcceleratorBadData(t *testing.T) {
	for _, data := range []string{`[]`, `"text"`, `5`} {
		h := newVCDN(t, testKey, reply(200, "application/json", `{"success":true,"code":200,"data":`+data+`}`))
		_, err := h.GetWebAccelerator(context.Background(), &GetWebAcceleratorInput{CDNID: cdnID})
		if apiError(t, err).Code != codeEmptyResponse {
			t.Errorf("data %s: err = %v", data, err)
		}
	}
}

func TestWebAcceleratorReadsNeedAKeyAndAnID(t *testing.T) {
	h := newVCDN(t, "", reply(200, "application/json", `{}`))
	if _, err := h.ListWebAccelerators(context.Background(), nil); !errors.Is(err, ErrNoAPIKey) {
		t.Errorf("list err = %v", err)
	}
	if _, err := h.GetWebAccelerator(context.Background(), &GetWebAcceleratorInput{CDNID: cdnID}); !errors.Is(err, ErrNoAPIKey) {
		t.Errorf("get err = %v", err)
	}
	if _, err := h.GetWebAccelerator(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Errorf("get nil err = %v", err)
	}
}
