package dns

import (
	"context"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// Record create defaults, sent when the caller leaves the field zero, per
// the console's own defaults. Zero is never a valid TTL or routing policy,
// so ADR 0002 rule 3 needs no pointer for either field.
const (
	defaultRecordTTL     = 300
	defaultRoutingPolicy = "simple-routing"
)

type createRecordBody struct {
	SubDomain           string        `json:"subDomain"`
	TTL                 int           `json:"ttl"`
	Type                string        `json:"type"`
	RoutingPolicy       string        `json:"routingPolicy"`
	Value               []RecordValue `json:"value"`
	EnableStickySession *bool         `json:"enableStickySession,omitempty"`
}

// CreateRecordInput creates a record in a hosted zone. SubDomain left empty
// creates the zone apex; the server rejects "@" for the same purpose.
// TTL left zero sends 300, the console default; RoutingPolicy left empty
// sends "simple-routing". NoWait skips the post-create wait for
// StatusActive; see the package doc's discussion of waits.
type CreateRecordInput struct {
	HostedZoneID string        `vngcloud:"required"`
	Type         string        `vngcloud:"required"`
	Values       []RecordValue `vngcloud:"required"`

	SubDomain     string
	TTL           int
	RoutingPolicy string
	StickySession *bool
	NoWait        bool
}

type CreateRecordOutput struct {
	Record Record
}

// CreateRecord creates a record in a hosted zone. It first runs the same
// pre-write wait every record write and every zone update and delete runs:
// it reads the zone until Status is StatusActive or StatusError, the two
// states the server accepts a write against, and returns ErrZoneBusy with
// nothing sent if the wait's own bound runs out first.
//
// The create itself is a POST and is never retried after a failure that may
// have already reached the server: after any error that is not a 4xx
// *core.APIError or core.ErrInvalidInput, the record may exist, and the
// caller lists records with ListRecords before creating it again, rather
// than retrying blind. The zone lock's own 400, if the pre-write wait above
// still lost a race to another writer, is one such 4xx and is likewise never
// retried.
//
// Without NoWait, CreateRecord then waits for the new record, and the zone
// it belongs to, to both reach StatusActive; every record write moves its
// zone out of ACTIVE for several seconds. If the record or the zone reaches
// StatusError instead, or the wait's bound runs out, or a read or a sleep in
// that wait fails, such as from a canceled ctx, the returned error wraps
// ErrFailed or ErrNotSettled and the Output still holds the record: the
// last one a read returned, or, if none did, the record the create response
// itself carried. Either way the Output is never nil and the caller keeps
// the new record's id.
func (c *Client) CreateRecord(ctx context.Context, in *CreateRecordInput) (*CreateRecordOutput, error) {
	const op = "dns.CreateRecord"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "HostedZoneID", in.HostedZoneID); err != nil {
		return nil, err
	}

	if err := c.lockWrite(ctx); err != nil {
		return nil, err
	}
	defer c.unlockWrite()

	if _, err := c.waitZoneReady(ctx, op, in.HostedZoneID); err != nil {
		return nil, err
	}

	ttl := in.TTL
	if ttl == 0 {
		ttl = defaultRecordTTL
	}
	routingPolicy := in.RoutingPolicy
	if routingPolicy == "" {
		routingPolicy = defaultRoutingPolicy
	}

	var resp struct {
		Data Record `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.url([]string{"dns", "hosted-zone", in.HostedZoneID, "record"}, nil),
		Body: createRecordBody{
			SubDomain:           in.SubDomain,
			TTL:                 ttl,
			Type:                in.Type,
			RoutingPolicy:       routingPolicy,
			Value:               in.Values,
			EnableStickySession: in.StickySession,
		},
		OK: []int{200},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, err
	}
	record := resp.Data
	if record.ID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status, Message: "create response had no id"}
	}
	if in.NoWait {
		return &CreateRecordOutput{Record: record}, nil
	}

	settled, err := c.settleRecord(ctx, op, in.HostedZoneID, record.ID, func(r *Record, _ *HostedZone) bool {
		return r.Status == StatusActive
	})
	if settled == nil {
		// The create already succeeded; no read after it ever came back, so
		// fall back to the create response itself, which at least carries
		// the new record's id, rather than losing it to a nil Output.
		settled = &record
	}
	return &CreateRecordOutput{Record: *settled}, err
}

// UpdateRecordInput changes a record. Only non-nil fields are sent, since
// the API itself applies a partial body; unlike UpdateHostedZone, the SDK
// does no read-merge here. At least one field must be set.
type UpdateRecordInput struct {
	HostedZoneID string `vngcloud:"required"`
	RecordID     string `vngcloud:"required"`

	SubDomain     *string
	Type          *string
	TTL           *int
	RoutingPolicy *string
	Values        *[]RecordValue
	StickySession *bool
	NoWait        bool
}

type UpdateRecordOutput struct {
	Record Record
}

// UpdateRecord changes a record, sending only the fields the caller set.
// It first runs the same pre-write wait CreateRecord does, for the same
// reason: nothing has been sent yet, and a busy zone past the wait's bound
// returns ErrZoneBusy with nothing sent.
//
// Without NoWait, it then waits for the record, and the zone it belongs to,
// to both reach StatusActive with the sent fields; a record shown as the
// zone's SubDomain is checked against the full name, since the server
// returns SubDomain as the full name rather than the label sent. It returns
// an error wrapping ErrFailed if the record or the zone reaches StatusError,
// or one wrapping ErrNotSettled if that wait's own bound runs out or a read
// or a sleep in it fails, such as from a canceled ctx. With NoWait, it
// returns after one read instead of waiting, and wraps that same read's
// failure in ErrNotSettled too, since the PUT above has already succeeded
// by then. Every one of these outcomes keeps a non-nil Output: the last
// record a read returned, or, if none did, one built from the fields the PUT
// itself sent.
func (c *Client) UpdateRecord(ctx context.Context, in *UpdateRecordInput) (*UpdateRecordOutput, error) {
	const op = "dns.UpdateRecord"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "HostedZoneID", in.HostedZoneID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "RecordID", in.RecordID); err != nil {
		return nil, err
	}
	if in.SubDomain == nil && in.Type == nil && in.TTL == nil && in.RoutingPolicy == nil && in.Values == nil && in.StickySession == nil {
		return nil, fmt.Errorf("%w: %s requires at least one field to change", core.ErrInvalidInput, op)
	}

	if err := c.lockWrite(ctx); err != nil {
		return nil, err
	}
	defer c.unlockWrite()

	if _, err := c.waitZoneReady(ctx, op, in.HostedZoneID); err != nil {
		return nil, err
	}

	body := map[string]any{}
	if in.SubDomain != nil {
		body["subDomain"] = *in.SubDomain
	}
	if in.Type != nil {
		body["type"] = *in.Type
	}
	if in.TTL != nil {
		body["ttl"] = *in.TTL
	}
	if in.RoutingPolicy != nil {
		body["routingPolicy"] = *in.RoutingPolicy
	}
	if in.Values != nil {
		body["value"] = *in.Values
	}
	if in.StickySession != nil {
		body["enableStickySession"] = *in.StickySession
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.url([]string{"dns", "hosted-zone", in.HostedZoneID, "record", in.RecordID}, nil),
		Body:      body,
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	// The PUT above already succeeded, so from here on every error means
	// the write may have landed and must not be sent again; fallback is
	// what the caller falls back to when no read after the write confirms
	// it, built from the fields the PUT itself sent.
	fallback := Record{ID: in.RecordID, HostedZoneID: in.HostedZoneID}
	if in.SubDomain != nil {
		fallback.SubDomain = *in.SubDomain
	}
	if in.Type != nil {
		fallback.Type = *in.Type
	}
	if in.TTL != nil {
		fallback.TTL = *in.TTL
	}
	if in.RoutingPolicy != nil {
		fallback.RoutingPolicy = *in.RoutingPolicy
	}
	if in.Values != nil {
		fallback.Value = *in.Values
	}
	if in.StickySession != nil {
		fallback.EnableStickySession = in.StickySession
	}

	if in.NoWait {
		record, err := c.getRecord(ctx, op, in.HostedZoneID, in.RecordID)
		if err != nil {
			return &UpdateRecordOutput{Record: fallback}, fmt.Errorf("%w: %s: record %s: %w", ErrNotSettled, op, in.RecordID, err)
		}
		return &UpdateRecordOutput{Record: *record}, nil
	}

	settled, err := c.settleRecord(ctx, op, in.HostedZoneID, in.RecordID, func(r *Record, zone *HostedZone) bool {
		return r.Status == StatusActive && recordMatchesUpdate(r, zone, in)
	})
	if settled == nil {
		settled = &fallback
	}
	return &UpdateRecordOutput{Record: *settled}, err
}

// recordMatchesUpdate reports whether r shows every field that in's caller
// set. SubDomain is checked against the zone's full name, since the
// server returns SubDomain as the full name (the zone's DomainName for the
// apex, or "<label>.<DomainName>" otherwise) rather than the label sent.
func recordMatchesUpdate(r *Record, zone *HostedZone, in *UpdateRecordInput) bool {
	if in.SubDomain != nil && r.SubDomain != fullSubDomain(*in.SubDomain, zone.DomainName) {
		return false
	}
	if in.Type != nil && r.Type != *in.Type {
		return false
	}
	if in.TTL != nil && r.TTL != *in.TTL {
		return false
	}
	if in.RoutingPolicy != nil && r.RoutingPolicy != *in.RoutingPolicy {
		return false
	}
	if in.Values != nil && !recordValuesEqual(r.Value, *in.Values) {
		return false
	}
	if in.StickySession != nil && !ptrEqual(r.EnableStickySession, in.StickySession) {
		return false
	}
	return true
}

// fullSubDomain builds the full name the server returns for subDomain
// within a zone named domainName: domainName itself for the apex
// (subDomain ""), or "<subDomain>.<domainName>" otherwise.
func fullSubDomain(subDomain, domainName string) string {
	if subDomain == "" {
		return domainName
	}
	return subDomain + "." + domainName
}

// recordValuesEqual reports whether a and b hold the same values in the
// same order, comparing Location and Weight by their pointed-to value
// rather than by pointer identity.
func recordValuesEqual(a, b []RecordValue) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Value != b[i].Value {
			return false
		}
		if !ptrEqual(a[i].Location, b[i].Location) {
			return false
		}
		if !ptrEqual(a[i].Weight, b[i].Weight) {
			return false
		}
	}
	return true
}

// ptrEqual reports whether a and b are both nil or both point to equal
// values.
func ptrEqual[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// DeleteRecordInput identifies the record to delete.
type DeleteRecordInput struct {
	HostedZoneID string `vngcloud:"required"`
	RecordID     string `vngcloud:"required"`
	NoWait       bool
}

type DeleteRecordOutput struct{}

// DeleteRecord deletes a record. The server refuses to delete its own NS
// and SOA records with a 400; the SDK does not filter them out first.
//
// It first runs the same pre-write wait CreateRecord does, for the same
// reason: nothing has been sent yet, and a busy zone past the wait's bound
// returns ErrZoneBusy with nothing sent.
//
// Without NoWait, it then waits for a read of the record to fail with
// NotFound, or returns an error wrapping ErrNotSettled if that wait's own
// bound runs out or a read or a sleep in it fails, such as from a canceled
// ctx. With NoWait, it returns at once after the delete request succeeds.
// DELETE is idempotent and keeps the transport's own retries; a retry that
// finds the record already gone returns NotFound, which is not an error
// DeleteRecord itself needs to handle specially. The Output is always
// non-nil; DeleteRecordOutput carries no field, so there is nothing else
// for a caller to fall back to.
func (c *Client) DeleteRecord(ctx context.Context, in *DeleteRecordInput) (*DeleteRecordOutput, error) {
	const op = "dns.DeleteRecord"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "HostedZoneID", in.HostedZoneID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "RecordID", in.RecordID); err != nil {
		return nil, err
	}

	if err := c.lockWrite(ctx); err != nil {
		return nil, err
	}
	defer c.unlockWrite()

	if _, err := c.waitZoneReady(ctx, op, in.HostedZoneID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.url([]string{"dns", "hosted-zone", in.HostedZoneID, "record", in.RecordID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	if in.NoWait {
		return &DeleteRecordOutput{}, nil
	}
	if err := c.waitRecordGone(ctx, op, in.HostedZoneID, in.RecordID); err != nil {
		return &DeleteRecordOutput{}, err
	}
	return &DeleteRecordOutput{}, nil
}
