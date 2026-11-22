package network

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestSubnetDeleteRequiresValidACLAssociations(t *testing.T) {
	for _, associations := range []string{`[null]`, `[""]`, `[".."]`, `["subnet/other"]`, `["subnet-2",null]`, `["subnet-2","subnet-2"]`, `[]`, `["subnet-2"]`} {
		t.Run(associations, func(t *testing.T) {
			deletes := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodDelete:
					deletes++
					w.WriteHeader(200)
				case strings.Contains(r.URL.Path, "/servers/subnets/"):
					_, _ = w.Write([]byte(`[]`))
				case strings.HasSuffix(r.URL.Path, "/network-interfaces-elastic"), strings.HasSuffix(r.URL.Path, "/virtualIpAddress"):
					_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
				case strings.HasSuffix(r.URL.Path, "/network-acl/list"):
					_, _ = w.Write([]byte(`{"listData":[{"uuid":"acl-1","networkId":"vpc-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
				case strings.HasSuffix(r.URL.Path, "/network-acl/acl-1"):
					body := strings.Replace(aclJSON("ACTIVE", false, nil, []string{}), `"subnetAssociationList":[]`, `"subnetAssociationList":`+associations, 1)
					_, _ = w.Write([]byte(body))
				default:
					_, _ = w.Write([]byte(`{"uuid":"subnet-1","status":"ACTIVE","networkUuid":"vpc-1"}`))
				}
			}))
			out, err := c.DeleteSubnet(context.Background(), &DeleteSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1", NoWait: true})
			if associations == `[]` || associations == `["subnet-2"]` {
				if err != nil || out == nil || deletes != 1 {
					t.Fatalf("valid associations: err=%v deletes=%d", err, deletes)
				}
			} else {
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || deletes != 0 {
					t.Fatalf("malformed associations: err=%v deletes=%d", err, deletes)
				}
			}
		})
	}
}
