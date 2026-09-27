package volume

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
)

const quoteVolumeFixture = `{"optimumPrice":32000,"originalPrice":32000,"discountPrice":0,"discountPercent":0,"propertiesPrice":[{"name":"Volume","description":"","optimumPrice":32000,"monthlyPrice":32000,"currentPrice":null,"discountPercent":0}]}`

func TestQuoteCreateVolumeSendsCreateBody(t *testing.T) {
	var gotPath string
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatalf("decode body: %v, raw = %s", err, data)
		}
		_, _ = w.Write([]byte(quoteVolumeFixture))
	}))

	out, err := c.QuoteCreateVolume(context.Background(), &CreateVolumeInput{
		Name: "vol-1", ZoneID: "zone-1", Size: 10, VolumeTypeID: "voltype-1",
	})
	if err != nil {
		t.Fatalf("QuoteCreateVolume() error = %v", err)
	}
	if gotPath != "/v1/price" {
		t.Fatalf("path = %s, want /v1/price", gotPath)
	}
	if body["resourceType"] != "volume" || body["action"] != "create" {
		t.Fatalf("unexpected resourceType/action: %+v", body)
	}
	info, ok := body["resourceInfo"].(map[string]any)
	if !ok {
		t.Fatalf("resourceInfo missing or wrong type: %+v", body)
	}
	want := map[string]any{
		"name": "vol-1", "size": float64(10), "volumeTypeId": "voltype-1",
		"zoneId": "zone-1", "isEnableAutoRenew": false, "period": float64(1), "isPoc": false,
	}
	for k, v := range want {
		if info[k] != v {
			t.Fatalf("resourceInfo[%q] = %v, want %v", k, info[k], v)
		}
	}
	if out.OptimumPrice != 32000 {
		t.Fatalf("OptimumPrice = %v, want 32000", out.OptimumPrice)
	}
}

func TestQuoteCreateVolumeIgnoresMaxPriceAndNoWait(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(quoteVolumeFixture))
	}))
	if _, err := c.QuoteCreateVolume(context.Background(), &CreateVolumeInput{
		Name: "vol-1", ZoneID: "zone-1", Size: 10, VolumeTypeID: "voltype-1",
		MaxPrice: 1, NoWait: true,
	}); err != nil {
		t.Fatalf("QuoteCreateVolume() error = %v", err)
	}
}

func TestQuoteCreateVolumeRequiresFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	if _, err := c.QuoteCreateVolume(context.Background(), &CreateVolumeInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestQuoteCreateVolumeRejectsBadVolumeTypeID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		if _, err := c.QuoteCreateVolume(context.Background(), &CreateVolumeInput{
			Name: "vol-1", ZoneID: "zone-1", Size: 10, VolumeTypeID: bad,
		}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("VolumeTypeID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}
