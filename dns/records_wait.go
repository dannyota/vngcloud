package dns

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// getRecord is GetRecord's request, reused by every record wait under the
// calling write's own operation name, so a failure names the write it
// happened inside rather than "dns.GetRecord". zoneID and recordID are
// assumed already checked with core.CheckPathID.
func (c *Client) getRecord(ctx context.Context, op, zoneID, recordID string) (*Record, error) {
	var resp struct {
		Data Record `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.url([]string{"dns", "hosted-zone", zoneID, "record", recordID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// settleRecord is CreateRecord and UpdateRecord's post-write wait unless
// NoWait is set: it reads the zone and the record on every poll iteration
// until settled reports true for a read of the record (the write's own
// success condition, given the zone read alongside it so a caller can
// compare a sent SubDomain against the zone's DomainName), or either the
// zone's or the record's Status becomes StatusError, whichever happens
// first. Both record create and record update move the zone out of ACTIVE
// for several seconds, so settled also requires the zone to have returned
// to StatusActive.
//
// It returns the last record it read alongside the outcome, following the
// same contract settleZone documents: a nil error when settled matched, an
// error wrapping ErrFailed when the zone or the record reached StatusError
// first, or one wrapping ErrNotSettled for every other way the wait can
// end, since the write itself already succeeded by the time settleRecord is
// called. The returned record is nil only when no read after the write ever
// succeeded, in which case the caller falls back to whatever the write
// itself produced.
func (c *Client) settleRecord(ctx context.Context, op, zoneID, recordID string, settled func(record *Record, zone *HostedZone) bool) (*Record, error) {
	var record *Record
	err := poll(ctx, c.now, c.sleep,
		func(ctx context.Context) (bool, error) {
			zone, err := c.getHostedZone(ctx, op, zoneID)
			if err != nil {
				return true, err
			}
			rec, err := c.getRecord(ctx, op, zoneID, recordID)
			if err != nil {
				return true, err
			}
			record = rec
			if zone.Status == StatusError || rec.Status == StatusError {
				return true, fmt.Errorf("%w: %s: record %s in hosted zone %s is ERROR", ErrFailed, op, recordID, zoneID)
			}
			if zone.Status == StatusActive && settled(rec, zone) {
				return true, nil
			}
			return false, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: record %s in hosted zone %s was accepted; do not send the same write again", ErrNotSettled, op, recordID, zoneID)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: record %s in hosted zone %s: %w", ErrNotSettled, op, recordID, zoneID, err)
	}
	return record, err
}

// waitRecordGone is DeleteRecord's post-write wait unless NoWait is set: it
// reads the record until the read fails with NotFound, or returns an error
// wrapping ErrNotSettled once the bound runs out first. Unlike settleRecord,
// it reads only the record: a record delete leaves the zone ACTIVE within a
// second, per the design, so there is no zone lock to poll through here.
//
// The delete itself already succeeded by the time waitRecordGone is called,
// so a canceled sleep or any other read failure means the same thing as the
// bound running out: do not delete the record again. Every error path here
// wraps ErrNotSettled.
func (c *Client) waitRecordGone(ctx context.Context, op, zoneID, recordID string) error {
	err := poll(ctx, c.now, c.sleep,
		func(ctx context.Context) (bool, error) {
			_, err := c.getRecord(ctx, op, zoneID, recordID)
			if err == nil {
				return false, nil
			}
			if core.IsNotFound(err) {
				return true, nil
			}
			return true, err
		},
		func() error {
			return fmt.Errorf("%w: %s: record %s in hosted zone %s was accepted; do not delete it again", ErrNotSettled, op, recordID, zoneID)
		},
	)
	if err != nil && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: record %s in hosted zone %s: %w", ErrNotSettled, op, recordID, zoneID, err)
	}
	return err
}
