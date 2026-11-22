package compute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestServerGuardRejectsSubstitutedIdentity(t *testing.T) {
	for _, body := range []string{`{"data":null}`, `{"data":{"status":"ACTIVE"}}`, `{"data":{"uuid":"server-2","status":"ACTIVE"}}`, `{"data":{"uuid":"server-2","UUID":"server-1","status":"ACTIVE"}}`, `{"data":{"uuid":"server-1","status":"ACTIVE","STATUS":"STOPPED"}}`} {
		for _, action := range []string{"stop", "delete"} {
			t.Run(action+body, func(t *testing.T) {
				writes := 0
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						writes++
						w.WriteHeader(202)
						return
					}
					_, _ = w.Write([]byte(body))
				}))
				var err error
				if action == "stop" {
					_, err = c.StopServer(context.Background(), &StopServerInput{ServerID: "server-1", NoWait: true})
				} else {
					_, err = c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1", NoWait: true})
				}
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.Code != "InvalidResponse" || writes != 0 {
					t.Fatalf("err=%v writes=%d", err, writes)
				}
			})
		}
	}
}

func TestSSHKeyRejectsCredentialReflection(t *testing.T) {
	for _, operation := range []string{"create", "import", "get", "list"} {
		for _, field := range []string{"id", "name", "pubKey", "status", "createdAt"} {
			t.Run(operation+"/"+field, func(t *testing.T) {
				c, _, captures := newServerSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
					row := map[string]string{"id": "key-1", "privateKey": "synthetic-private-key", field: credential}
					if operation == "create" || operation == "import" {
						w.WriteHeader(201)
					}
					if operation == "list" {
						_ = json.NewEncoder(w).Encode(map[string]any{"listData": []any{row}, "page": 1, "pageSize": 10000, "totalPage": 1, "totalItem": 1})
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"data": row})
					}
				}), 0)
				var out any
				var err error
				switch operation {
				case "create":
					out, err = c.CreateSSHKey(context.Background(), &CreateSSHKeyInput{Name: "key"})
				case "import":
					out, err = c.ImportSSHKey(context.Background(), &ImportSSHKeyInput{Name: "key", PublicKey: "ssh-rsa synthetic-public-key"})
				case "get":
					out, err = c.GetSSHKey(context.Background(), &GetSSHKeyInput{SSHKeyID: "key-1"})
				case "list":
					out, err = c.ListSSHKeys(context.Background(), nil)
				}
				if err == nil || captures.Load() != 0 {
					t.Fatalf("err=%v captures=%d", err, captures.Load())
				}
				if strings.Contains(fmt.Sprintf("%+v %+v", out, err), "synthetic-token-") {
					t.Fatal("credential escaped")
				}
			})
		}
	}
}

func TestSSHKeyRejectsIssuedSecretInModel(t *testing.T) {
	for _, field := range []string{"id", "name", "pubKey", "status", "createdAt"} {
		t.Run(field, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(201)
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"id": "key-1", "privateKey": "synthetic-issued-secret", field: "synthetic-issued-secret"}})
			}))
			out, err := c.CreateSSHKey(context.Background(), &CreateSSHKeyInput{Name: "key"})
			if out != nil || err == nil {
				t.Fatal("issued private key escaped through public model")
			}
			if strings.Contains(err.Error(), "synthetic-issued-secret") {
				t.Fatal("issued private key escaped through error")
			}
		})
	}
}
