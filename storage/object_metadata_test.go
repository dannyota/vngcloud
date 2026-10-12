package storage

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestObjectMetadataHeaders(t *testing.T) {
	c := objectTestClient(t)
	for _, tc := range []struct {
		name  string
		h     http.Header
		limit int
		want  string
	}{
		{"absent", nil, 4, ""},
		{"exact bytes", http.Header{"ETag": {"éé"}}, 4, "éé"},
		{"over bytes", http.Header{"ETag": {"ééx"}}, 4, ""},
		{"case insensitive", http.Header{"eTaG": {"safe"}}, 4, "safe"},
		{"identical duplicates", http.Header{"Etag": {"safe", "safe"}}, 8, ""},
		{"conflicting duplicates", http.Header{"Etag": {"safe", "other"}}, 8, ""},
		{"case variants", http.Header{"ETag": {"safe"}, "etag": {"safe"}}, 8, ""},
		{"empty variant", http.Header{"ETag": {"safe"}, "etag": nil}, 8, ""},
		{"no values", http.Header{"ETag": nil}, 8, ""},
		{"invalid UTF8", http.Header{"ETag": {"\xff"}}, 8, ""},
		{"control", http.Header{"ETag": {"a\nb"}}, 8, ""},
		{"credential", http.Header{"ETag": {"prefix synthetic-secret-key"}}, 128, ""},
		{"credential key", http.Header{"ETag": {"client_secret=unknown"}}, 128, ""},
		{"negative bound", http.Header{"ETag": {"safe"}}, -1, ""},
		{"zero bound", http.Header{"ETag": {"safe"}}, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.objectScalarHeader(tc.h, "etag", tc.limit); got != tc.want {
				t.Fatalf("optional scalar = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestObjectMetadataUserRules(t *testing.T) {
	c := objectTestClient(t)
	for _, tc := range []struct {
		name, suffix, value string
		keep                bool
	}{
		{"one byte", "a", "", true},
		{"allowed alphabet", "Az09_-", "safe", true},
		{"exact name", strings.Repeat("n", 128), "safe", true},
		{"over name", strings.Repeat("n", 129), "safe", false},
		{"empty name", "", "safe", false},
		{"space", "a b", "safe", false},
		{"non ASCII", "é", "safe", false},
		{"punctuation", "a.b", "safe", false},
		{"name control", "a\tb", "safe", false},
		{"name credential", "synthetic-access-id", "safe", false},
		{"lowercase credential", "SYNTHETIC-ACCESS-ID", "safe", false},
		{"exact UTF8 bytes", "a", strings.Repeat("é", 1024), true},
		{"over value", "a", strings.Repeat("é", 1024) + "x", false},
		{"invalid UTF8", "a", "\xff", false},
		{"value credential", "a", "synthetic-secret-key", false},
		{"credential key", "a", `{"client_secret":"unknown"}`, false},
		{"opaque percent", "a", "%73afe", true},
		{"opaque MIME", "a", "=?UTF-8?B?c2FmZQ==?=", true},
		{"opaque base64", "a", "c2FmZQ==", true},
		{"opaque JSON", "a", `{"label":"safe"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{"X-AmZ-MeTa-" + tc.suffix: {tc.value}, "Other": {"ignored"}}
			before := h.Clone()
			got := c.objectUserMetadata(h)
			want := map[string]string{}
			if tc.keep {
				want[strings.ToLower(tc.suffix)] = tc.value
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("metadata pair acceptance or whole-field omission differs")
			}
			if !reflect.DeepEqual(h, before) {
				t.Fatal("input headers changed")
			}
			assertObjectSafe(t, got, "synthetic-access-id", "synthetic-secret-key")
		})
	}
	for i := 0; i <= 127; i++ {
		if i >= 32 && i != 127 {
			continue
		}
		if got := c.objectUserMetadata(http.Header{"x-amz-meta-a": {"a" + string(rune(i)) + "b"}}); len(got) != 0 {
			t.Fatalf("control byte %d retained", i)
		}
	}
	for _, h := range []http.Header{nil, {}, {"x-amz-meta-a": nil},
		{"x-amz-meta-a": {"same", "same"}},
		{"X-Amz-Meta-A": {"safe"}, "x-amz-meta-a": {"safe"}},
		{"X-Amz-Meta-A": {"safe"}, "x-amz-meta-a": nil}} {
		if got := c.objectUserMetadata(h); got == nil || len(got) != 0 {
			t.Fatal("empty or ambiguous map retained")
		}
	}
}

func TestObjectMetadataBudgets(t *testing.T) {
	c := objectTestClient(t)
	h := http.Header{}
	for i := 64; i >= 0; i-- {
		h[fmt.Sprintf("x-amz-meta-k%02d", i)] = []string{"v"}
	}
	for range 8 {
		got := c.objectUserMetadata(h)
		if len(got) != 64 {
			t.Fatal("retained pair count differs")
		}
		for i := 0; i < 64; i++ {
			if got[fmt.Sprintf("k%02d", i)] != "v" {
				t.Fatal("sorted count selection differs")
			}
		}
		if _, ok := got["k64"]; ok {
			t.Fatal("last sorted pair exceeded count budget")
		}
	}
	// Four pairs reach exactly 8 KiB including suffix bytes. An oversized
	// next pair is omitted whole; a later small pair still fits when space exists.
	h = http.Header{"x-amz-meta-a": {strings.Repeat("v", 2048)},
		"x-amz-meta-b": {strings.Repeat("v", 2048)},
		"x-amz-meta-c": {strings.Repeat("v", 2048)},
		"x-amz-meta-d": {strings.Repeat("v", 2044)}, "x-amz-meta-e": {""}}
	for range 8 {
		got := c.objectUserMetadata(h)
		if len(got) != 4 || len(got["d"]) != 2044 {
			t.Fatal("exact total budget not retained")
		}
		if _, ok := got["e"]; ok {
			t.Fatal("suffix byte exceeded total budget")
		}
	}
	h["x-amz-meta-d"] = []string{strings.Repeat("v", 2045)}
	got := c.objectUserMetadata(h)
	if _, ok := got["d"]; ok {
		t.Fatal("over-budget pair partially retained")
	}
	if v, ok := got["e"]; !ok || v != "" || len(got) != 4 {
		t.Fatal("later fitting pair omitted")
	}
}

func TestObjectMetadataTimes(t *testing.T) {
	c := objectTestClient(t)
	for _, tc := range []struct {
		source, layout string
		limit          int
		keep           bool
	}{
		{"2024-01-02T03:04:05.123456789+01:00", time.RFC3339, 35, true},
		{"2024-01-02T03:04:05.123456789+01:00", time.RFC3339, 34, false},
		{"Tue, 02 Jan 2024 03:04:05 GMT", http.TimeFormat, 29, true},
		{"Tue, 02 Jan 2024 03:04:05 GMT", http.TimeFormat, 28, false},
		{"2024-01-02T03:04:05Z", time.RFC3339, 19, false},
		{"invalid", time.RFC3339, 128, false},
		{"synthetic-access-id", time.RFC3339, 128, false},
		{"2024-01-02T03:04:05Z\n", time.RFC3339, 128, false},
		{"9999-12-31T23:30:00-01:00", time.RFC3339, 128, false},
		{"0000-01-01T00:30:00+01:00", time.RFC3339, 128, false},
	} {
		t.Run(tc.source+strconv.Itoa(tc.limit), func(t *testing.T) {
			got := c.objectTime(tc.source, tc.limit, tc.layout)
			if (got != nil) != tc.keep {
				t.Fatal("time acceptance differs")
			}
			if !tc.keep {
				return
			}
			want, err := time.Parse(tc.layout, tc.source)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(want) || got.Location() != time.UTC || got.Nanosecond() != want.Nanosecond() {
				t.Fatal("UTC instant or fractional seconds changed")
			}
			text, err := got.MarshalText()
			if err != nil || string(text) != want.UTC().Format(time.RFC3339Nano) {
				t.Fatal("text changed")
			}
			data, err := json.Marshal(got)
			if err != nil || string(data) != `"`+string(text)+`"` {
				t.Fatal("JSON changed")
			}
		})
	}
}

func TestObjectMetadataTimeRepresentations(t *testing.T) {
	for _, source := range []struct{ value, layout string }{
		{"2024-01-02T03:04:05.123456789+01:00", time.RFC3339},
		{"Tue, 02 Jan 2024 03:04:05 GMT", http.TimeFormat},
	} {
		parsed, err := time.Parse(source.layout, source.value)
		if err != nil {
			t.Fatal(err)
		}
		utc := parsed.UTC()
		text, err := utc.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		data, err := utc.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		for _, form := range []string{utc.Format(time.RFC3339Nano), string(text), string(data),
			fmt.Sprintf("%v", utc), fmt.Sprintf("%+v", utc), fmt.Sprintf("%#v", utc)} {
			// The remote source is safe for this synthetic credential, but the
			// exported UTC representation would disclose it.
			if strings.Contains(source.value, form) {
				t.Fatal("normalization fixture already unsafe at source")
			}
			c := objectTestClient(t)
			c.credentials.SecretAccessKey = vngcloud.Secret(form)
			if c.filterObjectMetadata(source.value, 128) != source.value {
				t.Fatal("fixture source filtered")
			}
			got := c.objectTime(source.value, 128, source.layout)
			if got != nil {
				t.Fatal("credential-reflecting normalized time retained")
			}
			out := struct{ LastModified *time.Time }{got}
			if out.LastModified != nil {
				t.Fatal("direct field retained")
			}
			assertObjectSafe(t, out, form)
			if strings.Contains(fmt.Sprintf("%v", out), form) {
				t.Fatal("default formatting disclosed credential")
			}
			encoded, err := json.Marshal(out)
			if err != nil || string(encoded) != `{"LastModified":null}` {
				t.Fatal("omitted time not JSON null")
			}
		}
	}
	// A short credential can appear only after offset normalization.
	c := objectTestClient(t)
	c.credentials.SecretAccessKey = "x"
	c.credentials.AccessKeyID = "02:04"
	if got := c.objectTime("2024-01-02T03:04:05+01:00", 128, time.RFC3339); got != nil {
		t.Fatal("short normalized credential retained")
	}
}

func TestObjectMetadataSuffixAlphabet(t *testing.T) {
	c := objectTestClient(t)
	const allowed = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	for b := 0; b < 128; b++ {
		suffix := string(rune(b))
		got := c.objectUserMetadata(http.Header{"x-amz-meta-" + suffix: {"safe"}})
		want := map[string]string{}
		if strings.Contains(allowed, suffix) {
			want[strings.ToLower(suffix)] = "safe"
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("suffix byte %d acceptance differs", b)
		}
	}
}

func TestObjectMetadataEncodedReflections(t *testing.T) {
	c := objectTestClient(t)
	c.credentials.AccessKeyID = "short"
	c.credentials.SecretAccessKey = `a"b\c`
	for _, secret := range []string{"short", `a"b\c`} {
		encoded, err := json.Marshal(secret)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{secret, string(encoded)} {
			if got := c.objectScalarHeader(http.Header{"Etag": {value}}, "ETag", 1024); got != "" {
				t.Fatal("literal or JSON-escaped credential scalar retained")
			}
			got := c.objectUserMetadata(http.Header{"x-amz-meta-safe": {value}})
			if len(got) != 0 {
				t.Fatal("literal or JSON-escaped credential pair retained")
			}
			assertObjectSafe(t, got, secret)
		}
	}
	// Preserve the accepted crafted nested-encoding limitation. This helper
	// must not add another JSON decoding pass to the existing whole-field filter.
	inner, err := json.Marshal(c.credentials.SecretAccessKey.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	nested, err := json.Marshal(string(inner))
	if err != nil {
		t.Fatal(err)
	}
	value := string(nested)
	if c.filterObjectMetadata(value, 1024) != value {
		t.Fatal("limitation fixture unexpectedly filtered")
	}
	if got := c.objectUserMetadata(http.Header{"x-amz-meta-safe": {value}}); got["safe"] != value {
		t.Fatal("nested representation decoded or changed")
	}
}

func TestObjectMetadataTimeMarshalFailures(t *testing.T) {
	c := objectTestClient(t)
	for _, source := range []string{"9999-12-31T23:30:00-01:00", "0000-01-01T00:30:00+01:00"} {
		parsed, err := time.Parse(time.RFC3339, source)
		if err != nil {
			t.Fatal(err)
		}
		utc := parsed.UTC()
		if _, err := utc.MarshalText(); err == nil {
			t.Fatal("fixture must fail text marshaling")
		}
		if _, err := utc.MarshalJSON(); err == nil {
			t.Fatal("fixture must fail JSON marshaling")
		}
		if got := c.objectTime(source, 128, time.RFC3339); got != nil {
			t.Fatal("unmarshalable UTC time retained")
		}
	}
}
