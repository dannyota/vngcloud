package monitor

import "testing"

func TestConsoleMatchAllLogFilterBytes(t *testing.T) {
	const prefix = `{"type":"bool","value":{"filter":[],"should":[],"must":[{"type":"bool","value":{"filter":[],"must":[{"type":"multi_match","value":{"type":"phrase","query":`
	const suffix = `,"lenient":true}}],"should":[],"mustNot":[]}}],"mustNot":[]}}`
	for _, tc := range []struct {
		name, query, encoded string
	}{
		{"plain", "hello world", `"hello world"`},
		{"quotes", `say "hello"`, `"say \"hello\""`},
		{"backslashes", `a\b`, `"a\\b"`},
		{"html", "<>&", `"\u003c\u003e\u0026"`},
		{"unicode", "你好 café", `"你好 café"`},
		{"line-separators", "\u2028\u2029", `"\u2028\u2029"`},
		{"newlines", "first\nsecond\r\n", `"first\nsecond\r\n"`},
		{"json-looking", `{"key":"value"}`, `"{\"key\":\"value\"}"`},
		{"invalid-utf8", string([]byte{0xff}), `"�"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := string(consoleMatchAllLogFilter(tc.query)), prefix+tc.encoded+suffix; got != want {
				t.Errorf("filter = %q, want %q", got, want)
			}
		})
	}
	const empty = `{"type":"bool","value":{"filter":[],"should":[],"must":[],"mustNot":[]}}`
	if got := string(consoleMatchAllLogFilter("")); got != empty {
		t.Errorf("empty filter = %q, want %q", got, empty)
	}
	if matchAllLogFilter != empty {
		t.Errorf("match-all filter = %q, want %q", matchAllLogFilter, empty)
	}
}
