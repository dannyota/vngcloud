package storage

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func objectXMLDrain(d *objectXMLDecoder) ([]xml.Token, error) {
	var tokens []xml.Token
	for {
		token, err := d.Token()
		if err != nil {
			return tokens, err
		}
		tokens = append(tokens, xml.CopyToken(token))
	}
}

func TestObjectXMLDocuments(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"empty root", `<Result/>`, true},
		{"alternate root", `<Error/>`, true},
		{"declaration", `<?xml version="1.0" encoding="UTF-8"?><Result/>`, true},
		{"comments and whitespace", "<!--before--> \n<Result><!--inside-->value</Result>\t<!--after-->", true},
		{"namespace", `<Result xmlns="http://s3.amazonaws.com/doc/2006-03-01/"/>`, true},
		{"prefixed namespace", `<s:Result xmlns:s="http://s3.amazonaws.com/doc/2006-03-01/"/>`, true},
		{"unknown nesting and nested scalars", `<Result><Unknown><Value>nested</Value></Unknown><Key><Other/></Key></Result>`, true},
		{"standard entities", `<Result a="&quot;">&amp;&lt;&gt;&apos;&#x41;</Result>`, true},
		{"empty document", "", false},
		{"whitespace only", " \n\t", false},
		{"comment only", `<!--comment-->`, false},
		{"wrong root", `<Wrong/>`, false},
		{"wrong namespace", `<Result xmlns="urn:synthetic-private"/>`, false},
		{"unbound prefix", `<p:Result/>`, false},
		{"duplicate root", `<Result/><Result/>`, false},
		{"different extra root", `<Result/><Error/>`, false},
		{"unclosed root", `<Result>`, false},
		{"mismatched close", `<Result><Child></Result>`, false},
		{"orphan close", `</Result>`, false},
		{"trailing text", `<Result/>synthetic-private`, false},
		{"leading text", `synthetic-private<Result/>`, false},
		{"non XML whitespace", "<Result/>\u00a0", false},
		{"inner declaration", `<Result><?xml version="1.0"?></Result>`, false},
		{"late declaration", `<Result/><?xml version="1.0"?>`, false},
		{"declaration after whitespace", ` <?xml version="1.0"?><Result/>`, false},
		{"declaration after comment", `<!--x--><?xml version="1.0"?><Result/>`, false},
		{"duplicate declaration", `<?xml version="1.0"?><?xml version="1.0"?><Result/>`, false},
		{"processing instruction", `<?synthetic-private x?><Result/>`, false},
		{"inner processing instruction", `<Result><?target x?></Result>`, false},
		{"trailing processing instruction", `<Result/><?target x?>`, false},
		{"directive", `<!DOCTYPE Result><Result/>`, false},
		{"inner directive", `<Result><!synthetic-private></Result>`, false},
		{"external entity", `<!DOCTYPE Result [<!ENTITY x SYSTEM "https://peer.invalid/private">]><Result>&x;</Result>`, false},
		{"entity expansion", `<!DOCTYPE Result [<!ENTITY x "synthetic-private">]><Result>&x;</Result>`, false},
		{"undefined entity", `<Result>&synthetic-private;</Result>`, false},
		{"invalid character reference", `<Result>&#0;</Result>`, false},
		{"invalid UTF8 text", "<Result>\xff</Result>", false},
		{"invalid UTF8 attribute", "<Result a=\"\xff\"/>", false},
		{"invalid UTF8 comment", "<!--\xff--><Result/>", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newObjectXMLDecoder(strings.NewReader(tc.body), 1024, "Result", "Error")
			_, err := objectXMLDrain(d)
			if tc.valid {
				if !errors.Is(err, io.EOF) {
					t.Fatalf("valid document: %v", err)
				}
			} else {
				objectXMLAssertSafeError(t, err)
			}
			_, again := d.Token()
			if !errors.Is(again, err) {
				t.Fatalf("terminal error changed: %v -> %v", err, again)
			}
		})
	}
}

func TestObjectXMLBounds(t *testing.T) {
	const body = `<Result>1234</Result>`
	for _, extra := range []string{"", " ", strings.Repeat(" ", 128)} {
		for _, chunk := range []int{1, 3, 4096} {
			t.Run(fmt.Sprintf("extra%d/chunk%d", len(extra), chunk), func(t *testing.T) {
				r := &objectXMLTestReader{data: body + extra, chunk: chunk}
				d := newObjectXMLDecoder(r, int64(len(body)), "Result")
				_, err := objectXMLDrain(d)
				want := len(body)
				if extra == "" {
					if !errors.Is(err, io.EOF) {
						t.Fatal(err)
					}
				} else {
					want++
					objectXMLAssertSafeError(t, err)
				}
				if r.bytes != want {
					t.Fatalf("underlying bytes=%d, want %d", r.bytes, want)
				}
				before := r.reads
				_, _ = d.Token()
				if r.reads != before {
					t.Fatal("terminal decoder read again")
				}
			})
		}
	}
	for _, limit := range []int64{0, -1} {
		r := &objectXMLTestReader{data: body, chunk: 4096}
		_, err := objectXMLDrain(newObjectXMLDecoder(r, limit, "Result"))
		objectXMLAssertSafeError(t, err)
		if r.bytes > 1 {
			t.Fatalf("invalid limit read %d bytes", r.bytes)
		}
	}
	for _, depth := range []int{16, 17} {
		body := `<Result>` + strings.Repeat(`<Unknown>`, depth-1) + strings.Repeat(`</Unknown>`, depth-1) + `</Result>`
		_, err := objectXMLDrain(newObjectXMLDecoder(strings.NewReader(body), 1024, "Result"))
		if depth == 16 {
			if !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
		} else {
			objectXMLAssertSafeError(t, err)
		}
	}
}

func TestObjectXMLNamespaceTokens(t *testing.T) {
	body := `<Result xmlns="http://s3.amazonaws.com/doc/2006-03-01/" xmlns:p="urn:other"><p:Key p:attr="x"/><Key/><Key xmlns=""/></Result>`
	tokens, err := objectXMLDrain(newObjectXMLDecoder(strings.NewReader(body), 1024, "Result"))
	if !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	var starts []xml.StartElement
	for _, token := range tokens {
		if start, ok := token.(xml.StartElement); ok {
			starts = append(starts, start)
		}
	}
	if len(starts) != 4 || starts[1].Name.Space != "urn:other" || starts[1].Attr[0].Name.Space != "urn:other" ||
		starts[2].Name.Space != "http://s3.amazonaws.com/doc/2006-03-01/" || starts[3].Name.Space != "" {
		t.Fatalf("namespace data lost: %#v", starts)
	}
	_, err = objectXMLDrain(newObjectXMLDecoder(strings.NewReader(`<Result/>`), 1024))
	objectXMLAssertSafeError(t, err)
}

func TestObjectXMLReaderErrors(t *testing.T) {
	privateErr := errors.New("synthetic-private https://peer.invalid/private")
	for _, body := range []string{"", `<Result>`, `<Result/>`} {
		for _, cause := range []error{privateErr, io.ErrUnexpectedEOF,
			fmt.Errorf("synthetic-private: %w", context.Canceled),
			fmt.Errorf("synthetic-private: %w", context.DeadlineExceeded)} {
			for _, withData := range []bool{false, true} {
				r := &objectXMLTestReader{data: body, chunk: 1, err: cause, withData: withData}
				_, err := objectXMLDrain(newObjectXMLDecoder(r, 128, "Result"))
				objectXMLAssertSafeError(t, err)
				for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
					if errors.Is(err, sentinel) != errors.Is(cause, sentinel) {
						t.Fatalf("lost context: %v", err)
					}
				}
				if errors.Is(err, privateErr) || errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("retained arbitrary reader cause: %v", err)
				}
			}
		}
	}
	for _, withData := range []bool{false, true} {
		r := &objectXMLTestReader{data: `<Result/>`, chunk: 1, err: io.EOF, withData: withData, emptyReads: 2}
		_, err := objectXMLDrain(newObjectXMLDecoder(r, 9, "Result"))
		if !errors.Is(err, io.EOF) || r.bytes != 9 {
			t.Fatalf("short reads/EOF: bytes=%d error=%v", r.bytes, err)
		}
	}
}

func objectXMLAssertSafeError(t *testing.T, err error) {
	t.Helper()
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("expected rejection, got %v", err)
	}
	for _, text := range []string{err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		if len(text) > 256 || strings.Contains(text, "synthetic-private") || strings.Contains(text, "peer.invalid") {
			t.Fatalf("unsafe error text: %q", text)
		}
	}
	for cause := errors.Unwrap(err); cause != nil; cause = errors.Unwrap(cause) {
		if len(cause.Error()) > 128 || strings.Contains(cause.Error(), "synthetic-private") {
			t.Fatal("unsafe error cause")
		}
	}
}

type objectXMLTestReader struct {
	data       string
	chunk      int
	bytes      int
	reads      int
	err        error
	withData   bool
	emptyReads int
}

func (r *objectXMLTestReader) Read(p []byte) (int, error) {
	r.reads++
	if r.emptyReads > 0 {
		r.emptyReads--
		return 0, nil
	}
	if r.data == "" {
		if r.err != nil {
			return 0, r.err
		}
		return 0, io.EOF
	}
	n := copy(p[:min(len(p), r.chunk)], r.data)
	r.data = r.data[n:]
	r.bytes += n
	if r.withData && r.data == "" {
		return n, r.err
	}
	return n, nil
}
