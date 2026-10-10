package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "update golden files in testdata/golden")

// goldenBase and the types below are small stand-ins for real SDK Output and
// resource shapes, used only so the golden files stay short and readable:
// the pipeline under test (encodeJSON, --query, and json/table/text
// rendering) does not care whether a struct comes from this package or a
// service package.
type goldenBase struct {
	UUID string
	Name string
}

type goldenBudget struct {
	goldenBase
	LimitAmount int64
}

type goldenPagedList struct {
	Items     []goldenBudget
	Page      int
	PageSize  int
	TotalPage int
	TotalItem int
}

type goldenGetOutput struct {
	Budget goldenBudget
}

type goldenSummary struct {
	CurrentCost  float64
	ForecastCost float64
}

type goldenSeriesPoint struct {
	Date string
	Cost float64
}

type goldenNestedObject struct {
	Summary   goldenSummary
	Series    []goldenSeriesPoint
	StartDate string
	EndDate   string
}

type goldenEmbedded struct {
	goldenBase
	Status string
}

type goldenAnyItem struct {
	Name  string
	Extra any
}

type goldenAnyFields struct {
	Items []goldenAnyItem
}

type goldenNulls struct {
	Name   string
	Ptr    *string
	Nested *goldenBase
	Tags   []string
}

func goldenPath(name string) string {
	return filepath.Join("testdata", "golden", name)
}

// checkGolden renders out through the full pipeline (encode, optional
// --query, then format) and compares it against testdata/golden/name. Run
// with -update to write the current output as the new expected file, after
// reviewing it: these files define the CLI's table and text formats, which
// this package designs itself, rather than reproducing an existing
// contract.
func checkGolden(t *testing.T, name, format, query string, out any) {
	t.Helper()
	var buf bytes.Buffer
	if err := renderOutput(&buf, format, query, out, false); err != nil {
		t.Fatalf("renderOutput: %v", err)
	}
	got := buf.Bytes()

	path := goldenPath(name)
	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v (run with -update to create it)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: got:\n%s\nwant:\n%s", name, got, want)
	}
}

func goldenPagedListValue() *goldenPagedList {
	return &goldenPagedList{
		Items: []goldenBudget{
			{goldenBase{UUID: "b-1", Name: "monthly"}, 50},
			{goldenBase{UUID: "b-2", Name: "quarterly"}, 150},
		},
		Page:      1,
		PageSize:  10,
		TotalPage: 1,
		TotalItem: 2,
	}
}

func TestGoldenPagedList(t *testing.T) {
	v := goldenPagedListValue()
	checkGolden(t, "paged-list.json.golden", "json", "", v)
	checkGolden(t, "paged-list.table.golden", "table", "", v)
	checkGolden(t, "paged-list.text.golden", "text", "", v)
}

func TestGoldenGet(t *testing.T) {
	v := &goldenGetOutput{Budget: goldenBudget{goldenBase{UUID: "b-1", Name: "monthly"}, 50}}
	checkGolden(t, "get.json.golden", "json", "", v)
	checkGolden(t, "get.table.golden", "table", "", v)
	checkGolden(t, "get.text.golden", "text", "", v)
}

func TestGoldenNestedObject(t *testing.T) {
	v := &goldenNestedObject{
		Summary:   goldenSummary{CurrentCost: 120.5, ForecastCost: 200},
		Series:    []goldenSeriesPoint{{Date: "2026-01-01", Cost: 10.25}, {Date: "2026-01-02", Cost: 20}},
		StartDate: "2026-01-01",
		EndDate:   "2026-01-31",
	}
	checkGolden(t, "nested-object.json.golden", "json", "", v)
	checkGolden(t, "nested-object.table.golden", "table", "", v)
	checkGolden(t, "nested-object.text.golden", "text", "", v)
}

func TestGoldenEmbeddedStruct(t *testing.T) {
	v := &goldenEmbedded{goldenBase: goldenBase{UUID: "e-1", Name: "widget"}, Status: "ACTIVE"}
	checkGolden(t, "embedded-struct.json.golden", "json", "", v)
	checkGolden(t, "embedded-struct.table.golden", "table", "", v)
	checkGolden(t, "embedded-struct.text.golden", "text", "", v)
}

func TestGoldenAnyFields(t *testing.T) {
	v := &goldenAnyFields{Items: []goldenAnyItem{
		{Name: "a", Extra: "a string"},
		{Name: "b", Extra: 42.0},
		{Name: "c", Extra: nil},
	}}
	checkGolden(t, "any-fields.json.golden", "json", "", v)
	checkGolden(t, "any-fields.table.golden", "table", "", v)
	checkGolden(t, "any-fields.text.golden", "text", "", v)
}

func TestGoldenNulls(t *testing.T) {
	v := &goldenNulls{Name: "widget"}
	checkGolden(t, "nulls.json.golden", "json", "", v)
	checkGolden(t, "nulls.table.golden", "table", "", v)
	checkGolden(t, "nulls.text.golden", "text", "", v)
}

func TestGoldenNumericFilter(t *testing.T) {
	v := goldenPagedListValue()
	query := "Items[?LimitAmount > `100`]"
	checkGolden(t, "numeric-filter.json.golden", "json", query, v)
	checkGolden(t, "numeric-filter.table.golden", "table", query, v)
	checkGolden(t, "numeric-filter.text.golden", "text", query, v)
}

func TestGoldenProjection(t *testing.T) {
	v := goldenPagedListValue()
	query := "Items[].Name"
	checkGolden(t, "projection.json.golden", "json", query, v)
	checkGolden(t, "projection.table.golden", "table", query, v)
	checkGolden(t, "projection.text.golden", "text", query, v)
}

type goldenControlChars struct {
	Name string
}

func TestGoldenControlCharsInStringCellsAreEscaped(t *testing.T) {
	v := &goldenControlChars{Name: "a\tb\nc\rd\x1bE"}
	checkGolden(t, "control-chars.json.golden", "json", "", v)
	checkGolden(t, "control-chars.table.golden", "table", "", v)
	checkGolden(t, "control-chars.text.golden", "text", "", v)
}

func TestEscapeControlChars(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain", "plain"},
		{"a\tb", `a\tb`},
		{"a\nb", `a\nb`},
		{"a\rb", `a\rb`},
		{"a\x1bb", `a\x1bb`},
		{"a\x00b", `a\x00b`},
		{"a\x7fb", `a\x7fb`},
		{"", ""},
	}
	for _, tt := range tests {
		if got := escapeControlChars(tt.in); got != tt.want {
			t.Errorf("escapeControlChars(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRenderOutputRejectsBadFormat(t *testing.T) {
	var buf bytes.Buffer
	err := renderOutput(&buf, "yaml", "", &goldenGetOutput{}, false)
	if err == nil {
		t.Fatalf("expected an error for an unsupported --output value")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
}

func TestRenderOutputQueryCompileErrorIsAUsageError(t *testing.T) {
	var buf bytes.Buffer
	err := renderOutput(&buf, "json", "Items[", &goldenGetOutput{}, false)
	if err == nil {
		t.Fatalf("expected an error for a malformed --query")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
	if classify(err).Code != "InvalidUsage" {
		t.Fatalf("Code = %q, want InvalidUsage", classify(err).Code)
	}
}

func TestRenderOutputRuntimeQueryFailureIsQueryFailed(t *testing.T) {
	var buf bytes.Buffer
	// A JMESPath expression that raises a runtime type error: comparing a
	// string is fine, but calling a numeric function on one is not.
	err := renderOutput(&buf, "json", "abs(Budget.Name)", &goldenGetOutput{Budget: goldenBudget{goldenBase{Name: "x"}, 1}}, true)
	if err == nil {
		t.Fatalf("expected a runtime query error")
	}
	if exitCode(err) != 1 {
		t.Fatalf("exitCode = %d, want 1 (the operation itself already succeeded)", exitCode(err))
	}
	if classify(err).Code != "QueryFailed" {
		t.Fatalf("Code = %q, want QueryFailed", classify(err).Code)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("write succeeded")) {
		t.Fatalf("error = %q, want it to say the write succeeded", err.Error())
	}
}

func TestRenderOutputTerminalUnsafeRunes(t *testing.T) {
	const input = "Việt Nam\u009b2J\u0085\u202e\u2066\x7f\t\n\r\x00<&\\u009b"
	const text = `Việt Nam\x9b2J\x85\u202e\u2066\x7f\t\n\r\x00<&\u009b`
	const jsonText = `"Việt Nam\u009b2J\u0085\u202e\u2066\u007f\t\n\r\u0000\u003c\u0026\\u009b"`
	for _, query := range []string{"", "Name"} {
		for _, format := range []string{outputText, outputTable, outputJSON} {
			t.Run(format+"/"+query, func(t *testing.T) {
				var buf bytes.Buffer
				if err := renderOutput(&buf, format, query, &goldenControlChars{Name: input}, false); err != nil {
					t.Fatal(err)
				}
				switch format {
				case outputJSON:
					want := jsonText + "\n"
					if query == "" {
						want = "{\n  \"Name\": " + jsonText + "\n}\n"
					}
					if buf.String() != want {
						t.Fatalf("output = %q, want %q", buf.String(), want)
					}
					var decoded any
					if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
						t.Fatal(err)
					}
					if query == "" {
						decoded = decoded.(map[string]any)["Name"]
					}
					if decoded != input {
						t.Fatalf("decoded = %q, want %q", decoded, input)
					}
				case outputText:
					if buf.String() != text+"\n" {
						t.Fatalf("output = %q", buf.String())
					}
				case outputTable:
					if !strings.Contains(buf.String(), "| "+text+" |\n") {
						t.Fatalf("output = %q", buf.String())
					}
				}
			})
		}
	}
}

func TestEscapeControlCharsCoveredSet(t *testing.T) {
	for r := rune(0); r <= 0x2069; r++ {
		covered := r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0x061c || r == 0x200e || r == 0x200f ||
			(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
		want := string(r)
		if covered {
			switch r {
			case '\t':
				want = `\t`
			case '\n':
				want = `\n`
			case '\r':
				want = `\r`
			default:
				if r <= 0xff {
					want = fmt.Sprintf(`\x%02x`, r)
				} else {
					want = fmt.Sprintf(`\u%04x`, r)
				}
			}
		}
		if got := escapeControlChars(string(r)); got != want {
			t.Errorf("rune %U: got %q, want %q", r, got, want)
		}
	}
}

func TestRenderOutputUnsafeKeysAndNestedStrings(t *testing.T) {
	const value = "Việt Nam\u009b2J\u0085\u202e\u2066\x7f"
	for _, format := range []string{outputJSON, outputText, outputTable} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			if err := renderOutput(&buf, format, "", map[string]any{value: []string{value}}, false); err != nil {
				t.Fatal(err)
			}
			for _, r := range []rune{0x9b, 0x85, 0x202e, 0x2066, 0x7f} {
				if strings.ContainsRune(buf.String(), r) {
					t.Errorf("output contains %U", r)
				}
			}
			if !strings.Contains(buf.String(), "Việt Nam") {
				t.Fatal("ordinary Unicode changed")
			}
			if format == outputJSON {
				var decoded map[string][]string
				if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
					t.Fatal(err)
				}
				if len(decoded[value]) != 1 || decoded[value][0] != value {
					t.Fatalf("decoded = %q", decoded)
				}
			}
		})
	}
}
