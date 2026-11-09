package monitor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
)

// ErrQuotaStatusIncomplete marks a partial quota status snapshot.
var ErrQuotaStatusIncomplete = errors.New("monitor: quota status incomplete")

// GetQuotaStatusInput selects parameters for GetQuotaStatus.
type GetQuotaStatusInput struct {
	// Kind selects synthetic, email, or SMS; empty selects all three.
	Kind string
}

// GetQuotaStatusOutput holds the result of GetQuotaStatus.
type GetQuotaStatusOutput struct {
	AsOf     time.Time
	Items    []QuotaStatusRow
	Complete bool
}

// QuotaStatusRow holds one quota status and its known pricing.
type QuotaStatusRow struct {
	Kind, ClassName, PackageName, Status string
	Quota                                *Quota
	Price                                *float64
	StartTime, EndTime                   *time.Time
	TimeLeftSeconds                      *int64
	AutoRenew, RenewWindowOpen           *bool
	NextSafeCommand                      string
	Error                                *QuotaStatusError
}

// QuotaStatusError holds a safe error classification for a status row.
type QuotaStatusError struct {
	Code, Operation string
	Status          int
}

// GetQuotaStatus is a sequential snapshot. Its renewal window describes
// timing only and grants no authority to mutate a quota.
func (c *Client) GetQuotaStatus(ctx context.Context, in *GetQuotaStatusInput) (*GetQuotaStatusOutput, error) {
	const op = "monitor.GetQuotaStatus"
	kinds := []string{"Synthetic", "Email", "SMS"}
	if in != nil && in.Kind != "" {
		switch strings.ToLower(in.Kind) {
		case "synthetic":
			kinds = kinds[:1]
		case "email":
			kinds = kinds[1:2]
		case "sms":
			kinds = kinds[2:]
		default:
			return nil, fmt.Errorf("%s: Kind must be synthetic, email, or sms: %w", op, core.ErrInvalidInput)
		}
	}
	out := &GetQuotaStatusOutput{AsOf: c.now().UTC(), Items: make([]QuotaStatusRow, 0, len(kinds)), Complete: true}
	var credentials monitorReadCredentials
	var causes []error
	reader := quotaCompositeReader{client: c, credentials: &credentials}
	for _, kind := range kinds {
		row := QuotaStatusRow{Kind: kind}
		err := ctx.Err()
		if err == nil {
			row, err = c.quotaStatusRow(ctx, op, kind, out.AsOf, &reader)
		}
		if err != nil {
			out.Complete = false
			causes = append(causes, err)
			metadata := &QuotaStatusError{Code: "RequestFailed", Operation: op}
			var api *core.APIError
			if errors.As(err, &api) {
				metadata.Code = api.Code
				metadata.Status = api.StatusCode
			}
			row = QuotaStatusRow{Kind: kind, Status: "unavailable", Error: metadata}
		}
		out.Items = append(out.Items, row)
	}
	// Retained credentials cover every component, including later refreshes.
	for i := range out.Items {
		row := &out.Items[i]
		c.scrubCatalogText(&credentials, &row.ClassName, &row.PackageName, &row.Status)
		if q := row.Quota; q != nil {
			c.scrubCatalogText(&credentials, &q.ID, &q.Name, &q.Type, &q.Status, &q.PackageID)
		}
	}
	if !out.Complete {
		return out, fmt.Errorf("%s: %w", op, errors.Join(append([]error{ErrQuotaStatusIncomplete}, causes...)...))
	}
	return out, nil
}

func (c *Client) quotaStatusRow(ctx context.Context, op, kind string, asOf time.Time, reader *quotaCompositeReader) (QuotaStatusRow, error) {
	row := QuotaStatusRow{Kind: kind, NextSafeCommand: "monitor get-notification-quota"}
	segment := strings.ToLower(kind)
	if segment == "synthetic" {
		row.NextSafeCommand = "monitor get-synthetic-quota"
	}
	// Read the source quota separately so a detail/list 404 cannot become
	// a none row. Absence here never authorizes a write.
	raw, err := reader.read(ctx, op, "v1", segment, "quota")
	if errors.Is(err, core.ErrNotFound) {
		row.Status = "none"
		return row, nil
	}
	if err != nil {
		return row, err
	}
	quota, err := decodeQuota(op, raw)
	if err != nil {
		return row, err
	}
	if err := ctx.Err(); err != nil {
		return row, err
	}
	if segment == "synthetic" {
		raw, err = reader.read(ctx, op, "v2", "synthetic", quota.ID, "quota-detail")
		if err != nil {
			return row, err
		}
		o := quotaObjectOf(raw)
		api, location := o.integer("api", false), o.integer("location", false)
		_ = o.notificationLimits()
		row.ClassName = o.text("className", false)
		_ = o.text("tierName", true)
		row.Price = o.price()
		if o.text("resourceId", false) != quota.ID || o.text("packageId", false) != quota.PackageID || o.invalid {
			return row, quotaResponseError(op)
		}
		if row.ClassName == "Basic" && row.Price != nil && *row.Price == 0 && api == 10 && location == 1 {
			windowEnd := asOf.Add(7 * 24 * time.Hour)
			open := quota.Status == QuotaStatusActive && quota.EndTime.After(asOf) && !quota.EndTime.After(windowEnd)
			row.RenewWindowOpen = &open
			if !quota.POC && !quota.AutoRenew {
				if open {
					row.NextSafeCommand = "monitor renew-synthetic-quota"
				}
				if quota.Status == QuotaStatusInTrash && quota.EndTime.Before(asOf) {
					row.NextSafeCommand = "monitor recover-synthetic-quota"
				}
			}
		}
	} else {
		raw, readErr := reader.read(ctx, op, "v1", segment, "packages")
		if readErr != nil {
			return row, readErr
		}
		packages, ok := quotaArray(raw)
		if !ok {
			return row, quotaResponseError(op)
		}
		seen := make(map[string]bool, len(packages))
		for _, item := range packages {
			o := quotaObjectOf(item)
			p := NotificationPackage{ID: o.text("id", false), Name: o.text("name", false), Price: o.price(), Amount: o.integer("amount", false)}
			if o.invalid || p.ID == "" || seen[p.ID] {
				return row, quotaResponseError(op)
			}
			seen[p.ID] = true
			if p.ID == quota.PackageID {
				row.PackageName = p.Name
				row.Price = p.Price
			}
		}
	}
	row.Status = quota.Status
	quota.StartTime = quota.StartTime.UTC()
	quota.EndTime = quota.EndTime.UTC()
	quota.CreatedAt = quota.CreatedAt.UTC()
	quota.UpdatedAt = quota.UpdatedAt.UTC()
	row.Quota = &quota
	row.StartTime = &quota.StartTime
	row.EndTime = &quota.EndTime
	row.AutoRenew = &quota.AutoRenew
	seconds := quotaSecondsLeft(quota.EndTime, asOf)
	row.TimeLeftSeconds = &seconds
	return row, nil
}

// Parsed RFC 3339 timestamps have a much wider range than time.Duration.
// Subtract seconds and normalize the fractional part before truncating
// toward zero, retaining the sign for already-expired quotas.
func quotaSecondsLeft(end, asOf time.Time) int64 {
	seconds := end.Unix() - asOf.Unix()
	nanos := end.Nanosecond() - asOf.Nanosecond()
	if seconds > 0 && nanos < 0 {
		seconds--
	} else if seconds < 0 && nanos > 0 {
		seconds++
	}
	return seconds
}
