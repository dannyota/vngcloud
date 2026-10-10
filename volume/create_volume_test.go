package volume

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

const quoteVolumeFixture = `{"optimumPrice":32000,"originalPrice":32000,"discountPrice":0,"discountPercent":0,"propertiesPrice":[{"name":"Volume","description":"","optimumPrice":32000,"monthlyPrice":32000,"currentPrice":null,"discountPercent":0}]}`

func TestQuoteCreateVolumeSendsPricedBody(t *testing.T) {
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
		ZoneID: "zone-1", Size: 10, VolumeTypeID: "voltype-1",
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
		"size": float64(10), "volumeTypeId": "voltype-1",
		"zoneId": "zone-1", "period": float64(1), "isPoc": false,
	}
	if !reflect.DeepEqual(info, want) {
		t.Fatalf("resourceInfo = %v, want %v", info, want)
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

// TestQuoteCreateVolumeRejectsNonPositiveSize checks that a zero or
// negative Size refuses before any request: CheckRequired already catches
// 0 through the vngcloud:"required" tag, so this exercises the explicit
// check that also catches a negative value, which CheckRequired's
// zero-value test cannot.
func TestQuoteCreateVolumeRejectsNonPositiveSize(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, size := range []int{0, -1, -10} {
		if _, err := c.QuoteCreateVolume(context.Background(), &CreateVolumeInput{
			Name: "vol-1", ZoneID: "zone-1", Size: size, VolumeTypeID: "voltype-1",
		}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("Size=%d: err = %v, want ErrInvalidInput", size, err)
		}
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

// TestQuoteCreateVolumeSetUnpricedFieldsStayOutOfBody checks that a Name and
// AutoRenew the caller sets never reach the billing gateway.
func TestQuoteCreateVolumeSetUnpricedFieldsStayOutOfBody(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatalf("decode body: %v, raw = %s", err, data)
		}
		_, _ = w.Write([]byte(quoteVolumeFixture))
	}))
	if _, err := c.QuoteCreateVolume(context.Background(), &CreateVolumeInput{
		Name: "vol-1", ZoneID: "zone-1", Size: 10, VolumeTypeID: "voltype-1", AutoRenew: true,
	}); err != nil {
		t.Fatalf("QuoteCreateVolume() error = %v", err)
	}
	info := body["resourceInfo"].(map[string]any)
	for _, k := range []string{"name", "isEnableAutoRenew"} {
		if _, ok := info[k]; ok {
			t.Fatalf("resourceInfo has %q: %v", k, info)
		}
	}
}

func TestQuoteCreateVolumeRequiresPricedFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for name, in := range map[string]*CreateVolumeInput{
		"ZoneID":       {Size: 10, VolumeTypeID: "voltype-1"},
		"Size":         {ZoneID: "zone-1", VolumeTypeID: "voltype-1"},
		"VolumeTypeID": {ZoneID: "zone-1", Size: 10},
	} {
		if _, err := c.QuoteCreateVolume(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("%s empty: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if _, err := c.QuoteCreateVolume(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input: err = %v, want ErrInvalidInput", err)
	}
}

// TestVolumeQuoteBodyKeysMatchCreateBody builds the quote and the create
// from one fully populated Input and checks that every priced key the quote
// sends reaches the create with the same value. A priced field added only
// to the create body would let the quote understate the bill.
func TestVolumeQuoteBodyKeysMatchCreateBody(t *testing.T) {
	in := &CreateVolumeInput{
		Name:         "vol-1",
		ZoneID:       "zone-1",
		Size:         10,
		VolumeTypeID: "voltype-1",
		AutoRenew:    true,
		MaxPrice:     1,
		NoWait:       true,
	}
	testutil.RequireAllFieldsSet(t, in)
	info, err := buildVolumeQuoteInfo("op", in)
	if err != nil {
		t.Fatal(err)
	}
	body, err := buildCreateVolumeBody("op", in)
	if err != nil {
		t.Fatal(err)
	}
	testutil.RequireQuoteKeysInCreate(t, info, body, "period", "isPoc")
}
