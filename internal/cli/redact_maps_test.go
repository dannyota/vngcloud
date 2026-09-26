package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestIsSensitiveMapKeyMatchesEverySubstringCaseInsensitively(t *testing.T) {
	sensitive := []string{"password", "Password", "apiSecret", "AccessToken", "Credential", "privateKey", "PrivateKey"}
	for _, key := range sensitive {
		if !isSensitiveMapKey(key) {
			t.Errorf("isSensitiveMapKey(%q) = false, want true", key)
		}
	}
	safe := []string{"name", "id", "status", "used", "limit"}
	for _, key := range safe {
		if isSensitiveMapKey(key) {
			t.Errorf("isSensitiveMapKey(%q) = true, want false", key)
		}
	}
}

// redactTestOutput stands in for a map-backed Output such as
// portal.GetUserInfoOutput: a struct wrapping one map[string]any field.
type redactTestOutput struct {
	Info map[string]any
}

func newRedactTestOutput() *redactTestOutput {
	return &redactTestOutput{Info: map[string]any{
		"name":        "resource-1",
		"Password":    "super-secret",
		"apiSecret":   "shh",
		"accessToken": "tok-1",
		"Credential":  "cred-1",
		"privateKey":  "key-1",
		"nested": map[string]any{
			"token": "nested-secret",
		},
	}}
}

// TestRedactMapsHidesSensitiveKeysInEveryFormat checks the CLI reads
// design's "Key redaction for map-backed Outputs": a map with each kind of
// sensitive key, fed through json, table, and text, never prints a matched
// value, at any depth, while an ordinary value survives.
func TestRedactMapsHidesSensitiveKeysInEveryFormat(t *testing.T) {
	sensitiveValues := []string{"super-secret", "shh", "tok-1", "cred-1", "key-1", "nested-secret"}

	for _, format := range []string{outputJSON, outputTable, outputText} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			if err := renderOutput(&buf, format, "", newRedactTestOutput(), false); err != nil {
				t.Fatalf("renderOutput: %v", err)
			}
			got := buf.String()
			for _, secret := range sensitiveValues {
				if strings.Contains(got, secret) {
					t.Fatalf("%s output leaked a secret value %q:\n%s", format, secret, got)
				}
			}
			if !strings.Contains(got, "resource-1") {
				t.Fatalf("%s output dropped a non-sensitive value:\n%s", format, got)
			}
		})
	}
}

// TestRedactMapsAppliesBeforeQuery checks that --query cannot pull a
// redacted value back out: the query runs on the same value redactMaps
// already mutated, per the CLI reads design's "before encoding".
func TestRedactMapsAppliesBeforeQuery(t *testing.T) {
	var buf bytes.Buffer
	if err := renderOutput(&buf, outputJSON, "Info.nested", newRedactTestOutput(), false); err != nil {
		t.Fatalf("renderOutput: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "nested-secret") {
		t.Fatalf("--query output leaked a secret value:\n%s", got)
	}
	if !strings.Contains(got, redactedValue) {
		t.Fatalf("--query output is missing the redacted marker:\n%s", got)
	}
}

// redactNestedTestOutput exercises redactMaps through a slice of maps, the
// shape a map-backed list Output (core.List[portal.Zone], for example) has.
type redactNestedTestOutput struct {
	Items []map[string]any
}

func TestRedactMapsWalksSlicesAndNestedMaps(t *testing.T) {
	out := &redactNestedTestOutput{Items: []map[string]any{
		{"token": "secret-1"},
		{"nested": map[string]any{"password": "secret-2"}, "name": "kept"},
	}}
	redactMaps(reflect.ValueOf(out))

	if got := out.Items[0]["token"]; got != redactedValue {
		t.Fatalf("Items[0][token] = %v, want %q", got, redactedValue)
	}
	nested, ok := out.Items[1]["nested"].(map[string]any)
	if !ok {
		t.Fatalf("Items[1][nested] lost its type: %#v", out.Items[1]["nested"])
	}
	if got := nested["password"]; got != redactedValue {
		t.Fatalf("nested[password] = %v, want %q", got, redactedValue)
	}
	if got := out.Items[1]["name"]; got != "kept" {
		t.Fatalf("Items[1][name] = %v, want it left alone", got)
	}
}

// TestRedactMapsIgnoresANarrowerMapValueType checks redactMapEntries' guard:
// a map whose value type cannot hold a string is walked (so a nested map
// further down still gets redacted) but never itself redacted, since nothing
// sensitive can be an int.
func TestRedactMapsIgnoresANarrowerMapValueType(t *testing.T) {
	m := map[string]int{"password": 1, "count": 2}
	redactMaps(reflect.ValueOf(m))
	if m["password"] != 1 {
		t.Fatalf("password = %d, want it left alone (map[string]int cannot hold [redacted])", m["password"])
	}
}

func TestRedactMapsSkipsUnexportedFields(t *testing.T) {
	type hasUnexported struct {
		unexported map[string]any
	}
	v := hasUnexported{unexported: map[string]any{"password": "secret"}}
	// Must not panic: CanInterface is false for an unexported field, and
	// redactMaps must skip it rather than call SetMapIndex through it.
	redactMaps(reflect.ValueOf(&v))
	if v.unexported["password"] != "secret" {
		t.Fatalf("unexported field was mutated despite being unreachable through the public API")
	}
}
