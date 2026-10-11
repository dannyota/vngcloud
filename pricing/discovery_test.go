package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestDiscoveryErrorIdentityAndNilPrices(t *testing.T) {
	api := &vngcloud.APIError{Operation: "test.Quote", StatusCode: 500}
	err := &DiscoveryError{Errors: []error{api, context.Canceled}}
	var found *vngcloud.APIError
	if !errors.Is(err, ErrDiscoveryIncomplete) || !errors.Is(err, context.Canceled) || !errors.As(err, &found) || found != api {
		t.Fatal("lost error identity")
	}
	raw, e := json.Marshal(DiscoveryPrice{})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(raw), `"MonthlyPrice":null`) {
		t.Fatal(string(raw))
	}
}

func TestDiscoveryMetadataEmptyListsAndUTC(t *testing.T) {
	raw, err := json.Marshal(DiscoveryMetadata{QuotedAt: time.Date(2020, 1, 1, 7, 0, 0, 0, time.FixedZone("offset", 7*60*60))})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"Failures":[]`, `"Notes":[]`, `"QuotedAt":"2020-01-01T00:00:00Z"`, `"ExcludedGroups":null`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s in %s", want, raw)
		}
	}
}
