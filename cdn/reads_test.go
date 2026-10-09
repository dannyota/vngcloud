package cdn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

// noLeak fails when the model, in any common rendering, holds a value the
// fixture marks as secret or account data.
func noLeak(t *testing.T, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{string(b), fmt.Sprintf("%v", v), fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v)} {
		for _, banned := range []string{"private-key", "<secret", "<account>", "privateKey", "userEmail", "token"} {
			if strings.Contains(text, banned) {
				t.Errorf("rendering holds %q: %s", banned, text)
			}
		}
	}
}

func TestListCertificatesDecodesFixture(t *testing.T) {
	h := newVCDN(t, testKey, reply(200, "application/json", readFixture(t, "certificate-list.json")))
	out, err := h.ListCertificates(context.Background(), &ListCertificatesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(out.Items))
	}
	want := Certificate{
		ID: "cert-1", CommonName: "<hostname>", CName: "<hostname>", Issuer: "<issuer>",
		ValidFrom: "28 Apr 2025 06:53:20 GMT", ExpiresOn: "28 Apr 2026 06:53:20 GMT",
		Status: 1, CDNUsing: 2, CreatedTime: "2025-04-28T10:24:32.000+0000",
	}
	if out.Items[0] != want {
		t.Errorf("item 0 = %+v, want %+v", out.Items[0], want)
	}
	if out.Items[1].ID != "cert-2" || out.Items[1].Status != 0 {
		t.Errorf("item 1 = %+v", out.Items[1])
	}
	noLeak(t, out)
}

func TestListCertificatesEmptyForms(t *testing.T) {
	for _, data := range []string{`[]`, `null`} {
		h := newVCDN(t, testKey, reply(200, "application/json", `{"success":true,"code":200,"message":"ok","data":`+data+`}`))
		out, err := h.ListCertificates(context.Background(), nil)
		if err != nil || len(out.Items) != 0 {
			t.Errorf("data %s: out = %+v, err = %v", data, out, err)
		}
	}
	h := newVCDN(t, testKey, reply(200, "application/json", `{"success":true,"code":200,"data":{"a":1}}`))
	if _, err := h.ListCertificates(context.Background(), nil); apiError(t, err).Code != codeEmptyResponse {
		t.Fatalf("object data err = %v", err)
	}
}

func TestGetCertificateDecodesFixture(t *testing.T) {
	h := newVCDN(t, testKey, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vcdn-api/v1/certificate/detail/cert-1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		reply(200, "application/json", readFixture(t, "certificate-detail.json"))(w, r)
	})
	out, err := h.GetCertificate(context.Background(), &GetCertificateInput{CertificateID: "cert-1"})
	if err != nil {
		t.Fatal(err)
	}
	c := out.Certificate
	if c.ID != "cert-1" || c.CDNUsing != 2 || c.Status != 1 || c.Issuer != "<issuer>" {
		t.Errorf("certificate = %+v", c)
	}
	if !strings.Contains(c.Certificate, "<certificate>") || !strings.Contains(c.CARoot, "<ca-root>") {
		t.Errorf("PEM fields = %q, %q", c.Certificate, c.CARoot)
	}
	noLeak(t, out)
}

func TestGetCertificateRejectsBadInputBeforeAnyRequest(t *testing.T) {
	h := newVCDN(t, testKey, reply(200, "", `{}`))
	for _, id := range []string{"", "..", "a/b", "a?b", "a b", "a%2fb"} {
		_, err := h.GetCertificate(context.Background(), &GetCertificateInput{CertificateID: id})
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("id %q: err = %v, want ErrInvalidInput", id, err)
		}
	}
	if _, err := h.GetCertificate(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Errorf("nil input err = %v", err)
	}
	if h.requests.Load() != 0 {
		t.Fatal("a request was sent")
	}
}

func TestListAPIKeysDecodesFixtureAndMarksCurrent(t *testing.T) {
	h := newVCDN(t, "<secret>", reply(200, "application/json", readFixture(t, "apikey-list.json")))
	out, err := h.ListAPIKeys(context.Background(), &ListAPIKeysInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(out.Items))
	}
	first := out.Items[0]
	wantExpiry := time.Date(2026, 12, 1, 3, 4, 5, 678_000_000, time.UTC)
	if first.ID != 1 || !first.ExpiresAt.Equal(wantExpiry) || first.AllowOriginHeader != "https://<hostname>" {
		t.Errorf("first = %+v", first)
	}
	if !first.CreateTime.Equal(time.Date(2026, 10, 1, 0, 0, 0, 123_000_000, time.UTC)) || !first.UpdateTime.Equal(time.Date(2026, 10, 2, 1, 2, 3, 0, time.UTC)) {
		t.Errorf("times = %v, %v", first.CreateTime, first.UpdateTime)
	}
	if !first.Current || out.Items[1].Current {
		t.Errorf("Current = %v, %v, want true, false", first.Current, out.Items[1].Current)
	}
	noLeak(t, out)
}

func TestListAPIKeysCurrentFalseForOtherKey(t *testing.T) {
	h := newVCDN(t, "<secret>-different", reply(200, "application/json", readFixture(t, "apikey-list.json")))
	out, err := h.ListAPIKeys(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range out.Items {
		if k.Current {
			t.Errorf("key %d is Current, want none", k.ID)
		}
	}
}

func TestListAPIKeysRefusesAnythingButTheEnvelopeList(t *testing.T) {
	bodies := map[string]string{
		"bare list":       `[{"apiKeyId":1,"token":"<secret>"}]`,
		"bare object":     `{"apiKeyId":1,"token":"<secret>"}`,
		"object data":     `{"success":true,"code":200,"data":{"apiKeyId":1}}`,
		"null data":       `{"success":true,"code":200,"data":null}`,
		"string data":     `{"success":true,"code":200,"data":""}`,
		"missing success": `{"code":200,"data":[]}`,
		"bad time":        `{"success":true,"code":200,"data":[{"apiKeyId":1,"expiredDate":"not a time"}]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			h := newVCDN(t, "<secret>", reply(200, "application/json", body))
			_, err := h.ListAPIKeys(context.Background(), nil)
			apiErr := apiError(t, err)
			if apiErr.Code != codeEmptyResponse || strings.Contains(err.Error(), "<secret>") {
				t.Fatalf("err = %v code %q, want EmptyResponse without the token", err, apiErr.Code)
			}
		})
	}
}

func TestListAPIKeysEmptyList(t *testing.T) {
	h := newVCDN(t, "<secret>", reply(200, "application/json", `{"success":true,"code":200,"data":[]}`))
	out, err := h.ListAPIKeys(context.Background(), nil)
	if err != nil || len(out.Items) != 0 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}
