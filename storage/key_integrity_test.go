package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestS3KeyRejectsReflectedIssuedSecret(t *testing.T) {
	const secret = "synthetic-issued-secret"
	for _, field := range []string{"userKeyId", "accessKey", "projectId", "regionId", "userId", "subUserId", "createdDate"} {
		for _, list := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/list=%t", field, list), func(t *testing.T) {
				key := map[string]any{"userKeyId": "key-1", "accessKey": "access-1", "secretKey": secret}
				key[field] = "prefix-" + secret
				body := map[string]any{"success": true, "data": key}
				if list {
					body = map[string]any{"success": true, "datas": []any{key}}
				}
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				var captured captureLog
				c := New(testutil.NewConfigWithCapture(t, serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(raw) }), captured.hook))
				var out any
				if list {
					out, err = c.ListS3Keys(context.Background(), &ListS3KeysInput{ProjectID: "proj-1"})
				} else {
					out, err = c.CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
				}
				if err == nil {
					t.Fatal("reflected issued secret accepted")
				}
				encoded, _ := json.Marshal(out)
				if strings.Contains(fmt.Sprintf("%v %+v %#v %s %s", out, err, err, encoded, captured.all.String()), secret) {
					t.Fatal("issued secret escaped through ordinary fields")
				}
			})
		}
	}
}

func TestCreateS3KeyErrorsWithholdIssuedSecrets(t *testing.T) {
	for _, status := range []int{200, 400, 503} {
		s := &keyServer{status: status, body: `{"success":false,"code":"synthetic-issued-secret","message":"synthetic-issued-secret","errorMsg":"synthetic-issued-secret","data":{"secretKey":"synthetic-issued-secret"}}`}
		_, err := newTestClient(t, s.handler(t)).CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
		if err == nil || strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), "synthetic-issued-secret") {
			t.Fatalf("status %d: unsafe error", status)
		}
	}
}

func TestCreateS3KeyRejectsConflictingSecretFields(t *testing.T) {
	for _, field := range []string{`"secretKey":"first-secret","secretKey":"last-secret"`, `"secretKey":"first-secret","SECRETKEY":"last-secret"`, `"userKeyId":"first","USERKEYID":"last","secretKey":"synthetic-secret"`} {
		s := &keyServer{status: 200, body: `{"success":true,"data":{"userKeyId":"key-1","accessKey":"access-1",` + field + `}}`}
		out, err := newTestClient(t, s.handler(t)).CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
		if err == nil || out != nil {
			t.Fatalf("out = %v, error = %v", out, err)
		}
	}
}

func TestS3KeyRecoveryRequiresOwnership(t *testing.T) {
	s := &keyServer{status: 503, body: `{}`}
	_, err := newTestClient(t, s.handler(t)).CreateS3Key(context.Background(), &CreateS3KeyInput{ProjectID: "proj-1"})
	if err == nil || strings.Contains(err.Error(), "delete any") || !strings.Contains(err.Error(), "ownership") {
		t.Fatalf("unsafe recovery: %v", err)
	}
}
