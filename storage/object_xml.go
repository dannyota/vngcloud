package storage

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"slices"
	"unicode/utf8"
)

var (
	errObjectXMLInvalid = errors.New("storage: invalid object XML response")
	errObjectXMLRead    = errors.New("storage: object XML response read failed")
	errObjectXMLLimit   = errors.New("storage: object XML response exceeds byte limit")
)

type objectXMLDecoder struct {
	decoder  *xml.Decoder
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
	d.decoder = xml.NewDecoder(d.reader)
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
		if !d.initial || token.Target != "xml" || !utf8.Valid(token.Inst) {
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
	d.decoder, d.reader, d.roots = nil, nil, nil
	return nil, err
}

func objectXMLWhitespace(data []byte) bool {
	for _, b := range data {
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			return false
		}
	}
	return true
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
