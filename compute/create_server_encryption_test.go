package compute

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func encryptionServerInput(root, data string) *CreateServerInput {
	in := validCreateServerInput()
	in.RootDiskEncryptionTypeID = root
	if data != "" {
		in.DataDiskSize = 20
		in.DataDiskTypeID = "voltype-1"
		in.DataDiskEncryptionTypeID = data
	}
	return in
}

func TestCreateServerBodyEncryptionKeys(t *testing.T) {
	for name, tc := range map[string]struct {
		root, data string
		wantVolume bool
		wantKeys   map[string]any
		absent     []string
	}{
		"none": {wantVolume: false, absent: []string{"rootDiskEncryptionType", "dataDiskEncryptionType"}},
		"root": {root: "aes-xts-plain64_256", wantVolume: true, wantKeys: map[string]any{"rootDiskEncryptionType": "aes-xts-plain64_256"}, absent: []string{"dataDiskEncryptionType"}},
		"data": {data: "aes-xts-plain64_128", wantVolume: true, wantKeys: map[string]any{"dataDiskEncryptionType": "aes-xts-plain64_128"}, absent: []string{"rootDiskEncryptionType"}},
		"both": {root: "aes-xts-plain64_256", data: "aes-xts-plain64_128", wantVolume: true, wantKeys: map[string]any{"rootDiskEncryptionType": "aes-xts-plain64_256", "dataDiskEncryptionType": "aes-xts-plain64_128"}},
	} {
		t.Run(name, func(t *testing.T) {
			in := encryptionServerInput(tc.root, tc.data)
			body, err := buildCreateServerBody("op", in)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(body)
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if got["encryptionVolume"] != tc.wantVolume {
				t.Fatalf("encryptionVolume = %v, want %v: %s", got["encryptionVolume"], tc.wantVolume, raw)
			}
			for k, v := range tc.wantKeys {
				if got[k] != v {
					t.Fatalf("%s = %v, want %v: %s", k, got[k], v, raw)
				}
			}
			for _, k := range tc.absent {
				if _, ok := got[k]; ok {
					t.Fatalf("body has %s: %s", k, raw)
				}
			}
			info, err := buildServerQuoteInfo("op", in)
			if err != nil {
				t.Fatal(err)
			}
			if info["encryptionVolume"] != tc.wantVolume {
				t.Fatalf("quote encryptionVolume = %v, want %v", info["encryptionVolume"], tc.wantVolume)
			}
			for _, k := range []string{"rootDiskEncryptionType", "dataDiskEncryptionType"} {
				if _, ok := info[k]; ok {
					t.Fatalf("quote info has %s, which the console's price request never sends: %v", k, info)
				}
			}
		})
	}
}

func TestCreateServerRefusesDataDiskEncryptionWithoutDataDisk(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	in := validCreateServerInput()
	in.DataDiskEncryptionTypeID = "aes-xts-plain64_256"
	in.MaxPrice = 500000
	_, err := c.QuoteCreateServer(context.Background(), in)
	if !errors.Is(err, vngcloud.ErrInvalidInput) || !strings.Contains(err.Error(), "DataDiskEncryptionTypeID") {
		t.Fatalf("quote err = %v, want ErrInvalidInput naming DataDiskEncryptionTypeID", err)
	}
	if _, err := c.CreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("create err = %v, want ErrInvalidInput", err)
	}
}

func TestCreateServerRejectsBadEncryptionTypeIDs(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	for _, bad := range []string{" ", "..", ".", "a/b", "a?b", "a\nb"} {
		for field, set := range map[string]func(in *CreateServerInput){
			"RootDiskEncryptionTypeID": func(in *CreateServerInput) { in.RootDiskEncryptionTypeID = bad },
			"DataDiskEncryptionTypeID": func(in *CreateServerInput) {
				in.DataDiskSize, in.DataDiskTypeID, in.DataDiskEncryptionTypeID = 20, "voltype-1", bad
			},
		} {
			in := validCreateServerInput()
			set(in)
			if _, err := c.QuoteCreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("quote %s=%q err = %v, want ErrInvalidInput", field, bad, err)
			}
			if _, err := c.CreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("create %s=%q err = %v, want ErrInvalidInput", field, bad, err)
			}
		}
	}
}

func TestCreateServerSendsEncryptionKeysAndGuardPricesThem(t *testing.T) {
	var bodies [][]byte
	var createBody map[string]any
	c := withInstantSleep(newTestClient(t, serverQuoteRecorderWithCreate(t, &bodies, &createBody)))
	in := encryptionServerInput("aes-xts-plain64_256", "aes-xts-plain64_128")
	in.MaxPrice = 500000
	if _, err := c.CreateServer(context.Background(), in); err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if got := quoteInfoOf(t, bodies[0])["encryptionVolume"]; got != true {
		t.Fatalf("guard quote encryptionVolume = %v, want true", got)
	}
	if createBody["rootDiskEncryptionType"] != "aes-xts-plain64_256" || createBody["dataDiskEncryptionType"] != "aes-xts-plain64_128" || createBody["encryptionVolume"] != true {
		t.Fatalf("create body = %v", createBody)
	}
}

func serverQuoteRecorderWithCreate(t *testing.T, bodies *[][]byte, createBody *map[string]any) http.HandlerFunc {
	inner := serverQuoteRecorder(t, bodies)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/servers" {
			body := decodeComputeBody(t, r)
			*createBody = body
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			return
		}
		inner(w, r)
	}
}
