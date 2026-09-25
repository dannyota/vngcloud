package cdn

import (
	"fmt"
	"html"
	"net/netip"
	"sort"
	"strings"
)

// nonContentTags lists elements whose content never reaches the heading or
// CIDR search: script and style payloads, and markup (svg icons) that can
// repeat the heading text or hide characters that would otherwise look like
// a CIDR.
var nonContentTags = []string{"script", "style", "noscript", "template", "svg"}

// heading is one <h1>-<h6> element found in the page, in document order.
type heading struct {
	level int
	text  string
	// startLT is the index of the heading's opening '<', the start of the
	// next heading's search boundary for the previous section.
	startLT int
	// afterCloseTag is the index just after the heading's own closing tag,
	// where the section following it begins.
	afterCloseTag int
}

// parseIPRanges applies the CDN IP range page's strict parsing rules to
// body, the page's raw bytes, and returns the sorted, de-duplicated,
// canonical CIDRs the matched section names. Any doubt about the page's
// shape returns an error wrapping ErrPageFormat.
func parseIPRanges(body []byte) ([]string, error) {
	stripped := stripElements(removeComments(string(body)), nonContentTags)

	headings := findHeadings(stripped)
	matchIdx := -1
	matches := 0
	for i, h := range headings {
		if strings.Contains(strings.ToLower(h.text), strings.ToLower(headingNeedle)) {
			matches++
			matchIdx = i
		}
	}
	switch {
	case matches == 0:
		return nil, fmt.Errorf("%w: heading %q not found", ErrPageFormat, headingNeedle)
	case matches > 1:
		return nil, fmt.Errorf("%w: heading %q matched %d times", ErrPageFormat, headingNeedle, matches)
	}

	matched := headings[matchIdx]
	end := -1
	for i := matchIdx + 1; i < len(headings); i++ {
		if headings[i].level <= matched.level {
			end = i
			break
		}
	}
	if end == -1 {
		return nil, fmt.Errorf("%w: no heading found after the matched section", ErrPageFormat)
	}
	section := stripped[matched.afterCloseTag:headings[end].startLT]

	prefixes, err := extractPrefixes(section)
	if err != nil {
		return nil, err
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("%w: no CIDR found in the matched section", ErrPageFormat)
	}
	return finalizeIPRanges(prefixes), nil
}

// extractPrefixes turns section's rendered text into tokens and parses every
// IPv4- or IPv6-shaped one as a canonical CIDR. A token that looks like a
// CIDR but is not a valid, already-masked one fails the whole call: the
// parser never guesses what the page meant.
func extractPrefixes(section string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, tok := range tokenize(textOf(section)) {
		switch {
		case ipv4Shaped(tok):
			p, err := canonicalPrefix(tok)
			if err != nil {
				return nil, err
			}
			prefixes = append(prefixes, p)
		case ipv6Shaped(tok):
			p, err := canonicalPrefix(tok)
			if err != nil {
				return nil, err
			}
			prefixes = append(prefixes, p)
		case hasEmbeddedDottedQuad(tok):
			return nil, fmt.Errorf("%w: token %s holds a CIDR glued to other text", ErrPageFormat, quoteToken(tok))
		}
	}
	return prefixes, nil
}

// hasEmbeddedDottedQuad reports whether tok contains, anywhere within it, a
// maximal run of digits and '.' that itself splits into four or more
// non-empty groups: four dot-separated digit runs glued to other characters
// that ipv4Shaped's whole-token check rejects, such as a two-letter code
// (HN1.2.3.0/24) or a URL scheme (https://1.2.3.0/24). ipv4Shaped already
// accepts the token when the whole address part has this shape; this instead
// catches the same shape hiding inside a larger token, so extractPrefixes can
// fail loudly on it instead of silently ignoring it.
func hasEmbeddedDottedQuad(tok string) bool {
	for i := 0; i < len(tok); {
		if !isDigitOrDot(tok[i]) {
			i++
			continue
		}
		j := i
		for j < len(tok) && isDigitOrDot(tok[j]) {
			j++
		}
		if maxDottedGroups(tok[i:j]) >= 4 {
			return true
		}
		i = j
	}
	return false
}

func isDigitOrDot(c byte) bool {
	return (c >= '0' && c <= '9') || c == '.'
}

// maxDottedGroups returns the length of the longest run of consecutive
// non-empty, dot-separated groups in run, a string holding only digits and
// '.'. Two adjacent dots, or a leading or trailing one, produce an empty
// group that breaks the run.
func maxDottedGroups(run string) int {
	best, cur := 0, 0
	for _, group := range strings.Split(run, ".") {
		if group == "" {
			cur = 0
			continue
		}
		cur++
		if cur > best {
			best = cur
		}
	}
	return best
}

// canonicalPrefix parses tok as a CIDR and requires it to already be in
// canonical (masked) form: a bare address (ParsePrefix rejects it outright),
// a bad prefix length, or a prefix with host bits set all fail here.
func canonicalPrefix(tok string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(tok)
	if err != nil || p != p.Masked() {
		return netip.Prefix{}, fmt.Errorf("%w: invalid CIDR %s", ErrPageFormat, quoteToken(tok))
	}
	return p, nil
}

// finalizeIPRanges removes duplicate prefixes, sorts what remains by
// address then prefix length, and formats each with Prefix.String().
func finalizeIPRanges(prefixes []netip.Prefix) []string {
	seen := make(map[netip.Prefix]bool, len(prefixes))
	uniq := make([]netip.Prefix, 0, len(prefixes))
	for _, p := range prefixes {
		if seen[p] {
			continue
		}
		seen[p] = true
		uniq = append(uniq, p)
	}
	sort.Slice(uniq, func(i, j int) bool {
		a, b := uniq[i], uniq[j]
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c < 0
		}
		return a.Bits() < b.Bits()
	})
	out := make([]string, len(uniq))
	for i, p := range uniq {
		out[i] = p.String()
	}
	return out
}

// quoteToken quotes tok for an error message, with control characters
// removed and cut to 64 bytes, so an error never grows large or holds
// unprintable input from the page.
func quoteToken(tok string) string {
	var b strings.Builder
	for i := 0; i < len(tok) && b.Len() < 64; i++ {
		c := tok[i]
		if c < 0x20 || c == 0x7f {
			continue
		}
		b.WriteByte(c)
	}
	return fmt.Sprintf("%q", b.String())
}

// tokenize splits s on every character that is not an ASCII letter, digit,
// '.', ':', or '/', then trims a trailing '.' or ':' from each piece, so a
// CIDR that ends a sentence still counts. Empty results, which trimming can
// produce from a token that was only punctuation, are dropped.
func tokenize(s string) []string {
	raw := strings.FieldsFunc(s, func(r rune) bool {
		return !isTokenRune(r)
	})
	out := raw[:0]
	for _, f := range raw {
		f = strings.TrimRight(f, ".:")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func isTokenRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '.', r == ':', r == '/':
		return true
	default:
		return false
	}
}

// ipv4Shaped reports whether tok looks like an IPv4 address, with or without
// a "/n" suffix: its address part is four dot-separated all-digit groups.
// The suffix, if present, is validated later by netip.ParsePrefix.
func ipv4Shaped(tok string) bool {
	addr := tok
	if i := strings.IndexByte(tok, '/'); i >= 0 {
		addr = tok[:i]
	}
	groups := strings.Split(addr, ".")
	if len(groups) != 4 {
		return false
	}
	for _, g := range groups {
		if !isAllDigits(g) {
			return false
		}
	}
	return true
}

// ipv6Shaped reports whether tok looks like an IPv6 CIDR: it contains a "/"
// and at least two ":".
func ipv6Shaped(tok string) bool {
	return strings.Contains(tok, "/") && strings.Count(tok, ":") >= 2
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// textOf renders s as visible text: every "<...>" span is replaced with a
// single space, so text from two adjacent elements (for example two table
// cells with no whitespace between their tags in the source) never glues
// into one token, then HTML entities are decoded and runs of whitespace
// collapse to one space. It is not a full HTML parser: it does not track
// element nesting, but tagEnd finds each tag's own closing '>' correctly
// even when a quoted attribute value holds a literal '>'.
func textOf(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '<' {
			end := tagEnd(s, i)
			if end == -1 {
				// The rest of s is inside an unterminated tag: no more text
				// follows.
				break
			}
			b.WriteByte(' ')
			i = end + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return strings.Join(strings.Fields(html.UnescapeString(b.String())), " ")
}

// tagEnd returns the index of the '>' that closes the tag starting at
// s[start] (s[start] must be '<'), treating a '>' inside a single- or
// double-quoted attribute value as ordinary text rather than the tag's end.
// It returns -1 when no such '>' exists before the document ends.
func tagEnd(s string, start int) int {
	var quote byte
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i
		}
	}
	return -1
}

// removeComments removes every "<!--...-->" span from s whole, including its
// content, before any other parsing step: an HTML comment can hold a literal
// '>' that is not inside a quote, so tagEnd's rule for every other kind of
// tag does not find a comment's real end. A comment left unterminated by the
// document's end removes everything from its start onward, the same
// convention stripElement and findHeadings use for an unterminated tag.
func removeComments(s string) string {
	const open, closeTag = "<!--", "-->"
	var b strings.Builder
	pos := 0
	for {
		start := strings.Index(s[pos:], open)
		if start == -1 {
			b.WriteString(s[pos:])
			break
		}
		start += pos
		b.WriteString(s[pos:start])
		rest := s[start+len(open):]
		end := strings.Index(rest, closeTag)
		if end == -1 {
			break
		}
		pos = start + len(open) + end + len(closeTag)
	}
	return b.String()
}

// isTagNameEnd reports whether c can follow a tag name: whitespace, the tag
// close, or the self-closing slash. It stops "<h30" from matching "<h3", and
// "<styles" from matching "<style".
func isTagNameEnd(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '>', '/':
		return true
	default:
		return false
	}
}

// findHeadings scans s in document order for every <h1>-<h6> element,
// recording each one's level, rendered text, and the byte ranges needed to
// bound the section after it (see heading's field docs). A heading with no
// closing tag before the end of the document has its text and section start
// run to the end of s.
func findHeadings(s string) []heading {
	var out []heading
	pos := 0
	for pos < len(s) {
		level, tagStart, openEnd, ok := nextHeadingOpen(s, pos)
		if !ok {
			break
		}
		closeStart, closeEnd := headingClose(s, openEnd, level)
		textEnd, next := closeStart, closeEnd
		if closeStart == -1 {
			textEnd, next = len(s), len(s)
		}
		out = append(out, heading{
			level:         level,
			text:          textOf(s[openEnd:textEnd]),
			startLT:       tagStart,
			afterCloseTag: next,
		})
		if next <= pos {
			break
		}
		pos = next
	}
	return out
}

// nextHeadingOpen finds the next <h1>-<h6> opening tag at or after from. It
// returns the heading level (1-6), the index of '<', the index just after
// the opening tag's '>', and whether one was found.
func nextHeadingOpen(s string, from int) (level, tagStart, openEnd int, ok bool) {
	for i := from; i+2 < len(s); i++ {
		if s[i] != '<' || (s[i+1]|0x20) != 'h' {
			continue
		}
		c := s[i+2]
		if c < '1' || c > '6' {
			continue
		}
		if i+3 < len(s) && !isTagNameEnd(s[i+3]) {
			continue
		}
		end := tagEnd(s, i)
		if end == -1 {
			return 0, 0, 0, false
		}
		return int(c - '0'), i, end + 1, true
	}
	return 0, 0, 0, false
}

// headingClose finds the </hN> close tag matching a heading of the given
// level, opened at openEnd. It returns the index of the close tag's '<' and
// the index just after its '>', or -1, -1 when the document ends first.
func headingClose(s string, openEnd, level int) (closeStart, closeEnd int) {
	want := fmt.Sprintf("</h%d", level)
	for i := openEnd; i+len(want) <= len(s); i++ {
		if s[i] != '<' || !strings.EqualFold(s[i:i+len(want)], want) {
			continue
		}
		after := i + len(want)
		if after < len(s) && !isTagNameEnd(s[after]) {
			continue
		}
		end := tagEnd(s, i)
		if end == -1 {
			return -1, -1
		}
		return i, end + 1
	}
	return -1, -1
}

// stripElements removes every element named in tags from s, tag and content
// together, so their text never reaches the heading or CIDR search.
func stripElements(s string, tags []string) string {
	for _, tag := range tags {
		s = stripElement(s, tag)
	}
	return s
}

// stripElement removes every <tag>...</tag> span from s, including a
// self-closing <tag/> with no content to remove. It assumes tag never
// nests a same-named element, true of every entry in nonContentTags.
func stripElement(s, tag string) string {
	var b strings.Builder
	pos := 0
	for {
		start, openEnd, selfClosing, ok := findOpenTag(s, tag, pos)
		if !ok {
			b.WriteString(s[pos:])
			break
		}
		b.WriteString(s[pos:start])
		if selfClosing {
			pos = openEnd
			continue
		}
		closeEnd := findCloseTagEnd(s, tag, openEnd)
		if closeEnd == -1 {
			// No matching close tag before the document ends: everything
			// from the open tag onward is inside the element, so nothing
			// more of s is written to b.
			break
		}
		pos = closeEnd
	}
	return b.String()
}

// findOpenTag finds the next case-insensitive "<tag" at or after from. It
// returns the index of '<', the index just after the tag's closing '>', and
// whether that '>' was immediately preceded by '/' (a self-closing tag).
func findOpenTag(s, tag string, from int) (start, openEnd int, selfClosing, ok bool) {
	open := "<" + tag
	for i := from; i+len(open) <= len(s); i++ {
		if s[i] != '<' || !strings.EqualFold(s[i:i+len(open)], open) {
			continue
		}
		after := i + len(open)
		if after < len(s) && !isTagNameEnd(s[after]) {
			continue
		}
		gt := tagEnd(s, i)
		if gt == -1 {
			return 0, 0, false, false
		}
		end := gt + 1
		selfClosing = gt > i && s[gt-1] == '/'
		return i, end, selfClosing, true
	}
	return 0, 0, false, false
}

// findCloseTagEnd finds the next case-insensitive "</tag" at or after from
// and returns the index just after its '>', or -1 if the document ends
// first.
func findCloseTagEnd(s, tag string, from int) int {
	closeTag := "</" + tag
	for i := from; i+len(closeTag) <= len(s); i++ {
		if s[i] != '<' || !strings.EqualFold(s[i:i+len(closeTag)], closeTag) {
			continue
		}
		after := i + len(closeTag)
		if after < len(s) && !isTagNameEnd(s[after]) {
			continue
		}
		end := tagEnd(s, i)
		if end == -1 {
			return -1
		}
		return end + 1
	}
	return -1
}
