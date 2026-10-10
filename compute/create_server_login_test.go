package compute

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
)

const loginCloudConfig = "#cloud-config\nssh_authorized_keys:\n  - <public-key>\n"

func TestCreateServerLoginBodies(t *testing.T) {
	for _, login := range []string{"ssh-key", "user-data"} {
		t.Run(login, func(t *testing.T) {
			var got map[string]any
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routeServerWriteRequest(t, w, r, emptyListServersPage, func(w http.ResponseWriter, r *http.Request) {
					got = decodeComputeBody(t, r)
					w.WriteHeader(http.StatusAccepted)
					_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
				}, nil)
			}))
			in := validCreateServerInput()
			in.NoWait = true
			in.MaxPrice = 347800
			want := map[string]any{
				"name": "web-1", "zoneId": "zone-1", "flavorId": "flavor-1", "imageId": "image-1",
				"networkId": "vpc-1", "subnetId": "subnet-1", "securityGroup": []any{"sg-1"},
				"rootDiskSize": float64(20), "rootDiskTypeId": "voltype-1",
				"encryptionVolume": false, "isEnableAutoRenew": false,
			}
			if login == "user-data" {
				in.SSHKeyID = ""
				in.UserData = loginCloudConfig
				want["userData"] = base64.StdEncoding.EncodeToString([]byte(loginCloudConfig))
				want["userDataBase64Encoded"] = true
			} else {
				want["sshKeyId"] = "key-1"
			}
			if _, err := c.CreateServer(context.Background(), in); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("body = %v, want %v", got, want)
			}
		})
	}
}

func TestCreateServerRequiresExactlyOneLoginBeforeRequests(t *testing.T) {
	for _, both := range []bool{false, true} {
		t.Run(map[bool]string{false: "neither", true: "both"}[both], func(t *testing.T) {
			var requests atomic.Int64
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusBadRequest)
			}))
			in := validCreateServerInput()
			in.MaxPrice = 347800
			want := "login is required"
			if both {
				in.UserData = loginCloudConfig
				want = "GreenNode refuses user data together with an SSH key"
			} else {
				in.SSHKeyID = ""
			}
			_, err := c.CreateServer(context.Background(), in)
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), want) || (both && !strings.Contains(err.Error(), "cloud-config")) || (!both && !strings.Contains(err.Error(), "user data that installs keys")) {
				t.Fatalf("error = %v, want login guidance", err)
			}
			for _, secret := range []string{loginCloudConfig, base64.StdEncoding.EncodeToString([]byte(loginCloudConfig))} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("error contains user data")
				}
			}
			if requests.Load() != 0 {
				t.Fatalf("requests = %d, want 0", requests.Load())
			}
		})
	}
}

func TestQuoteCreateServerBothLoginChoices(t *testing.T) {
	var bodies [][]byte
	c := newTestClient(t, serverQuoteRecorder(t, &bodies))
	for _, userData := range []bool{false, true} {
		in := validCreateServerInput()
		if userData {
			in.SSHKeyID = ""
			in.UserData = loginCloudConfig
		}
		if _, err := c.QuoteCreateServer(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	if len(bodies) != 2 || !reflect.DeepEqual(bodies[0], bodies[1]) {
		t.Fatalf("quote bodies differ: %s", bodies)
	}
	info := quoteInfoOf(t, bodies[0])
	for _, key := range []string{"sshKeyId", "userData", "userDataBase64Encoded"} {
		if _, ok := info[key]; ok {
			t.Fatalf("quote contains %s", key)
		}
	}
}
