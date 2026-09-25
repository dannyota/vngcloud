package cli

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
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
