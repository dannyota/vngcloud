package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"danny.vn/vngcloud"
)

const listEmpty = `<ListBucketResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated></ListBucketResult>`

func listPage(fields string) string {
	return strings.Replace(listEmpty, `</ListBucketResult>`, fields+`</ListBucketResult>`, 1)
}

func listCall(t *testing.T, body string, in *ListObjectsInput) (*ListObjectsOutput, error) {
	t.Helper()
	c := objectTestClient(t)
	c.httpClient.Transport = &objectResponseTransport{response: &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}}
	return c.ListObjects(t.Context(), in)
}

func TestListObjectsRequest(t *testing.T) {
	token := " next+%2F&=?#猫 "
	calls := 0
	c, count := objectLocalClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		want := url.Values{"list-type": {"2"}, "encoding-type": {"url"}, "max-keys": {"2"}, "prefix": {"a /+%猫"}, "delimiter": {"/"}}
		if calls == 2 {
			want.Set("continuation-token", token)
		}
		stamp, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
		if err != nil {
			t.Error(err)
		}
		reference := r.Clone(r.Context())
		reference.URL.Scheme = "https"
		reference.URL.Host = "hcm04.vstorage.vngcloud.vn"
		reference.Host = reference.URL.Host
		reference.Header = http.Header{}
		_, _, auth := objectReferenceSign(reference, r.Header.Get("X-Amz-Content-Sha256"), stamp, "HCM04", objectTestConfig().Credentials)
		if r.Header.Get("Authorization") != auth {
			t.Error("list query signature differs")
		}
		if r.Method != "GET" || r.URL.Path != "/bucket" || r.URL.RawQuery != objectEncodeQuery(want) || r.Header.Get("Authorization") == "" || r.Header.Get("X-Amz-Content-Sha256") != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
			t.Errorf("unexpected request: %s %s", r.Method, r.RequestURI)
		}
		body := listEmpty
		if calls == 1 {
			body = strings.Replace(body, "false", "true", 1)
			body = strings.Replace(body, `</ListBucketResult>`, `<NextContinuationToken> next+%2F&amp;=?#猫 </NextContinuationToken></ListBucketResult>`, 1)
		}
		_, _ = io.WriteString(w, body)
	})
	in := &ListObjectsInput{Bucket: "bucket", Prefix: "a /+%猫", Delimiter: "/", MaxKeys: 2}
	out, err := c.ListObjects(t.Context(), in)
	if err != nil || out.NextContinuationToken != token || count.Load() != 1 {
		t.Fatalf("out=%v err=%v calls=%d", out, err, count.Load())
	}
	in.ContinuationToken = out.NextContinuationToken
	out, err = c.ListObjects(t.Context(), in)
	if err != nil || out.IsTruncated || count.Load() != 2 {
		t.Fatalf("out=%v err=%v calls=%d", out, err, count.Load())
	}
}

func TestListObjectsValidation(t *testing.T) {
	c, count := objectLocalClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, listEmpty) })
	for _, in := range []*ListObjectsInput{nil, {}, {Bucket: "../bucket"}, {Bucket: "bucket", MaxKeys: -1}, {Bucket: "bucket", MaxKeys: 1001}, {Bucket: "bucket", Prefix: "\xff"}, {Bucket: "bucket", Prefix: strings.Repeat("a", 1025)}, {Bucket: "bucket", Delimiter: "x"}, {Bucket: "bucket", ContinuationToken: "\n"}, {Bucket: "bucket", ContinuationToken: "\xff"}, {Bucket: "bucket", ContinuationToken: strings.Repeat("a", 8193)}} {
		out, err := c.ListObjects(t.Context(), in)
		if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) || count.Load() != 0 {
			t.Fatalf("out=%v err=%v requests=%d", out, err, count.Load())
		}
	}
	var absent *ObjectClient
	if _, err := absent.ListObjects(t.Context(), &ListObjectsInput{Bucket: "bucket"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatal(err)
	}
}

func TestListObjectsDecode(t *testing.T) {
	for _, file := range []string{"list.xml", "list-empty.xml"} {
		raw, err := os.ReadFile("../testdata/storage/objects/" + file)
		if err != nil {
			t.Fatal(err)
		}
		in := &ListObjectsInput{Bucket: "bucket"}
		if file == "list.xml" {
			in.Prefix = "folder/"
			in.Delimiter = "/"
			in.MaxKeys = 3
		}
		out, err := listCall(t, string(raw), in)
		if err != nil || out.Items == nil || out.CommonPrefixes == nil {
			t.Fatalf("out=%v err=%v", out, err)
		}
		if file == "list-empty.xml" {
			if len(out.Items) != 0 || out.MaxKeys != 1000 {
				t.Fatal(out)
			}
			continue
		}
		if len(out.Items) != 2 || out.Items[0].Key != "folder/z+%2F" || out.Items[1].Key != "folder/猫" || out.Items[0].Size != 7 || out.Items[0].ETag != `"safe"` || out.Items[0].StorageClass != "STANDARD" || out.Items[0].LastModified == nil || out.Items[0].LastModified.Format(time.RFC3339) != "2020-01-02T01:04:05Z" || len(out.CommonPrefixes) != 1 || out.CommonPrefixes[0] != "folder/child/" || out.MaxKeys != 3 {
			t.Fatal(out)
		}
	}
	invalid := []string{
		"", `<Wrong/>`, listEmpty + `<ListBucketResult/>`, listEmpty + `x`, strings.Replace(listEmpty, "false", "1", 1), strings.Replace(listEmpty, "url", "other", 1), strings.Replace(listEmpty, `<EncodingType>url</EncodingType>`, "", 1), strings.Replace(listEmpty, `<IsTruncated>false</IsTruncated>`, "", 1),
		listPage(`<EncodingType>url</EncodingType>`), listPage(`<IsTruncated>false</IsTruncated>`), listPage(`<Contents><Key>a</Key></Contents>`), listPage(`<Contents><Size>0</Size></Contents>`), listPage(`<Contents><Key>a</Key><Key>b</Key><Size>0</Size></Contents>`), listPage(`<Contents><Key><Nested/></Key><Size>0</Size></Contents>`), listPage(`<CommonPrefixes/>`), listPage(`<Name>other</Name>`), listPage(`<MaxKeys>2</MaxKeys>`), listPage(`<KeyCount>1001</KeyCount>`), listPage(`<KeyCount>-1</KeyCount>`), listPage(`<KeyCount>0</KeyCount><KeyCount>0</KeyCount>`), listPage(`<Contents xmlns="urn:other"><Key>a</Key><Size>0</Size></Contents><EncodingType>other</EncodingType>`),
	}
	for _, size := range []string{"", "-1", "+1", " 1", "1.0", "9223372036854775808"} {
		invalid = append(invalid, listPage(`<Contents><Key>a</Key><Size>`+size+`</Size></Contents>`))
	}
	for i, body := range invalid {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			out, err := listCall(t, body, &ListObjectsInput{Bucket: "bucket"})
			if out != nil || err == nil {
				t.Fatalf("out=%v err=%v", out, err)
			}
		})
	}
	for _, fields := range []string{`<Unknown><Key>a</Key></Unknown>`, `<p:IsTruncated xmlns:p="urn:other">true</p:IsTruncated>`, `<KeyCount>0</KeyCount>`, `<Contents><Key>a</Key><Size>9223372036854775807</Size></Contents>`} {
		if _, err := listCall(t, listPage(fields), &ListObjectsInput{Bucket: "bucket"}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := listCall(t, listPage(strings.Repeat(`<Contents><Key>a</Key><Size>0</Size></Contents>`, 2)), &ListObjectsInput{Bucket: "bucket", MaxKeys: 1})
	if out != nil || err == nil {
		t.Fatal("accepted too many entries")
	}
}

func TestListObjectsPagination(t *testing.T) {
	for _, token := range []string{"", "same", "&#10;", "synthetic-secret-key", strings.Repeat("a", 8193), "opaque+%2F&amp;="} {
		body := strings.Replace(listEmpty, "false", "true", 1)
		body = strings.Replace(body, `</ListBucketResult>`, `<NextContinuationToken>`+token+`</NextContinuationToken></ListBucketResult>`, 1)
		out, err := listCall(t, body, &ListObjectsInput{Bucket: "bucket", ContinuationToken: "same"})
		valid := token == "opaque+%2F&amp;="
		if valid {
			if err != nil || out.NextContinuationToken != "opaque+%2F&=" || !out.IsTruncated {
				t.Fatalf("out=%v err=%v", out, err)
			}
		} else if out != nil || err == nil {
			t.Fatal("accepted unusable token")
		}
	}
	for _, fields := range []string{`<NextContinuationToken>x</NextContinuationToken>`, `<ContinuationToken>different</ContinuationToken>`} {
		if out, err := listCall(t, listPage(fields), &ListObjectsInput{Bucket: "bucket"}); out != nil || err == nil {
			t.Fatal("accepted inconsistent final page")
		}
	}
}

func TestListObjectsEncoding(t *testing.T) {
	for _, key := range []string{"a+b", "%252F", "%00%0A%7F", "a//.././b", "%E7%8C%AB", strings.Repeat("a", 1024)} {
		out, err := listCall(t, listPage(`<Contents><Key>`+key+`</Key><Size>0</Size></Contents>`), &ListObjectsInput{Bucket: "bucket"})
		want, _ := url.PathUnescape(key)
		if err != nil || out.Items[0].Key != want {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
	for _, fields := range []string{`<Contents><Key>%</Key><Size>0</Size></Contents>`, `<Contents><Key>%FF</Key><Size>0</Size></Contents>`, `<Contents><Key></Key><Size>0</Size></Contents>`, `<Contents><Key>` + strings.Repeat("a", 1025) + `</Key><Size>0</Size></Contents>`, `<Prefix>wrong</Prefix>`, `<Delimiter>%2F</Delimiter>`, `<CommonPrefixes><Prefix>a%2F</Prefix></CommonPrefixes>`} {
		if out, err := listCall(t, listPage(fields), &ListObjectsInput{Bucket: "bucket"}); out != nil || err == nil {
			t.Fatal("accepted invalid encoding or echo")
		}
	}
	for _, fields := range []string{`<Contents><Key>other</Key><Size>0</Size></Contents>`, `<CommonPrefixes><Prefix>a</Prefix></CommonPrefixes>`, `<CommonPrefixes><Prefix>other%2F</Prefix></CommonPrefixes>`} {
		if out, err := listCall(t, listPage(fields), &ListObjectsInput{Bucket: "bucket", Prefix: "a", Delimiter: "/"}); out != nil || err == nil {
			t.Fatal("accepted inconsistent prefix")
		}
	}
}

func TestListObjectsSecurity(t *testing.T) {
	for _, credential := range []string{"synthetic-secret-key", `a"b\c`, "7"} {
		for _, form := range []string{credential, string(mustListJSON(t, credential)), url.PathEscape(credential)} {
			escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(form)
			for _, field := range []string{"Key", "Prefix", "NextContinuationToken", "Size"} {
				c := objectTestClient(t)
				c.credentials.SecretAccessKey = vngcloud.Secret(credential)
				fields := `<Contents><Key>safe</Key><Size>0</Size></Contents>`
				in := &ListObjectsInput{Bucket: "bucket", Delimiter: "/"}
				switch field {
				case "Key":
					fields = `<Contents><Key>` + escaped + `</Key><Size>0</Size></Contents>`
				case "Size":
					fields = `<Contents><Key>safe</Key><Size>` + escaped + `</Size></Contents>`
				case "Prefix":
					fields = `<CommonPrefixes><Prefix>` + escaped + `%2F</Prefix></CommonPrefixes>`
				case "NextContinuationToken":
					fields = `<NextContinuationToken>` + escaped + `</NextContinuationToken>`
				}
				body := listPage(fields)
				if field == "NextContinuationToken" {
					body = strings.Replace(body, "false", "true", 1)
				}
				c.httpClient.Transport = &objectResponseTransport{response: &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}}
				out, err := c.ListObjects(t.Context(), in)
				if out != nil || err == nil {
					t.Fatalf("accepted reflected %s", field)
				}
				assertObjectSafe(t, err, credential, form)
			}
		}
		c := objectTestClient(t)
		c.credentials.SecretAccessKey = vngcloud.Secret(credential)
		body := listPage(`<Contents><Key>safe</Key><Size>0</Size><ETag>` + credential + `</ETag><StorageClass>` + credential + `</StorageClass><LastModified>` + credential + `</LastModified></Contents>`)
		c.httpClient.Transport = &objectResponseTransport{response: &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}}
		out, err := c.ListObjects(t.Context(), &ListObjectsInput{Bucket: "bucket"})
		if err != nil || out.Items[0].ETag != "" || out.Items[0].StorageClass != "" || out.Items[0].LastModified != nil {
			t.Fatalf("out=%v err=%v", out, err)
		}
		assertObjectSafe(t, out, credential)
	}
}

func mustListJSON(t *testing.T, value string) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type listTrackedBody struct {
	io.Reader
	closed bool
}

func (b *listTrackedBody) Close() error { b.closed = true; return nil }

func TestListObjectsOutcomes(t *testing.T) {
	for _, status := range []int{200, 204, 206, 301, 302, 307, 308, 400, 401, 403, 404, 409, 412, 429, 500, 503} {
		body := &listTrackedBody{Reader: strings.NewReader(`<Error><Code>AccessDenied</Code><Message>synthetic-secret-key</Message></Error>`)}
		c := objectTestClient(t)
		tr := &objectResponseTransport{response: &http.Response{StatusCode: status, Body: body}}
		c.httpClient.Transport = tr
		out, err := c.ListObjects(t.Context(), &ListObjectsInput{Bucket: "bucket"})
		var api *vngcloud.APIError
		if out != nil || !errors.As(err, &api) || api.Operation != "storage.ListObjects" || api.StatusCode != status || !body.closed {
			t.Fatalf("out=%v err=%v closed=%v", out, err, body.closed)
		}
		for code, sentinel := range map[int]error{401: vngcloud.ErrAuth, 403: vngcloud.ErrPermission, 404: vngcloud.ErrNotFound, 429: vngcloud.ErrRateLimited} {
			if errors.Is(err, sentinel) != (status == code) {
				t.Fatal(err)
			}
		}
		assertObjectSafe(t, err, "synthetic-secret-key")
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("synthetic-secret-key https://peer.invalid")} {
		body := &listTrackedBody{Reader: &objectXMLTestReader{data: listEmpty, chunk: 1, err: cause, withData: true}}
		c := objectTestClient(t)
		c.httpClient.Transport = &objectResponseTransport{response: &http.Response{StatusCode: 200, Body: body}}
		out, err := c.ListObjects(t.Context(), &ListObjectsInput{Bucket: "bucket"})
		if out != nil || err == nil || !body.closed {
			t.Fatalf("out=%v err=%v closed=%v", out, err, body.closed)
		}
		for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
			if errors.Is(err, sentinel) != errors.Is(cause, sentinel) {
				t.Fatal("lost context")
			}
		}
		assertObjectSafe(t, err, "synthetic-secret-key", "peer.invalid")
	}
}

func TestListObjectsBounds(t *testing.T) {
	const limit = 8 * 1024 * 1024
	for _, extra := range []int{0, 1} {
		data := listEmpty + strings.Repeat(" ", limit-len(listEmpty)+extra)
		reader := &objectXMLTestReader{data: data, chunk: 4096}
		body := &listTrackedBody{Reader: reader}
		c := objectTestClient(t)
		c.httpClient.Transport = &objectResponseTransport{response: &http.Response{StatusCode: 200, Body: body}}
		out, err := c.ListObjects(t.Context(), &ListObjectsInput{Bucket: "bucket"})
		if extra == 0 {
			if err != nil || out == nil {
				t.Fatal(err)
			}
		} else if out != nil || err == nil {
			t.Fatal("accepted over-limit document")
		}
		if reader.bytes != limit+extra || !body.closed {
			t.Fatalf("bytes=%d closed=%v", reader.bytes, body.closed)
		}
	}
	for _, depth := range []int{16, 17} {
		unknown := strings.Repeat(`<Unknown>`, depth-1) + strings.Repeat(`</Unknown>`, depth-1)
		out, err := listCall(t, listPage(unknown), &ListObjectsInput{Bucket: "bucket"})
		if depth == 16 {
			if err != nil || out == nil {
				t.Fatal(err)
			}
		} else if out != nil || err == nil {
			t.Fatal("accepted over-depth document")
		}
	}
	for _, fields := range []string{`<Contents><Key>a</Key><Size>` + strings.Repeat("0", 100) + `</Size></Contents>`, `<KeyCount>` + strings.Repeat("0", 100) + `</KeyCount>`, `<Contents><Key>a</Key><Size>0</Size><ETag>x</ETag><ETag>x</ETag></Contents>`, `<Contents><Key>a</Key><Size>0</Size><StorageClass><Nested/></StorageClass></Contents>`, `<CommonPrefixes><Prefix>a/</Prefix><Prefix>a/</Prefix></CommonPrefixes>`} {
		if out, err := listCall(t, listPage(fields), &ListObjectsInput{Bucket: "bucket", Delimiter: "/"}); out != nil || err == nil {
			t.Fatal("accepted invalid scalar")
		}
	}
	for _, body := range []string{`<!DOCTYPE ListBucketResult>` + listEmpty, `<?target synthetic?>` + listEmpty, strings.Replace(listEmpty, `<EncodingType>`, `<EncodingType xmlns="urn:other">`, 1), strings.Replace(listEmpty, `<IsTruncated>`, `<IsTruncated xmlns="urn:other">`, 1), strings.Replace(listEmpty, `<ListBucketResult>`, `<ListBucketResult xmlns="urn:other">`, 1), listPage(`<Unknown>\xff</Unknown>`)} {
		// Include a byte invalid in UTF-8 rather than a literal escape sequence.
		body = strings.ReplaceAll(body, `\xff`, "\xff")
		if out, err := listCall(t, body, &ListObjectsInput{Bucket: "bucket"}); out != nil || err == nil {
			t.Fatal("accepted invalid XML")
		}
	}
}

func TestListObjectsOptionalMetadata(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int
	}{{"ETag", 1024}, {"StorageClass", 128}, {"LastModified", 128}} {
		for _, extra := range []int{0, 1} {
			value := strings.Repeat("a", tc.limit+extra)
			out, err := listCall(t, listPage(`<Contents><Key>a</Key><Size>0</Size><`+tc.name+`>`+value+`</`+tc.name+`></Contents>`), &ListObjectsInput{Bucket: "bucket"})
			if err != nil {
				t.Fatal(err)
			}
			got := out.Items[0].ETag
			if tc.name == "StorageClass" {
				got = out.Items[0].StorageClass
			}
			if tc.name == "LastModified" {
				if out.Items[0].LastModified != nil {
					t.Fatal("retained invalid timestamp")
				}
				continue
			}
			want := value
			if extra != 0 {
				want = ""
			}
			if got != want {
				t.Fatalf("%s length=%d want=%d", tc.name, len(got), len(want))
			}
		}
	}
	for _, credential := range []string{"2020-01-02T01:04:05Z", "2020-01-02 01:04:05 +0000 UTC", "time.Date(2020, time.January, 2, 1, 4, 5, 0, time.UTC)"} {
		c := objectTestClient(t)
		c.credentials.SecretAccessKey = vngcloud.Secret(credential)
		c.httpClient.Transport = &objectResponseTransport{response: &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(listPage(`<Contents><Key>a</Key><Size>0</Size><LastModified>2020-01-02T03:04:05+02:00</LastModified></Contents>`)))}}
		out, err := c.ListObjects(t.Context(), &ListObjectsInput{Bucket: "bucket"})
		if err != nil || out.Items[0].LastModified != nil {
			t.Fatalf("out=%v err=%v", out, err)
		}
		assertObjectSafe(t, out, credential)
	}
}

func TestListObjectsEchoes(t *testing.T) {
	in := &ListObjectsInput{Bucket: "bucket", Prefix: "a/+%猫", Delimiter: "/", ContinuationToken: "opaque+%2F", MaxKeys: 1}
	fields := `<Name>bucket</Name><Prefix>a%2F+%25%E7%8C%AB</Prefix><Delimiter>%2F</Delimiter><ContinuationToken>opaque+%2F</ContinuationToken><MaxKeys>1</MaxKeys><KeyCount>1</KeyCount>`
	out, err := listCall(t, listPage(fields), in)
	if err != nil || out.MaxKeys != 1 || len(out.Items) != 0 {
		t.Fatalf("out=%v err=%v", out, err)
	}
	for _, fields := range []string{`<Name>bucket</Name><Name>bucket</Name>`, `<MaxKeys>0</MaxKeys>`, `<MaxKeys>1001</MaxKeys>`, `<ContinuationToken>opaque%2B%2F</ContinuationToken>`, `<Prefix>a%2F+%25%E7%8C%AB</Prefix><Prefix>a%2F+%25%E7%8C%AB</Prefix>`} {
		if out, err := listCall(t, listPage(fields), in); out != nil || err == nil {
			t.Fatal("accepted invalid echo")
		}
	}
}

type listBlockingBody struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func (b *listBlockingBody) Read([]byte) (int, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.closed
	return 0, errors.New("synthetic-secret-key blocked read")
}

func (b *listBlockingBody) Close() error { b.closeOnce.Do(func() { close(b.closed) }); return nil }

func TestListObjectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	body := &listBlockingBody{started: make(chan struct{}), closed: make(chan struct{})}
	c := objectTestClient(t)
	c.httpClient.Transport = &objectResponseTransport{response: &http.Response{StatusCode: 200, Body: body}}
	done := make(chan struct{})
	go func() { defer close(done); <-body.started; cancel() }()
	out, err := c.ListObjects(ctx, &ListObjectsInput{Bucket: "bucket"})
	<-done
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("out=%v err=%v", out, err)
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("cancellation did not close body")
	}
	assertObjectSafe(t, err, "synthetic-secret-key")
}

func TestListObjectsOptionalReflections(t *testing.T) {
	credential := `a"b\c`
	for _, form := range []string{credential, string(mustListJSON(t, credential)), url.PathEscape(credential)} {
		escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(form)
		c := objectTestClient(t)
		c.credentials.AccessKeyID = vngcloud.Secret(credential)
		body := listPage(`<Contents><Key>safe</Key><Size>0</Size><ETag>` + escaped + `</ETag><StorageClass>` + escaped + `</StorageClass><LastModified>` + escaped + `</LastModified></Contents>`)
		c.httpClient.Transport = &objectResponseTransport{response: &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}}
		out, err := c.ListObjects(t.Context(), &ListObjectsInput{Bucket: "bucket"})
		if err != nil || out.Items[0].ETag != "" || out.Items[0].StorageClass != "" || out.Items[0].LastModified != nil {
			t.Fatalf("out=%v err=%v", out, err)
		}
		assertObjectSafe(t, out, credential, form)
	}
}

func TestListObjectsErrorContext(t *testing.T) {
	for _, tc := range []struct {
		status   int
		code     string
		sentinel error
	}{
		{403, "AccessDenied", vngcloud.ErrPermission},
		{404, "NoSuchKey", vngcloud.ErrNotFound},
		{429, "SlowDown", vngcloud.ErrRateLimited},
		{500, "InternalError", nil},
	} {
		for _, mode := range []string{"complete", "read failure", "close unblocks"} {
			for _, contextErr := range []error{context.Canceled, context.DeadlineExceeded} {
				t.Run(strconv.Itoa(tc.status)+"/"+mode+"/"+contextErr.Error(), func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						ctx, cancel := context.WithCancel(t.Context())
						defer cancel()
						rawCause := errors.New("synthetic-secret-key https://peer.invalid/private error-body read failure")
						body := &listErrorContextBody{mode: mode, code: tc.code, closed: make(chan struct{}), cause: rawCause}
						if errors.Is(contextErr, context.Canceled) {
							body.cancel = cancel
						}
						tr := &listContextFailureTransport{status: tc.status, body: body}
						c := objectTestClient(t)
						c.timeout = time.Second
						c.httpClient.Transport = tr
						out, err := c.ListObjects(ctx, &ListObjectsInput{Bucket: "bucket"})
						var api *vngcloud.APIError
						if out != nil || !errors.As(err, &api) || api.Operation != "storage.ListObjects" || api.StatusCode != tc.status {
							t.Fatalf("out=%v error=%v", out, err)
						}
						wantCode := "S3Error"
						if mode == "complete" {
							wantCode = tc.code
						}
						if api.Code != wantCode || api.Retryable != (tc.status == 429) {
							t.Errorf("code=%s retryable=%t", api.Code, api.Retryable)
						}
						if !errors.Is(err, contextErr) || !errors.Is(api.Err, contextErr) {
							t.Errorf("lost context identity: %v", err)
						}
						otherContext := context.Canceled
						if errors.Is(contextErr, context.Canceled) {
							otherContext = context.DeadlineExceeded
						}
						if errors.Is(err, otherContext) {
							t.Errorf("unexpected context identity: %v", err)
						}
						for _, sentinel := range []error{vngcloud.ErrPermission, vngcloud.ErrNotFound, vngcloud.ErrRateLimited} {
							if errors.Is(err, sentinel) != errors.Is(tc.sentinel, sentinel) {
								t.Errorf("wrong status identity: %v", err)
							}
						}
						if tr.calls != 1 || body.reads.Load() != 1 || body.closes.Load() != 1 {
							t.Errorf("calls=%d reads=%d closes=%d", tr.calls, body.reads.Load(), body.closes.Load())
						}
						if errors.Is(err, rawCause) || errors.Is(api.Err, rawCause) {
							t.Error("retained raw reader cause")
						}
						assertObjectSafe(t, err, "synthetic-secret-key", "synthetic-access-id", "synthetic-object-bytes", "https://peer.invalid/private")
						assertObjectSafe(t, api.Err, "synthetic-secret-key", "synthetic-object-bytes", "https://peer.invalid/private")
					})
				})
			}
		}
	}
}

type listContextFailureTransport struct {
	status int
	body   *listErrorContextBody
	cancel context.CancelFunc
	calls  int
	cause  error
}

func (tr *listContextFailureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.calls++
	if tr.body != nil {
		tr.body.ctx = req.Context()
		return &http.Response{StatusCode: tr.status, Body: tr.body}, nil
	}
	if tr.cancel != nil {
		tr.cancel()
	}
	<-req.Context().Done()
	return nil, tr.cause
}

type listErrorContextBody struct {
	ctx    context.Context
	cancel context.CancelFunc
	mode   string
	code   string
	cause  error
	closed chan struct{}
	reads  atomic.Int32
	closes atomic.Int32
}

func (b *listErrorContextBody) Read(p []byte) (int, error) {
	b.reads.Add(1)
	if b.cancel != nil {
		b.cancel()
	}
	if b.mode == "close unblocks" {
		<-b.closed
	} else {
		<-b.ctx.Done()
	}
	if b.mode == "complete" {
		raw := `<Error><Code>` + b.code + `</Code><Message>synthetic-secret-key synthetic-object-bytes</Message></Error>`
		return copy(p, raw), io.EOF
	}
	return 0, b.cause
}

func (b *listErrorContextBody) Close() error {
	b.closes.Add(1)
	close(b.closed)
	return nil
}

func TestListObjectsDispatchContext(t *testing.T) {
	for _, mode := range []string{"cancel during dispatch", "deadline during dispatch", "canceled before dispatch"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				rawCause := errors.New("synthetic-secret-key https://peer.invalid/private dispatch failure")
				tr := &listContextFailureTransport{cause: rawCause}
				want := context.Canceled
				switch mode {
				case "cancel during dispatch":
					tr.cancel = cancel
				case "deadline during dispatch":
					want = context.DeadlineExceeded
				case "canceled before dispatch":
					cancel()
				}
				c := objectTestClient(t)
				c.timeout = time.Second
				c.httpClient.Transport = tr
				out, err := c.ListObjects(ctx, &ListObjectsInput{Bucket: "bucket"})
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || api.Operation != "storage.ListObjects" || api.StatusCode != 0 || api.Code != "RequestFailed" || api.Retryable {
					t.Fatalf("out=%v err=%v", out, err)
				}
				if !errors.Is(err, want) || !errors.Is(api.Err, want) {
					t.Errorf("lost context identity: %v", err)
				}
				if tr.calls != 1 {
					t.Errorf("calls=%d", tr.calls)
				}
				if errors.Is(err, rawCause) || errors.Is(api.Err, rawCause) {
					t.Error("retained raw transport cause")
				}
				assertObjectSafe(t, err, "synthetic-secret-key", "https://peer.invalid/private")
				assertObjectSafe(t, api.Err, "synthetic-secret-key", "https://peer.invalid/private")
			})
		})
	}
}
