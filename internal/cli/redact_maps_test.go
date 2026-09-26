package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"danny.vn/vngcloud/monitor"
)

func TestIsSensitiveMapKeyMatchesEverySubstringCaseInsensitively(t *testing.T) {
	sensitive := []string{"password", "Password", "apiSecret", "AccessToken", "Credential", "privateKey", "PrivateKey", "accessKey", "access_key_id", ".dockerconfigjson", "auth", "Auths"}
	for _, key := range sensitive {
		if !isSensitiveMapKey(key) {
			t.Errorf("isSensitiveMapKey(%q) = false, want true", key)
		}
	}
	safe := []string{"name", "id", "status", "used", "limit", "author", "authType"}
	for _, key := range safe {
		if isSensitiveMapKey(key) {
			t.Errorf("isSensitiveMapKey(%q) = true, want false", key)
		}
	}
}

// TestIsSensitiveMapKeyMatchesEverySpellingAfterNormalizing checks that
// normalizing a key before matching (lower-case, letters and digits only)
// catches every punctuation and casing variant of the same word, including
// the three private-key spellings a plain lower-case Contains check missed,
// and the substrings the match list gained alongside them.
func TestIsSensitiveMapKeyMatchesEverySpellingAfterNormalizing(t *testing.T) {
	sensitive := []string{
		"private_key", "private-key", "Private Key", "PRIVATE_KEY",
		"passwd", "Passwd", "pass_wd",
		"passphrase", "pass-phrase", "Pass Phrase",
		"apikey", "api_key", "API-Key", "Api Key",
		"authorization", "Authorization", "AUTHORIZATION-HEADER",
	}
	for _, key := range sensitive {
		if !isSensitiveMapKey(key) {
			t.Errorf("isSensitiveMapKey(%q) = false, want true", key)
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
	// json.Marshal HTML-escapes "<" and ">" by default, so the literal
	// marker never survives JSON output unchanged; "redacted" alone does.
	if !strings.Contains(got, "redacted") {
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

	if got := out.Items[0]["token"]; got != redactedPlaceholder {
		t.Fatalf("Items[0][token] = %v, want %q", got, redactedPlaceholder)
	}
	nested, ok := out.Items[1]["nested"].(map[string]any)
	if !ok {
		t.Fatalf("Items[1][nested] lost its type: %#v", out.Items[1]["nested"])
	}
	if got := nested["password"]; got != redactedPlaceholder {
		t.Fatalf("nested[password] = %v, want %q", got, redactedPlaceholder)
	}
	if got := out.Items[1]["name"]; got != "kept" {
		t.Fatalf("Items[1][name] = %v, want it left alone", got)
	}
}

// TestRedactMapsIgnoresANarrowerMapValueType checks that a map whose value
// type is not any, such as map[string]int, is left alone entirely: the CLI
// reads design scopes redaction to map[string]any, so redactMaps never even
// walks into a narrower map value type, since nothing narrower than any can
// hold a nested map or slice for it to find further down anyway.
func TestRedactMapsIgnoresANarrowerMapValueType(t *testing.T) {
	m := map[string]int{"password": 1, "count": 2}
	redactMaps(reflect.ValueOf(m))
	if m["password"] != 1 {
		t.Fatalf("password = %d, want it left alone (map[string]int is not map[string]any)", m["password"])
	}
}

// TestRedactMapsLeavesTypedMapStructFieldsAlone checks the CLI reads
// design's scope for this release: a struct field of a concrete map type
// such as map[string]string, the shape monitor.CheckRequest.Headers and
// Query decode as, is never touched, even when one of its keys looks
// sensitive; only a map[string]any field is ever redacted. This is what
// keeps a get-check round trip to create-check intact, per the monitor
// design's decision that check headers and bodies print unredacted.
func TestRedactMapsLeavesTypedMapStructFieldsAlone(t *testing.T) {
	type hasTypedMap struct {
		Headers map[string]string
	}
	v := &hasTypedMap{Headers: map[string]string{
		"Authorization": "Bearer real-secret-token",
		"api_token":     "real-secret-token",
	}}
	redactMaps(reflect.ValueOf(v))
	if v.Headers["Authorization"] != "Bearer real-secret-token" {
		t.Fatalf("Headers[Authorization] = %q, want it left alone", v.Headers["Authorization"])
	}
	if v.Headers["api_token"] != "real-secret-token" {
		t.Fatalf("Headers[api_token] = %q, want it left alone", v.Headers["api_token"])
	}
}

// TestRedactMapsMonitorCheckHeadersUnchangedPortalMapRedacted checks the
// same rule end to end through renderOutput, on the two real types the CLI
// reads design names: a monitor check's Headers and Query print in full
// (decision 7 of the monitor design), while a portal-shaped map[string]any
// Output still redacts a matching key.
func TestRedactMapsMonitorCheckHeadersUnchangedPortalMapRedacted(t *testing.T) {
	check := &monitor.GetCheckOutput{Check: monitor.Check{
		ID: "chk-1",
		Config: monitor.CheckConfig{
			Request: monitor.CheckRequest{
				Headers: map[string]string{"Authorization": "Bearer real-secret-token"},
				Query:   map[string]string{"api_token": "real-secret-token"},
			},
		},
	}}
	var checkBuf bytes.Buffer
	if err := renderOutput(&checkBuf, outputJSON, "", check, false); err != nil {
		t.Fatalf("renderOutput: %v", err)
	}
	if !strings.Contains(checkBuf.String(), "real-secret-token") {
		t.Fatalf("monitor check Headers/Query were redacted, want them printed in full:\n%s", checkBuf.String())
	}

	portalLike := &redactTestOutput{Info: map[string]any{"token": "portal-secret-token"}}
	var portalBuf bytes.Buffer
	if err := renderOutput(&portalBuf, outputJSON, "", portalLike, false); err != nil {
		t.Fatalf("renderOutput: %v", err)
	}
	if strings.Contains(portalBuf.String(), "portal-secret-token") {
		t.Fatalf("portal-shaped map[string]any value was not redacted:\n%s", portalBuf.String())
	}
}

// TestRedactMapsWalksAnySliceInsideAnyMap checks the deepest nesting the CLI
// reads design's "reached through any or []any inside them" allows: a
// map[string]any holding a []any holding another map[string]any still gets
// its sensitive key redacted, and a sibling non-sensitive value survives.
func TestRedactMapsWalksAnySliceInsideAnyMap(t *testing.T) {
	out := map[string]any{
		"items": []any{
			map[string]any{"token": "deep-secret", "name": "kept"},
		},
	}
	redactMaps(reflect.ValueOf(out))

	items, ok := out["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v, want a one-element []any", out["items"])
	}
	nested, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("items[0] lost its type: %#v", items[0])
	}
	if got := nested["token"]; got != redactedPlaceholder {
		t.Fatalf("nested[token] = %v, want %q", got, redactedPlaceholder)
	}
	if got := nested["name"]; got != "kept" {
		t.Fatalf("nested[name] = %v, want it left alone", got)
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
