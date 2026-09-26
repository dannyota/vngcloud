package monitor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestCheckRequestTimeoutDecode and TestCheckOptionsDecode check the two
// shapes the API sends these fields in: an integral decimal, such as 30.0,
// and a plain integer, plus that a fractional value is rejected without
// being echoed back.
func TestCheckRequestTimeoutDecode(t *testing.T) {
	t.Run("integral decimal", func(t *testing.T) {
		var r CheckRequest
		if err := json.Unmarshal([]byte(`{"timeout":30.0}`), &r); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if r.Timeout != 30 {
			t.Fatalf("Timeout = %d, want 30", r.Timeout)
		}
	})

	t.Run("integer", func(t *testing.T) {
		var r CheckRequest
		if err := json.Unmarshal([]byte(`{"timeout":30}`), &r); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if r.Timeout != 30 {
			t.Fatalf("Timeout = %d, want 30", r.Timeout)
		}
	})

	t.Run("fractional value is rejected", func(t *testing.T) {
		var r CheckRequest
		err := json.Unmarshal([]byte(`{"timeout":30.5}`), &r)
		if err == nil {
			t.Fatal("expected an error for a fractional timeout, got nil")
		}
		if strings.Contains(err.Error(), "30.5") {
			t.Fatalf("error text names the input value: %v", err)
		}
	})
}

// TestCheckNotificationsDecode checks Check.Notifications decodes the
// hyphenated "In-alarm" key alongside the plain "Up" and "Undetermined"
// keys, each a list of channel IDs.
func TestCheckNotificationsDecode(t *testing.T) {
	raw := `{"id":"chk-1","notifications":{"In-alarm":["ch-1","ch-2"],"Up":["ch-3"],"Undetermined":[]}}`
	var check Check
	if err := json.Unmarshal([]byte(raw), &check); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	n := check.Notifications
	if len(n.InAlarm) != 2 || n.InAlarm[0] != "ch-1" || n.InAlarm[1] != "ch-2" {
		t.Fatalf("InAlarm = %v", n.InAlarm)
	}
	if len(n.Up) != 1 || n.Up[0] != "ch-3" {
		t.Fatalf("Up = %v", n.Up)
	}
	if len(n.Undetermined) != 0 {
		t.Fatalf("Undetermined = %v, want empty", n.Undetermined)
	}
}

// TestChannelDecode covers Channel's custom UnmarshalJSON: Type comes from
// the nested typeNotification.name, and Headers comes from the header field,
// a JSON string of [{"key","value"}] pairs that is absent for a channel with
// no headers.
func TestChannelDecode(t *testing.T) {
	t.Run("webhook with headers", func(t *testing.T) {
		raw := `{
			"id": "ch-1",
			"name": "example-webhook",
			"address": "https://example.com/hooks/incoming",
			"header": "[{\"key\":\"X-Example\",\"value\":\"secret\"}]",
			"typeNotification": {"id": "type-webhook", "name": "Webhook", "description": "Webhook"},
			"createdDate": "2026-09-26T15:46:45"
		}`
		var ch Channel
		if err := json.Unmarshal([]byte(raw), &ch); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if ch.ID != "ch-1" || ch.Name != "example-webhook" {
			t.Fatalf("unexpected identity: %+v", ch)
		}
		if ch.Type != ChannelTypeWebhook {
			t.Fatalf("Type = %q, want %q", ch.Type, ChannelTypeWebhook)
		}
		if len(ch.Headers) != 1 || ch.Headers[0].Key != "X-Example" || ch.Headers[0].Value != "secret" {
			t.Fatalf("Headers = %+v", ch.Headers)
		}
		if ch.CreatedDate != "2026-09-26T15:46:45" || ch.UpdatedDate != "" {
			t.Fatalf("unexpected timestamps: %+v", ch)
		}
	})

	t.Run("no header field", func(t *testing.T) {
		raw := `{"id":"ch-2","name":"example-email","address":"<account>",
			"typeNotification":{"id":"type-email","name":"Email","description":"Email"},
			"createdDate":"2026-09-26T15:40:00","updatedDate":"2026-09-26T16:00:00"}`
		var ch Channel
		if err := json.Unmarshal([]byte(raw), &ch); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if ch.Headers != nil {
			t.Fatalf("Headers = %+v, want nil", ch.Headers)
		}
		if ch.UpdatedDate != "2026-09-26T16:00:00" {
			t.Fatalf("UpdatedDate = %q", ch.UpdatedDate)
		}
	})

	t.Run("header not a JSON array of key/value objects", func(t *testing.T) {
		for _, header := range []string{"not json", "{}", `"a string"`, "[1,2,3]"} {
			raw := `{"id":"ch-3","header":` + jsonString(header) + `}`
			var ch Channel
			if err := json.Unmarshal([]byte(raw), &ch); err != nil {
				t.Fatalf("Unmarshal() error = %v for header %q", err, header)
			}
			if ch.Headers != nil {
				t.Fatalf("Headers = %+v, want nil for header %q", ch.Headers, header)
			}
		}
	})

	t.Run("empty header field", func(t *testing.T) {
		raw := `{"id":"ch-4","header":""}`
		var ch Channel
		if err := json.Unmarshal([]byte(raw), &ch); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if ch.Headers != nil {
			t.Fatalf("Headers = %+v, want nil", ch.Headers)
		}
	})
}

// TestChannelMarshalJSONRoundTrips checks that Channel.MarshalJSON writes
// Type and Headers back into the typeNotification and header wire fields
// UnmarshalJSON reads, even though both fields carry a json:"-" tag against
// encoding/json's own default marshaling. Without a custom MarshalJSON, a
// caller that marshals a Channel to cache it and unmarshals it back would
// silently lose both fields.
func TestChannelMarshalJSONRoundTrips(t *testing.T) {
	t.Run("webhook with headers", func(t *testing.T) {
		want := Channel{
			ID:              "ch-1",
			Name:            "example-webhook",
			Address:         "https://example.com/hooks/incoming",
			Type:            ChannelTypeWebhook,
			Headers:         []ChannelHeader{{Key: "X-Example", Value: "secret"}},
			MetricMappingID: "metric-1",
			CreatedDate:     "2026-09-26T15:46:45",
			UpdatedDate:     "2026-09-26T16:00:00",
		}

		data, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}

		var got Channel
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal() error = %v (data=%s)", err, data)
		}
		if got.ID != want.ID || got.Name != want.Name || got.Address != want.Address {
			t.Fatalf("unexpected identity: got %+v, want %+v", got, want)
		}
		if got.Type != want.Type {
			t.Fatalf("Type = %q, want %q", got.Type, want.Type)
		}
		if !reflect.DeepEqual(got.Headers, want.Headers) {
			t.Fatalf("Headers = %+v, want %+v", got.Headers, want.Headers)
		}
		if got.MetricMappingID != want.MetricMappingID || got.CreatedDate != want.CreatedDate || got.UpdatedDate != want.UpdatedDate {
			t.Fatalf("unexpected metadata: got %+v, want %+v", got, want)
		}
	})

	t.Run("email with no headers", func(t *testing.T) {
		want := Channel{ID: "ch-2", Type: ChannelTypeEmail, Address: "someone@example.com"}

		data, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}

		var got Channel
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("Unmarshal() error = %v (data=%s)", err, data)
		}
		if got.Type != want.Type {
			t.Fatalf("Type = %q, want %q", got.Type, want.Type)
		}
		if got.Headers != nil {
			t.Fatalf("Headers = %+v, want nil", got.Headers)
		}
	})
}

// jsonString encodes s as a JSON string literal, so a test table can embed
// arbitrary raw text, including text that is itself invalid JSON, as a JSON
// string value.
func jsonString(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestCheckOptionsDecode(t *testing.T) {
	t.Run("integral decimals", func(t *testing.T) {
		var o CheckOptions
		raw := `{"test_frequency":60.0,"tests":1.0,"failed_locations":1.0}`
		if err := json.Unmarshal([]byte(raw), &o); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if o.TestFrequency != 60 || o.Tests != 1 || o.FailedLocations != 1 {
			t.Fatalf("unexpected options: %+v", o)
		}
	})

	t.Run("fractional value is rejected", func(t *testing.T) {
		var o CheckOptions
		err := json.Unmarshal([]byte(`{"tests":1.5}`), &o)
		if err == nil {
			t.Fatal("expected an error for a fractional tests, got nil")
		}
		if strings.Contains(err.Error(), "1.5") {
			t.Fatalf("error text names the input value: %v", err)
		}
	})
}
