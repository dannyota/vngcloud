package ini

import (
	"strconv"
	"strings"
	"testing"
)

func TestParseSectionsAndKeys(t *testing.T) {
	src := "[default]\nregion = hcm-3\nproject_id = proj-1\n\n[profile dev]\nregion = han-1\n"
	f, err := Parse("test", strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	def, ok := f.Section("default")
	if !ok {
		t.Fatal("default section missing")
	}
	if def["region"] != "hcm-3" || def["project_id"] != "proj-1" {
		t.Fatalf("unexpected default section: %#v", def)
	}
	dev, ok := f.Section("profile dev")
	if !ok {
		t.Fatal("profile dev section missing")
	}
	if dev["region"] != "han-1" {
		t.Fatalf("unexpected dev section: %#v", dev)
	}
}

func TestParseUnknownSectionMissing(t *testing.T) {
	f, err := Parse("test", strings.NewReader("[default]\nregion = hcm-3\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if _, ok := f.Section("nope"); ok {
		t.Fatal("expected missing section to report false")
	}
}

func TestParseStripsLeadingBOM(t *testing.T) {
	bom := string([]byte{0xEF, 0xBB, 0xBF})
	src := bom + "[default]\nregion = hcm-3\n"
	f, err := Parse("test", strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	def, ok := f.Section("default")
	if !ok || def["region"] != "hcm-3" {
		t.Fatalf("BOM not stripped: %#v ok=%v", def, ok)
	}
}

func TestParseKeysLowercased(t *testing.T) {
	f, err := Parse("test", strings.NewReader("[default]\nRegion = hcm-3\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	def, _ := f.Section("default")
	if def["region"] != "hcm-3" {
		t.Fatalf("key not lowercased: %#v", def)
	}
	if _, ok := def["Region"]; ok {
		t.Fatal("original-case key should not also be present")
	}
}

func TestParseValueKeepsInnerSpacesAndEquals(t *testing.T) {
	f, err := Parse("test", strings.NewReader("[default]\nkey =  a = b  c \n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	def, _ := f.Section("default")
	if def["key"] != "a = b  c" {
		t.Fatalf("value = %q", def["key"])
	}
}

func TestParseBlankLinesSkipped(t *testing.T) {
	src := "[default]\n\n\nregion = hcm-3\n\n"
	f, err := Parse("test", strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	def, _ := f.Section("default")
	if len(def) != 1 || def["region"] != "hcm-3" {
		t.Fatalf("unexpected section: %#v", def)
	}
}

func TestParseFullLineComments(t *testing.T) {
	src := "# leading comment\n[default]\n  # indented comment\n; semicolon comment\nregion = hcm-3\n"
	f, err := Parse("test", strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	def, _ := f.Section("default")
	if len(def) != 1 || def["region"] != "hcm-3" {
		t.Fatalf("unexpected section: %#v", def)
	}
}

func TestParseRepeatedKeyKeepsLastValue(t *testing.T) {
	f, err := Parse("test", strings.NewReader("[default]\nregion = hcm-3\nregion = han-1\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	def, _ := f.Section("default")
	if def["region"] != "han-1" {
		t.Fatalf("region = %q, want han-1 (last wins)", def["region"])
	}
}

func TestParseRepeatedSectionMerges(t *testing.T) {
	src := "[default]\nregion = hcm-3\n[default]\nproject_id = proj-1\n"
	f, err := Parse("test", strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	def, _ := f.Section("default")
	if def["region"] != "hcm-3" || def["project_id"] != "proj-1" {
		t.Fatalf("sections did not merge: %#v", def)
	}
}

func TestParseEmptyValueKept(t *testing.T) {
	f, err := Parse("test", strings.NewReader("[default]\nregion = \n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	def, _ := f.Section("default")
	value, ok := def["region"]
	if !ok || value != "" {
		t.Fatalf("region = %q, ok = %v, want empty string present", value, ok)
	}
}

func TestParseSectionHeaderTrimmed(t *testing.T) {
	f, err := Parse("test", strings.NewReader("[  profile dev  ]\nregion = han-1\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if _, ok := f.Section("profile dev"); !ok {
		t.Fatalf("section header was not trimmed: %#v", f)
	}
}

const (
	secretMarker  = "secret-XYZ"
	errSourceName = "creds"
)

func assertParseError(t *testing.T, line int, src string) {
	t.Helper()
	_, err := Parse(errSourceName, strings.NewReader(src))
	if err == nil {
		t.Fatalf("Parse() error = nil, want an error for %q", src)
	}
	if strings.Contains(err.Error(), secretMarker) {
		t.Fatalf("Parse() error leaked line content: %v", err)
	}
	wantPrefix := errSourceName + ":" + strconv.Itoa(line) + ":"
	if !strings.HasPrefix(err.Error(), wantPrefix) {
		t.Fatalf("Parse() error = %q, want prefix %q", err.Error(), wantPrefix)
	}
}

func TestParseErrorKeyBeforeAnySection(t *testing.T) {
	assertParseError(t, 1, "root_email = "+secretMarker+"\n")
}

func TestParseErrorLineWithoutEquals(t *testing.T) {
	assertParseError(t, 2, "[default]\n"+secretMarker+"\n")
}

func TestParseErrorEmptyKey(t *testing.T) {
	assertParseError(t, 2, "[default]\n = "+secretMarker+"\n")
}

func TestParseErrorUnclosedSection(t *testing.T) {
	assertParseError(t, 1, "[default "+secretMarker+"\n")
}

func TestParseErrorLineWithoutEqualsNotFirstLine(t *testing.T) {
	assertParseError(t, 3, "[default]\nregion = hcm-3\nbroken-line-"+secretMarker+"\n")
}
