package cdn

import (
	"errors"
	"os"
	"testing"
)

// wantIPRanges is the exact 19 canonical CIDRs testdata/cdn/faq-vcdn.html
// names, sorted by address then prefix length, with the 21-token, 19-unique
// duplicates and the ";" separator already resolved.
var wantIPRanges = []string{
	"14.225.2.32/28",
	"14.225.10.64/28",
	"42.115.221.64/27",
	"42.115.221.128/27",
	"43.239.149.128/28",
	"61.28.226.48/28",
	"61.28.231.126/32",
	"113.164.14.192/27",
	"113.164.15.32/28",
	"113.164.15.80/29",
	"113.164.241.176/28",
	"118.69.83.64/27",
	"118.69.83.160/28",
	"118.69.84.64/28",
	"171.244.16.224/27",
	"171.244.28.64/27",
	"171.244.128.0/27",
	"210.245.26.0/24",
	"210.245.38.64/27",
}

func TestParseIPRangesFixture(t *testing.T) {
	body, err := os.ReadFile("../testdata/cdn/faq-vcdn.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	got, err := parseIPRanges(body)
	if err != nil {
		t.Fatalf("parseIPRanges() error = %v", err)
	}
	if len(got) != len(wantIPRanges) {
		t.Fatalf("got %d ranges, want %d: %v", len(got), len(wantIPRanges), got)
	}
	for i, want := range wantIPRanges {
		if got[i] != want {
			t.Fatalf("range[%d] = %q, want %q (full: %v)", i, got[i], want, got)
		}
	}
}

func TestParseIPRangesTableErrors(t *testing.T) {
	cases := []struct {
		name string
		file string
	}{
		{"heading missing", "heading-missing.html"},
		{"heading matched twice", "heading-duplicate.html"},
		{"no heading after the section", "no-heading-after.html"},
		{"section with no CIDR", "section-no-cidr.html"},
		{"bare address", "bare-address.html"},
		{"invalid length", "invalid-length.html"},
		{"host bits set", "host-bits-set.html"},
		{"heading found only inside script", "heading-only-in-script.html"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := os.ReadFile("../testdata/cdn/" + tc.file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			_, err = parseIPRanges(body)
			if !errors.Is(err, ErrPageFormat) {
				t.Fatalf("parseIPRanges() error = %v, want ErrPageFormat", err)
			}
		})
	}
}

// TestParseIPRangesIPv6CIDR checks that a valid, already-masked IPv6 CIDR in
// the section is accepted alongside an IPv4 one and sorted after it:
// netip.Addr.Compare orders every IPv4 address before every IPv6 address.
func TestParseIPRangesIPv6CIDR(t *testing.T) {
	page := `<html><body>
<h3>CDN IP range</h3>
<p>1.2.3.0/24 2001:db8::/32</p>
<h3>Next</h3>
</body></html>`

	got, err := parseIPRanges([]byte(page))
	if err != nil {
		t.Fatalf("parseIPRanges() error = %v", err)
	}
	want := []string{"1.2.3.0/24", "2001:db8::/32"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestParseIPRangesEntityEncodedHeading checks that an entity-encoded
// heading (as the real page uses for its apostrophe and quote) still
// matches after decoding.
func TestParseIPRangesEntityEncodedHeading(t *testing.T) {
	page := `<html><body>
<h3>&quot;CDN IP range&#x27;s FAQ</h3>
<p>1.2.3.0/24</p>
<h3>Next</h3>
</body></html>`

	got, err := parseIPRanges([]byte(page))
	if err != nil {
		t.Fatalf("parseIPRanges() error = %v", err)
	}
	if len(got) != 1 || got[0] != "1.2.3.0/24" {
		t.Fatalf("got %v, want [1.2.3.0/24]", got)
	}
}

// TestTextOfReplacesTagsWithSpace checks that textOf replaces each tag with
// a space rather than deleting it, so text on either side of a tag never
// glues into one token: "Ranges:" and "1.2.3.0/24" here come from separate
// <p> elements with no space between the tags in the source.
func TestTextOfReplacesTagsWithSpace(t *testing.T) {
	got := textOf(`<p>Ranges:</p><p>1.2.3.0/24</p> 5.6.7.0/24`)
	want := "Ranges: 1.2.3.0/24 5.6.7.0/24"
	if got != want {
		t.Fatalf("textOf() = %q, want %q", got, want)
	}
}

// TestTextOfSeparatesAdjacentCellText checks the same rule for a table row,
// the shape the real FAQ page never uses for its CIDR list but that a future
// revision of the page might.
func TestTextOfSeparatesAdjacentCellText(t *testing.T) {
	got := textOf(`<td>HN</td><td>1.2.3.0/24</td>`)
	want := "HN 1.2.3.0/24"
	if got != want {
		t.Fatalf("textOf() = %q, want %q", got, want)
	}
}

// TestParseIPRangesFindsBothRangesAcrossTags checks the full pipeline for
// the same shape as TestTextOfReplacesTagsWithSpace: both CIDRs are found,
// and the text glued by a missing space between two tags never happens.
func TestParseIPRangesFindsBothRangesAcrossTags(t *testing.T) {
	page := `<html><body>
<h3>CDN IP range</h3>
<p>Ranges:</p><p>1.2.3.0/24</p> 5.6.7.0/24
<h3>Next</h3>
</body></html>`

	got, err := parseIPRanges([]byte(page))
	if err != nil {
		t.Fatalf("parseIPRanges() error = %v", err)
	}
	want := []string{"1.2.3.0/24", "5.6.7.0/24"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestParseIPRangesCommentAndQuotedAttributeAddNothing checks that an HTML
// comment is removed whole before any tag search, and that a tag ends only
// at a '>' outside quotes: without both, "old > 6.6.6.0/24" and
// "a>b 7.7.7.0/24" (a decoy CIDR inside the comment and inside a quoted
// attribute value) would leak into the section's visible text and be
// counted as ranges.
func TestParseIPRangesCommentAndQuotedAttributeAddNothing(t *testing.T) {
	page := `<html><body>
<h3>CDN IP range</h3>
<!-- old > 6.6.6.0/24 -->
<span title="a>b 7.7.7.0/24">1.2.3.0/24</span>
<h3>Next</h3>
</body></html>`

	got, err := parseIPRanges([]byte(page))
	if err != nil {
		t.Fatalf("parseIPRanges() error = %v", err)
	}
	want := []string{"1.2.3.0/24"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestParseIPRangesGluedIPv4Fails checks that a token holding four
// dot-separated digit groups anywhere, but not IPv4-shaped as a whole, fails
// the call instead of being silently ignored: a CIDR glued to a two-letter
// code or a URL scheme must never disappear from the result.
func TestParseIPRangesGluedIPv4Fails(t *testing.T) {
	cases := []struct {
		name string
		page string
	}{
		// Each page also holds a genuinely valid CIDR, so a parser that
		// merely ignores the malformed token (rather than failing on it)
		// would otherwise succeed here instead of failing as it must.
		{"glued prefix", "<html><body><h3>CDN IP range</h3><p>HN1.2.3.0/24 9.9.9.0/24</p><h3>Next</h3></body></html>"},
		{"url scheme prefix", "<html><body><h3>CDN IP range</h3><p>https://1.2.3.0/24 9.9.9.0/24</p><h3>Next</h3></body></html>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseIPRanges([]byte(tc.page))
			if !errors.Is(err, ErrPageFormat) {
				t.Fatalf("parseIPRanges() error = %v, want ErrPageFormat", err)
			}
		})
	}
}

// TestParseIPRangesH4InsideSectionDoesNotEndIt checks that the section ends
// only at the next heading of the same or higher level: an <h4> nested
// inside an <h3> section must not end it, so the section still reaches the
// next <h3>.
func TestParseIPRangesH4InsideSectionDoesNotEndIt(t *testing.T) {
	page := `<html><body>
<h3>CDN IP range</h3>
<h4>Sub heading</h4>
<p>1.2.3.0/24</p>
<h3>Next</h3>
</body></html>`

	got, err := parseIPRanges([]byte(page))
	if err != nil {
		t.Fatalf("parseIPRanges() error = %v", err)
	}
	if len(got) != 1 || got[0] != "1.2.3.0/24" {
		t.Fatalf("got %v, want [1.2.3.0/24]", got)
	}
}

// TestParseIPRangesBadTokenMessageIsBounded checks that a parse error names
// the offending token, quoted, and never grows unbounded: the design caps
// it at 64 bytes with control characters removed.
func TestParseIPRangesBadTokenMessageIsBounded(t *testing.T) {
	page := `<html><body>
<h3>CDN IP range</h3>
<p>1.2.3.4/99</p>
<h3>Next</h3>
</body></html>`

	_, err := parseIPRanges([]byte(page))
	if !errors.Is(err, ErrPageFormat) {
		t.Fatalf("error = %v, want ErrPageFormat", err)
	}
	if got := err.Error(); len(got) > 200 {
		t.Fatalf("error message too long (%d bytes): %s", len(got), got)
	}
}
