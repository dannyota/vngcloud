package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// encryptionOrderFixture serves the quote and create paths of both paid
// creates and records the body of each price request and of the volume and
// server orders.
type encryptionOrderFixture struct {
	*svcFixture
	priceBody, volumeBody, serverBody []byte
}

func newEncryptionOrderFixture(t *testing.T) *encryptionOrderFixture {
	t.Helper()
	f := &encryptionOrderFixture{}
	post := func(dst *[]byte, created string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.Method {
			case http.MethodGet:
				_, _ = w.Write([]byte(volumeEmptyListJSON))
			case http.MethodPost:
				defer func() { _ = r.Body.Close() }()
				*dst, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(created))
			default:
				t.Errorf("unexpected method %s", r.Method)
			}
		}
	}
	f.svcFixture = newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			f.priceBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(volumeQuoteJSON(100000)))
		},
		"/v2/proj-1/volumes": post(&f.volumeBody, `{"data":{"uuid":"volume-1"}}`),
		"/v2/proj-1/servers": post(&f.serverBody, `{"data":{"uuid":"server-1"}}`),
	})
	return f
}

// quoteInfo returns the resourceInfo of the last price request.
func (f *encryptionOrderFixture) quoteInfo(t *testing.T) map[string]any {
	t.Helper()
	var decoded struct {
		ResourceInfo map[string]any `json:"resourceInfo"`
	}
	if err := json.Unmarshal(f.priceBody, &decoded); err != nil {
		t.Fatalf("price body is not valid JSON: %v (%s)", err, f.priceBody)
	}
	return decoded.ResourceInfo
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	return decoded
}

func runEncryption(t *testing.T, f *encryptionOrderFixture, args ...string) (stderr string, err error) {
	t.Helper()
	root, _, errBuf := newSvcRoot(t, f.svcFixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"}, args...))
	err = root.ExecuteContext(context.Background())
	return errBuf.String(), err
}

func wantKey(t *testing.T, got map[string]any, key string, want any) {
	t.Helper()
	if got[key] != want {
		t.Errorf("%s = %v, want %v (map=%v)", key, got[key], want, got)
	}
}

func wantNoKey(t *testing.T, got map[string]any, key string) {
	t.Helper()
	if v, ok := got[key]; ok {
		t.Errorf("%s = %v, want the key absent (map=%v)", key, v, got)
	}
}

var quoteCreateVolumeEncryptedArgs = []string{
	"volume", "quote-create-volume", "--zone-id", "zone-1", "--size", "10", "--volume-type-id", "voltype-1",
	"--encryption-type-id", "aes-xts-plain64_256",
}

func TestVolumeQuoteCreateVolumeSendsEncryptionType(t *testing.T) {
	f := newEncryptionOrderFixture(t)
	if stderr, err := runEncryption(t, f, quoteCreateVolumeEncryptedArgs...); err != nil {
		t.Fatalf("quote-create-volume: %v (stderr=%s)", err, stderr)
	}
	wantKey(t, f.quoteInfo(t), "encryptionType", "aes-xts-plain64_256")
}

func TestVolumeCreateVolumeSendsEncryptionTypeOnQuoteAndOrder(t *testing.T) {
	f := newEncryptionOrderFixture(t)
	stderr, err := runEncryption(t, f, "volume", "create-volume", "--name", "data", "--zone-id", "zone-1",
		"--size", "10", "--volume-type-id", "voltype-1", "--encryption-type-id", "aes-xts-plain64_128",
		"--max-price", "100000", "--no-wait")
	if err != nil {
		t.Fatalf("create-volume: %v (stderr=%s)", err, stderr)
	}
	wantKey(t, f.quoteInfo(t), "encryptionType", "aes-xts-plain64_128")
	wantKey(t, decodeBody(t, f.volumeBody), "encryptionType", "aes-xts-plain64_128")
}

func TestVolumeCreateVolumeWithoutEncryptionSendsNoEncryptionKey(t *testing.T) {
	f := newEncryptionOrderFixture(t)
	stderr, err := runEncryption(t, f, "volume", "create-volume", "--name", "data", "--zone-id", "zone-1",
		"--size", "10", "--volume-type-id", "voltype-1", "--max-price", "100000", "--no-wait")
	if err != nil {
		t.Fatalf("create-volume: %v (stderr=%s)", err, stderr)
	}
	wantNoKey(t, f.quoteInfo(t), "encryptionType")
	wantNoKey(t, decodeBody(t, f.volumeBody), "encryptionType")
}

var quoteCreateServerEncryptedArgs = []string{
	"compute", "quote-create-server", "--zone-id", "zone-1", "--flavor-id", "flavor-1",
	"--image-id", "image-1", "--root-disk-size", "20", "--root-disk-type-id", "voltype-1",
	"--root-disk-encryption-type-id", "aes-xts-plain64_256",
}

func TestComputeQuoteCreateServerSendsOnlyEncryptionVolume(t *testing.T) {
	f := newEncryptionOrderFixture(t)
	if stderr, err := runEncryption(t, f, quoteCreateServerEncryptedArgs...); err != nil {
		t.Fatalf("quote-create-server: %v (stderr=%s)", err, stderr)
	}
	info := f.quoteInfo(t)
	wantKey(t, info, "encryptionVolume", true)
	wantNoKey(t, info, "rootDiskEncryptionType")
	wantNoKey(t, info, "dataDiskEncryptionType")
}

func TestComputeQuoteCreateServerDataDiskEncryptionSetsEncryptionVolume(t *testing.T) {
	f := newEncryptionOrderFixture(t)
	args := append([]string{}, quoteCreateServerEncryptedArgs[:len(quoteCreateServerEncryptedArgs)-2]...)
	args = append(args, "--data-disk-size", "10", "--data-disk-type-id", "voltype-1",
		"--data-disk-encryption-type-id", "aes-xts-plain64_128")
	if stderr, err := runEncryption(t, f, args...); err != nil {
		t.Fatalf("quote-create-server: %v (stderr=%s)", err, stderr)
	}
	info := f.quoteInfo(t)
	wantKey(t, info, "encryptionVolume", true)
	wantNoKey(t, info, "dataDiskEncryptionType")
}

func TestComputeCreateServerSendsEncryptionVolumeAndTypeKeys(t *testing.T) {
	f := newEncryptionOrderFixture(t)
	args := append([]string{}, validCreateServerArgs...)
	args = append(args, "--root-disk-encryption-type-id", "aes-xts-plain64_256",
		"--data-disk-size", "10", "--data-disk-type-id", "voltype-1",
		"--data-disk-encryption-type-id", "aes-xts-plain64_128", "--max-price", "100000", "--no-wait")
	if stderr, err := runEncryption(t, f, args...); err != nil {
		t.Fatalf("create-server: %v (stderr=%s)", err, stderr)
	}
	info := f.quoteInfo(t)
	wantKey(t, info, "encryptionVolume", true)
	wantNoKey(t, info, "rootDiskEncryptionType")
	order := decodeBody(t, f.serverBody)
	wantKey(t, order, "encryptionVolume", true)
	wantKey(t, order, "rootDiskEncryptionType", "aes-xts-plain64_256")
	wantKey(t, order, "dataDiskEncryptionType", "aes-xts-plain64_128")
}

func TestComputeCreateServerWithoutEncryptionSendsNoTypeKeys(t *testing.T) {
	f := newEncryptionOrderFixture(t)
	args := append(append([]string{}, validCreateServerArgs...), "--max-price", "100000", "--no-wait")
	if stderr, err := runEncryption(t, f, args...); err != nil {
		t.Fatalf("create-server: %v (stderr=%s)", err, stderr)
	}
	order := decodeBody(t, f.serverBody)
	wantKey(t, order, "encryptionVolume", false)
	wantNoKey(t, order, "rootDiskEncryptionType")
	wantNoKey(t, order, "dataDiskEncryptionType")
}

// TestEncryptionFlagMisuseExitsWithZeroRequests covers the shape checks the
// SDK makes before any request: a data disk type without a data disk, and an
// ID that is not a plain token.
func TestEncryptionFlagMisuseExitsWithZeroRequests(t *testing.T) {
	cases := map[string][]string{
		"data disk type without data disk": append(append([]string{}, quoteCreateServerEncryptedArgs[:len(quoteCreateServerEncryptedArgs)-2]...),
			"--data-disk-encryption-type-id", "aes-xts-plain64_256"),
		"create-server data disk type without data disk": append(append([]string{}, validCreateServerArgs...),
			"--data-disk-encryption-type-id", "aes-xts-plain64_256", "--max-price", "100000"),
		"bad volume type":        append(append([]string{}, quoteCreateVolumeEncryptedArgs[:len(quoteCreateVolumeEncryptedArgs)-1]...), "a/b"),
		"bad root disk type":     append(append([]string{}, quoteCreateServerEncryptedArgs[:len(quoteCreateServerEncryptedArgs)-1]...), "a/b"),
		"bad create-volume type": {"volume", "create-volume", "--name", "data", "--zone-id", "zone-1", "--size", "10", "--volume-type-id", "voltype-1", "--encryption-type-id", "a/b", "--max-price", "100000"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			f := newEncryptionOrderFixture(t)
			stderr, err := runEncryption(t, f, args...)
			if err == nil {
				t.Fatalf("expected an error for %v", args)
			}
			if exitCode(err) != 2 {
				t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr)
			}
			if n := f.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}

// TestVolumeListEncryptionTypesPrintsIDAndName decodes the live shape, a
// list of key and displayKey objects.
func TestVolumeListEncryptionTypesPrintsIDAndName(t *testing.T) {
	body, err := os.ReadFile("../../testdata/volume/list_encryption_types_keys.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/proj-1/volumes/encryption_types": jsonHandler(http.StatusOK, string(body)),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--output", "table", "volume", "list-encryption-types"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("list-encryption-types: %v (stderr=%s)", err, stderr.String())
	}
	out := stdout.String()
	header := strings.Split(out, "\n")[1]
	for _, col := range []string{" ID ", " Name "} {
		if !strings.Contains(header, col) {
			t.Errorf("header = %q, want column%s", header, col)
		}
	}
	for _, id := range []string{"aes-xts-plain64_128", "aes-xts-plain64_256"} {
		if strings.Count(out, id) < 2 {
			t.Errorf("output lacks %s in both ID and Name:\n%s", id, out)
		}
	}
}

// TestGenDocsEncryptionNotes checks that the generated pages say where the
// type IDs come from, how encryption is priced, and where to read the type
// back.
func TestGenDocsEncryptionNotes(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	read := func(name string) string {
		data, err := os.ReadFile(dir + "/" + name)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", name, err)
		}
		return string(data)
	}
	volume, compute := read("CLI-Volume.md"), read("CLI-Compute.md")
	cases := []struct{ page, op, want string }{
		{volume, "list-encryption-types", "aes-xts-plain64_256"},
		{volume, "quote-create-volume", "list-encryption-types"},
		{volume, "create-volume", "same price"},
		{volume, "create-volume", "get-volume"},
		{volume, "get-volume", "encryption type"},
		{volume, "get-underlying-volume", "get-volume"},
		{volume, "attach-volume", "encrypted"},
		{compute, "quote-create-server", "encryptionVolume"},
		{compute, "create-server", "volume list-encryption-types"},
	}
	for _, c := range cases {
		if section := genDocsSection(t, c.page, c.op); !strings.Contains(section, c.want) {
			t.Errorf("%s section lacks %q:\n%s", c.op, c.want, section)
		}
	}
}

// get-volume and get-underlying-volume ran against a real encrypted volume,
// so their notes must not claim the shape is unverified. list-snapshots has
// no live read and keeps the warning.
func TestVolumeReadNotesAreVerifiedLive(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	data, err := os.ReadFile(dir + "/CLI-Volume.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	page := string(data)
	for _, op := range []string{"get-volume", "get-underlying-volume"} {
		section := genDocsSection(t, page, op)
		if strings.Contains(section, "Unverified live") {
			t.Errorf("%s still says unverified:\n%s", op, section)
		}
		if !strings.Contains(section, "Verified live") {
			t.Errorf("%s lacks the verified-live sentence:\n%s", op, section)
		}
	}
	if section := genDocsSection(t, page, "list-snapshots"); !strings.Contains(section, "Unverified live") {
		t.Errorf("list-snapshots lost its unverified note:\n%s", section)
	}
}
