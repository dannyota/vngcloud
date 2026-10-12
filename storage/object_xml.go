package storage

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

var (
	errObjectXMLInvalid = errors.New("storage: invalid object XML response")
	errObjectXMLRead    = errors.New("storage: object XML response read failed")
	errObjectXMLLimit   = errors.New("storage: object XML response exceeds byte limit")
)

type objectXMLDecoder struct {
	decoder  *xml.Decoder
	syntax   *objectXMLTokenReader
	reader   *objectXMLReader
	roots    []string
	depth    int
	started  bool
	ended    bool
	initial  bool
	terminal error
}

func newObjectXMLDecoder(r io.Reader, maxBytes int64, roots ...string) *objectXMLDecoder {
	d := &objectXMLDecoder{roots: slices.Clone(roots), initial: true}
	if r == nil || maxBytes < 0 {
		d.terminal = errObjectXMLInvalid
		return d
	}
	d.reader = &objectXMLReader{source: r, remaining: maxBytes}
	d.syntax = &objectXMLTokenReader{decoder: xml.NewDecoder(d.reader)}
	d.decoder = xml.NewTokenDecoder(d.syntax)
	return d
}

func (d *objectXMLDecoder) Token() (xml.Token, error) {
	if d.terminal != nil {
		return nil, d.terminal
	}
	token, err := d.decoder.Token()
	// A read error delivered with bytes must not become a successful result,
	// even if the XML decoder buffered a complete token from those bytes.
	if d.reader.terminal != nil && !errors.Is(d.reader.terminal, io.EOF) {
		return d.finish(d.reader.terminal)
	}
	if err != nil {
		if errors.Is(err, io.EOF) && d.ended && d.depth == 0 {
			return d.finish(io.EOF)
		}
		return d.finish(errObjectXMLInvalid)
	}
	switch token := token.(type) {
	case xml.StartElement:
		if d.ended || d.depth == 16 {
			return d.finish(errObjectXMLInvalid)
		}
		// encoding/xml permits duplicates. Compare ordinary attributes by
		// resolved name and namespace declarations by their original name.
		seen := make(map[xml.Name]struct{}, len(token.Attr))
		declarations := make(map[xml.Name]struct{})
		for i, attr := range token.Attr {
			name, names := attr.Name, seen
			raw := d.syntax.attributes[i]
			if raw.Space == "xmlns" || (raw.Space == "" && raw.Local == "xmlns") {
				name, names = raw, declarations
			}
			if _, duplicate := names[name]; duplicate {
				return d.finish(errObjectXMLInvalid)
			}
			names[name] = struct{}{}
		}
		if d.depth == 0 {
			if d.started || !slices.Contains(d.roots, token.Name.Local) ||
				(token.Name.Space != "" && token.Name.Space != "http://s3.amazonaws.com/doc/2006-03-01/") {
				return d.finish(errObjectXMLInvalid)
			}
			d.started = true
		}
		d.depth++
	case xml.EndElement:
		d.depth--
		if d.depth == 0 {
			d.ended = true
		}
	case xml.CharData:
		if !utf8.Valid(token) || (d.depth == 0 && !objectXMLWhitespace(token)) {
			return d.finish(errObjectXMLInvalid)
		}
	case xml.Comment:
		// encoding/xml does not validate UTF-8 within comments.
		if !utf8.Valid(token) {
			return d.finish(errObjectXMLInvalid)
		}
	case xml.ProcInst:
		if !d.initial || token.Target != "xml" || !objectXMLDeclaration(token.Inst) {
			return d.finish(errObjectXMLInvalid)
		}
	case xml.Directive:
		return d.finish(errObjectXMLInvalid)
	}
	d.initial = false
	return token, nil
}

func (d *objectXMLDecoder) finish(err error) (xml.Token, error) {
	d.terminal = err
	// Drop the parser on failure so its raw syntax error cannot be retained.
	d.decoder, d.syntax, d.reader, d.roots = nil, nil, nil, nil
	return nil, err
}

// Retain raw attribute names before TokenDecoder resolves prefixes. A
// namespace declaration must stay distinct from an ordinary attribute even
// when that attribute's namespace name happens to be "xmlns".
type objectXMLTokenReader struct {
	decoder    *xml.Decoder
	attributes []xml.Name
}

func (r *objectXMLTokenReader) Token() (xml.Token, error) {
	token, err := r.decoder.RawToken()
	if start, ok := token.(xml.StartElement); ok {
		r.attributes = make([]xml.Name, len(start.Attr))
		for i, attr := range start.Attr {
			r.attributes[i] = attr.Name
		}
	}
	return token, err
}

func objectXMLWhitespace(data []byte) bool {
	for _, b := range data {
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			return false
		}
	}
	return true
}

// encoding/xml treats a declaration as a processing instruction and does
// not enforce its grammar. Only XML 1.0 and UTF-8 are supported by this parser.
func objectXMLDeclaration(inst []byte) bool {
	version, rest, ok := objectXMLDeclarationField(string(inst), "version")
	if !ok || version != "1.0" {
		return false
	}
	for _, name := range []string{"encoding", "standalone"} {
		field := strings.TrimLeft(rest, " \t\r\n")
		if field == "" {
			return true
		}
		if field == rest {
			return false
		}
		if !strings.HasPrefix(field, name) {
			continue
		}
		var value string
		value, rest, ok = objectXMLDeclarationField(field, name)
		if !ok || (name == "encoding" && !strings.EqualFold(value, "UTF-8")) ||
			(name == "standalone" && value != "yes" && value != "no") {
			return false
		}
	}
	return strings.TrimLeft(rest, " \t\r\n") == ""
}

func objectXMLDeclarationField(text, name string) (value, rest string, ok bool) {
	rest, ok = strings.CutPrefix(text, name)
	if !ok {
		return "", "", false
	}
	rest = strings.TrimLeft(rest, " \t\r\n")
	rest, ok = strings.CutPrefix(rest, "=")
	if !ok {
		return "", "", false
	}
	rest = strings.TrimLeft(rest, " \t\r\n")
	if len(rest) == 0 || (rest[0] != '\'' && rest[0] != '"') {
		return "", "", false
	}
	end := strings.IndexByte(rest[1:], rest[0])
	if end < 0 {
		return "", "", false
	}
	return rest[1 : end+1], rest[end+2:], true
}

// objectXMLReader caps underlying reads without adding one to a possibly
// maximal int64 limit. Only the overflow probe can consume an extra byte.
type objectXMLReader struct {
	source    io.Reader
	remaining int64
	terminal  error
}

func (r *objectXMLReader) Read(p []byte) (int, error) {
	if r.terminal != nil {
		return 0, r.terminal
	}
	if len(p) == 0 {
		return 0, nil
	}
	probe := r.remaining == 0
	if probe {
		p = p[:1]
	} else {
		p = p[:min(int64(len(p)), r.remaining)]
	}
	n, err := r.source.Read(p)
	if n < 0 || n > len(p) {
		r.terminal = errObjectXMLRead
		return 0, r.terminal
	}
	switch {
	case errors.Is(err, context.Canceled):
		r.terminal = errors.Join(errObjectXMLRead, context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		r.terminal = errors.Join(errObjectXMLRead, context.DeadlineExceeded)
	case probe && n != 0:
		r.terminal = errObjectXMLLimit
	case err == io.EOF:
		r.terminal = io.EOF
	case err != nil:
		r.terminal = errObjectXMLRead
	}
	if probe {
		return 0, r.terminal
	}
	r.remaining -= int64(n)
	return n, r.terminal
}
