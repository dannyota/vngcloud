package storage

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/transport"
)

var (
	// ErrObjectExists reports a create-only PUT refused because the key exists.
	ErrObjectExists = errors.New("storage: object already exists")
	// ErrObjectWriteUnconfirmed reports a write that may have reached storage.
	ErrObjectWriteUnconfirmed = errors.New("storage: object write is unconfirmed; download and compare before retrying")
)

func (c *ObjectClient) filterObjectMetadata(value string, maxBytes int) string {
	if maxBytes < 0 || len(value) > maxBytes || !utf8.ValidString(value) || objectHasControls(value) {
		return ""
	}
	if transport.RedactErrorText(value, c.credentials.AccessKeyID.Reveal(), c.credentials.SecretAccessKey.Reveal()) != value {
		return ""
	}
	return value
}

func objectAPIError(op string, status int, code string, cause error) *vngcloud.APIError {
	// Only local operations and codes may enter the shared printable error.
	if op != "storage.PutObject" && op != "storage.GetObject" {
		op = "storage.Object"
	}
	message := "object request failed"
	switch code {
	case "ObjectExists":
		message = "object already exists"
	case "IncompleteObject":
		message = "object body ended before its declared length"
	case "ObjectTooLarge":
		message = "object exceeds the download limit"
	case "ObjectBodyClosed":
		message = "object body is closed"
	case "RequestFailed":
	default:
		if !objectKnownS3Code(code) {
			code = "S3Error"
		}
	}
	api := &vngcloud.APIError{Operation: op, StatusCode: status, Code: code, Message: message, Retryable: status == 429}
	switch status {
	case 401:
		api.Err = vngcloud.ErrAuth
		api.Message = "object authentication failed"
	case 403:
		api.Err = vngcloud.ErrPermission
		api.Message = "object permission denied"
	case 404:
		api.Err = vngcloud.ErrNotFound
		api.Message = "object bucket or key not found"
	case 429:
		api.Err = vngcloud.ErrRateLimited
		api.Message = "object request rate limited"
	case 412:
		if code == "ObjectExists" {
			api.Err = ErrObjectExists
		}
	}
	switch {
	case errors.Is(cause, context.Canceled):
		api.Err = errors.Join(api.Err, context.Canceled)
	case errors.Is(cause, context.DeadlineExceeded):
		api.Err = errors.Join(api.Err, context.DeadlineExceeded)
	case api.Err == nil && errors.Is(cause, io.ErrUnexpectedEOF):
		api.Err = io.ErrUnexpectedEOF
	}
	return api
}

func (c *ObjectClient) objectResponseError(op string, resp *http.Response, createOnly bool) error {
	code := "S3Error"
	var readErr error
	if resp.Body != nil {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
		readErr = err
		if err == nil && len(raw) <= 64*1024 {
			candidate := objectXMLCode(raw)
			if objectKnownS3Code(candidate) && c.filterObjectMetadata(candidate, 64) == candidate {
				code = candidate
			}
		}
	}
	if resp.StatusCode == 412 && createOnly {
		code = "ObjectExists"
	}
	return objectAPIError(op, resp.StatusCode, code, readErr)
}

func objectKnownS3Code(code string) bool {
	switch code {
	case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "RequestTimeTooSkewed",
		"NoSuchBucket", "NoSuchKey", "PreconditionFailed", "SlowDown", "InternalError", "ServiceUnavailable":
		return true
	default:
		return false
	}
}

// XML tokens remain local to this bounded parse. Unknown fields never enter
// an error; directives and multiple documents invalidate the code.
func objectXMLCode(raw []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	depth := 0
	rootSeen, rootEnded, codeSeen := false, false, false
	inCode := false
	var code strings.Builder
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if rootEnded && depth == 0 {
				return code.String()
			}
			return ""
		}
		if err != nil {
			return ""
		}
		switch token := token.(type) {
		case xml.StartElement:
			if rootEnded || inCode {
				return ""
			}
			depth++
			if depth == 1 {
				if rootSeen || token.Name.Local != "Error" {
					return ""
				}
				rootSeen = true
			}
			if depth == 2 && token.Name.Local == "Code" {
				if codeSeen {
					return ""
				}
				codeSeen = true
				inCode = true
			}
		case xml.EndElement:
			if depth == 2 && inCode {
				inCode = false
			}
			depth--
			if depth == 0 {
				rootEnded = true
			}
		case xml.CharData:
			if inCode {
				if code.Len()+len(token) > 64 {
					return ""
				}
				code.Write(token)
			} else if depth == 0 && strings.TrimSpace(string(token)) != "" {
				return ""
			}
		case xml.Directive:
			return ""
		}
	}
}
