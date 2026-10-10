package volume

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"danny.vn/vngcloud"
)

func TestQuoteCreateVolumeSendsEncryptionType(t *testing.T) {
	var info map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, _ = decodeVolumeBody(t, r)["resourceInfo"].(map[string]any)
		_, _ = w.Write([]byte(quoteVolumeFixture))
	}))
	in := validCreateVolumeInput()
	in.EncryptionTypeID = "aes-xts-plain64_256"
	if _, err := c.QuoteCreateVolume(context.Background(), in); err != nil {
		t.Fatalf("QuoteCreateVolume() error = %v", err)
	}
	want := map[string]any{
		"size": float64(10), "volumeTypeId": "voltype-1", "zoneId": "zone-1",
		"encryptionType": "aes-xts-plain64_256", "period": float64(1), "isPoc": false,
	}
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("resourceInfo = %v, want %v", info, want)
	}
}

func TestCreateVolumeSendsEncryptionType(t *testing.T) {
	var createBody, quoteInfo map[string]any
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/price" {
			quoteInfo, _ = decodeVolumeBody(t, r)["resourceInfo"].(map[string]any)
		}
		routeVolumeWriteRequest(t, w, r, emptyListVolumesPage,
			func(w http.ResponseWriter, r *http.Request) {
				createBody = decodeVolumeBody(t, r)
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1"}}`))
			},
			staticGet(`{"data":{"uuid":"volume-1","name":"vol-1","status":"AVAILABLE"}}`),
		)
	})))
	in := validCreateVolumeInput()
	in.EncryptionTypeID = "aes-xts-plain64_128"
	in.MaxPrice = 32000
	if _, err := c.CreateVolume(context.Background(), in); err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if createBody["encryptionType"] != "aes-xts-plain64_128" {
		t.Fatalf("create encryptionType = %v, want aes-xts-plain64_128", createBody["encryptionType"])
	}
	if quoteInfo["encryptionType"] != "aes-xts-plain64_128" {
		t.Fatalf("guard quote encryptionType = %v, want aes-xts-plain64_128", quoteInfo["encryptionType"])
	}
	if _, ok := createBody["encryptionVolume"]; ok {
		t.Fatalf("create body has encryptionVolume: %v", createBody)
	}
}

func TestCreateVolumeOmitsEncryptionTypeWhenUnset(t *testing.T) {
	body, err := buildCreateVolumeBody("op", validCreateVolumeInput())
	if err != nil {
		t.Fatal(err)
	}
	info, err := buildVolumeQuoteInfo("op", validCreateVolumeInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := info["encryptionType"]; ok {
		t.Fatalf("quote info has encryptionType: %v", info)
	}
	if body.EncryptionType != "" {
		t.Fatalf("create body EncryptionType = %q, want empty", body.EncryptionType)
	}
}

func TestCreateVolumeRejectsBadEncryptionTypeID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	for _, bad := range []string{" ", "..", ".", "a/b", "a?b", "a\nb"} {
		in := validCreateVolumeInput()
		in.EncryptionTypeID = bad
		if _, err := c.QuoteCreateVolume(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("quote EncryptionTypeID=%q err = %v, want ErrInvalidInput", bad, err)
		}
		if _, err := c.CreateVolume(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("create EncryptionTypeID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}
