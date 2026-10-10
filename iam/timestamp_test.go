package iam

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

// TestEpochMillisForms checks every wire form a timestamp field takes.
func TestEpochMillisForms(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{"number", `1750000000000`, 1750000000000, false},
		{"numeric string", `"1750000000000"`, 1750000000000, false},
		{"number long", `{"$numberLong": "1750000000000"}`, 1750000000000, false},
		{"rfc3339 millis", `"2025-06-15T15:06:40.000Z"`, 1750000000000, false},
		{"rfc3339 seconds", `"2025-06-15T15:06:40Z"`, 1750000000000, false},
		{"rfc3339 offset", `"2025-06-15T22:06:40.500+07:00"`, 1750000000500, false},
		{"null", `null`, 0, false},
		{"empty string", `""`, 0, false},
		{"word", `"yesterday"`, 0, true},
		{"bool", `true`, 0, true},
		{"bad number long", `{"$numberLong": "x"}`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got epochMillis
			err := json.Unmarshal([]byte(tt.in), &got)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Unmarshal(%s) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if int64(got) != tt.want {
				t.Fatalf("Unmarshal(%s) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestListServiceAccountsStringTimestamps decodes a raw list whose rows carry
// timestamps as ISO 8601 and numeric strings, with lastUse sometimes absent.
func TestListServiceAccountsStringTimestamps(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/iam/list_service_accounts_string_timestamps.json")
	}))
	out, err := c.ListServiceAccounts(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListServiceAccounts() error = %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(out.Items))
	}
	if got := out.Items[0]; got.CreatedAt != 1750000000000 || got.LastUse != 0 {
		t.Fatalf("Items[0] CreatedAt, LastUse = %d, %d; want 1750000000000, 0", got.CreatedAt, got.LastUse)
	}
	if got := out.Items[1]; got.CreatedAt != 1750000000000 || got.LastUse != 1750000100000 {
		t.Fatalf("Items[1] CreatedAt, LastUse = %d, %d; want 1750000000000, 1750000100000", got.CreatedAt, got.LastUse)
	}
}
