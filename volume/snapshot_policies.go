package volume

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

type ListSnapshotPoliciesInput struct {
	BackendID string `vngcloud:"required"`
	Page      int
	Size      int
}

type ListSnapshotPoliciesOutput = core.PagedList[SnapshotPolicy]

func (c *Client) ListSnapshotPolicies(ctx context.Context, in *ListSnapshotPoliciesInput) (*ListSnapshotPoliciesOutput, error) {
	const op = "volume.ListSnapshotPolicies"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "BackendID", in.BackendID); err != nil {
		return nil, err
	}
	if in.Page < 0 || in.Size < 0 {
		return nil, fmt.Errorf("%w: %s requires non-negative Page and Size", core.ErrInvalidInput, op)
	}
	if err := c.checkSnapshotRegion(op); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	page, size := in.Page, in.Size
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 10
	}
	q := url.Values{"backendId": {in.BackendID}, "projectId": {projectID}, "page": {strconv.Itoa(page)}, "size": {strconv.Itoa(size)}}
	var resp snapshotPoliciesResponse
	if err := c.c.DoJSON(ctx, transport.Request{Operation: op, Method: "GET", URL: c.snapshotBackupURL("snapshot-policies", q), OK: []int{200}}, &resp); err != nil {
		return nil, err
	}
	if resp.Items == nil || resp.Page == nil || resp.PageSize == nil || resp.TotalPages == nil || resp.TotalItems == nil {
		return nil, snapshotShapeError(op)
	}
	return core.NewPagedList(resp.Items, *resp.Page, *resp.PageSize, *resp.TotalPages, *resp.TotalItems), nil
}

func (c *Client) checkSnapshotRegion(op string) error {
	// Overrides cannot expand the verified region scope.
	if endpoints.VServerBackup(c.c.Region()) == "" {
		return fmt.Errorf("%w: %s requires a supported snapshot region", core.ErrInvalidConfig, op)
	}
	return nil
}

func (c *Client) snapshotBackupURL(resource string, q url.Values) string {
	return c.c.RouteURL(routes.Route{Product: routes.ProductVServerBackup, Version: "v1", Parts: []string{resource}, Query: q})
}

func snapshotShapeError(op string) error {
	return &core.APIError{Operation: op, Message: "invalid response shape; body withheld"}
}

type snapshotPoliciesResponse struct {
	Items      []SnapshotPolicy `json:"items"`
	Page       *int             `json:"page"`
	PageSize   *int             `json:"pageSize"`
	TotalPages *int             `json:"totalPages"`
	TotalItems *int             `json:"totalItems"`
}

func (r *snapshotPoliciesResponse) UnmarshalJSON(data []byte) error {
	type wire snapshotPoliciesResponse
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return snapshotShapeError("volume.ListSnapshotPolicies")
	}
	*r = snapshotPoliciesResponse(decoded)
	return nil
}

type SnapshotPolicy struct {
	ID                  string               `json:"id"`
	Name                string               `json:"name"`
	PolicyType          string               `json:"policyType"`
	Config              SnapshotPolicyConfig `json:"config"`
	CreatedAt           string               `json:"createdAt"`
	UpdatedAt           string               `json:"updatedAt"`
	SnapshotServerCount int                  `json:"snapshotServerCount"`
	SnapshotVolumeCount int                  `json:"snapshotVolumeCount"`
}

type SnapshotPolicyConfig struct {
	Hour              int                         `json:"hour"`
	Minute            int                         `json:"minute"`
	TimeZone          string                      `json:"timeZone"`
	HourlyEnabled     bool                        `json:"hourlyEnabled"`
	HourlyConfig      *SnapshotPolicyHourlyConfig `json:"hourlyConfig,omitempty"`
	DailyEnabled      bool                        `json:"dailyEnabled"`
	DailyConfig       *SnapshotPolicyDailyConfig  `json:"dailyConfig,omitempty"`
	WeeklyEnabled     bool                        `json:"weeklyEnabled"`
	MonthlyEnabled    bool                        `json:"monthlyEnabled"`
	IsProtectedServer bool                        `json:"isProtectedServer"`
	StatusSendEmail   []string                    `json:"statusSendEmail"`
}

type SnapshotPolicyHourlyConfig struct {
	Interval  *int `json:"interval,omitempty"`
	Retention *int `json:"retention,omitempty"`
}

type SnapshotPolicyDailyConfig struct {
	Retention *int `json:"retention,omitempty"`
}
