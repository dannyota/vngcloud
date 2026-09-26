package monitor

import (
	"encoding/json"
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
