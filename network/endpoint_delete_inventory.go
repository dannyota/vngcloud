package network

import (
	"context"
	"encoding/json"
	"strings"
)

func (o *endpointOp) deleteInventory(ctx context.Context) ([]endpointRow, error) {
	type row struct {
		UUID          string `json:"uuid"`
		VPCID         string `json:"vpcId"`
		Status        string `json:"status"`
		EndpointType  string `json:"endpointType"`
		EnableDNSName bool   `json:"enableDnsName"`
		Service       struct {
			UUID string `json:"uuid"`
		} `json:"service"`
		Project struct {
			VServerProjectID string `json:"VServerProjectId"`
		} `json:"project"`
	}
	return endpointScan(ctx, o, []string{"endpoints"}, nil, func(raw json.RawMessage) ([]endpointRow, error) {
		rows, err := natDecodeRows[row](o.op, raw, "uuid", "vpcId", "status", "endpointType", "enableDnsName", "service", "project")
		if err != nil {
			return nil, endpointSafeError(o.op, err)
		}
		var fields []map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || len(fields) != len(rows) {
			return nil, endpointInvalid(o.op)
		}
		result := make([]endpointRow, len(rows))
		for i, r := range rows {
			for key := range fields[i] {
				for _, name := range []string{"endpointResource", "endpointDomains"} {
					if strings.EqualFold(key, name) && key != name {
						return nil, endpointInvalid(o.op)
					}
				}
			}
			if !endpointIDForm.MatchString(r.UUID) || !vpcPattern.MatchString(r.VPCID) || !uuidPattern.MatchString(r.Service.UUID) || r.Project.VServerProjectID != o.scope.project || r.Status == "" {
				return nil, endpointInvalid(o.op)
			}
			result[i].UUID, result[i].VPCID, result[i].Status, result[i].EndpointType, result[i].EnableDNSName = r.UUID, r.VPCID, r.Status, &r.EndpointType, r.EnableDNSName
			result[i].Service.UUID = r.Service.UUID
			result[i].Project.VServerProjectID = r.Project.VServerProjectID
			result[i].resource, result[i].domains = fields[i]["endpointResource"], fields[i]["endpointDomains"]
		}
		return result, nil
	}, func(r endpointRow) string { return r.UUID })
}
