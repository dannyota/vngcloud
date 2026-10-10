package cdn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"

	"danny.vn/vngcloud/internal/core"
)

// UpstreamInput is one origin in an UpdateWebAccelerator Input. With ID set
// it edits the origin that has that ID, replacing all its fields; without
// one it adds an origin. UpstreamType is httpOrigin when empty. OriginValue
// is nil for an HTTP origin.
type UpstreamInput struct {
	ID           string
	Priority     int
	IPAddress    string `vngcloud:"required"`
	UpstreamType string
	OriginValue  *string
	UseSSL       bool
}

// RuleActionInput names a default rule action and its value. Value is the
// server's string; for hsts and minify it is JSON text, as in RuleAction.
type RuleActionInput struct {
	Name  string `vngcloud:"required"`
	Value string
}

// UpdateWebAcceleratorInput holds only the changes. UpdateWebAccelerator
// reads the CDN and merges them in, so a caller never handles an action ID.
// At least one field besides CDNID and NoWait must be set.
//
// SetRuleActions changes the value of the action with that name, or adds
// the action; RemoveRuleActions drops actions by name. A name may appear
// once across both lists. Upstreams sends only the origins given: no call
// removes an origin, so one left out stays, and an origin changes by
// editing it in place with its ID. Scalar fields keep the CDN's value when
// nil. CNames and FailOverErrorCodes keep it when nil and replace it when
// set, so an empty non-nil list clears it.
//
// NoWait returns after one read of the CDN instead of waiting for it to
// settle.
type UpdateWebAcceleratorInput struct {
	CDNID              string `vngcloud:"required"`
	SetRuleActions     []RuleActionInput
	RemoveRuleActions  []string
	Upstreams          []UpstreamInput
	LBType             *string
	FailOverErrorCodes []string
	CertificateID      *string
	OriginHostHeader   *string
	CNames             []string
	NoWait             bool
}

type UpdateWebAcceleratorOutput struct {
	WebAccelerator WebAccelerator
}

func (in *UpdateWebAcceleratorInput) empty() bool {
	return len(in.SetRuleActions) == 0 && len(in.RemoveRuleActions) == 0 && len(in.Upstreams) == 0 &&
		in.LBType == nil && in.FailOverErrorCodes == nil && in.CertificateID == nil &&
		in.OriginHostHeader == nil && in.CNames == nil
}

// check refuses an Input that cannot be merged, before any request.
func (in *UpdateWebAcceleratorInput) check(op string) error {
	if in.empty() {
		return fmt.Errorf("%w: %s requires at least one change besides CDNID and NoWait", core.ErrInvalidInput, op)
	}
	seen := map[string]bool{}
	names := make([]string, 0, len(in.SetRuleActions)+len(in.RemoveRuleActions))
	for _, a := range in.SetRuleActions {
		names = append(names, a.Name)
	}
	names = append(names, in.RemoveRuleActions...)
	for _, name := range names {
		if name == "" {
			return fmt.Errorf("%w: %s requires every rule action name to be non-empty", core.ErrInvalidInput, op)
		}
		if seen[name] {
			return fmt.Errorf("%w: %s names the rule action %q more than once", core.ErrInvalidInput, op, limitMessage(name))
		}
		seen[name] = true
	}
	for _, u := range in.Upstreams {
		if u.IPAddress == "" {
			return fmt.Errorf("%w: %s requires every upstream to have an IPAddress", core.ErrInvalidInput, op)
		}
		if u.ID != "" {
			if err := core.CheckPathID(op, "Upstreams.ID", u.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// UpdateWebAccelerator changes a CDN. The server deletes every rule action
// the update body leaves out, so the call reads the CDN, merges the changes
// by action name into the raw object, and sends the whole of it back, with
// every field the SDK does not model unchanged. The CDN must be ACTIVE: a
// DISABLED CDN gives ErrInvalidInput, and a DEPLOYING, DELETING, or DISABLING
// CDN gives ErrBusy, both with nothing sent. When the merge changes nothing,
// nothing is sent and the Output is the read.
//
// The update is sent once. After a server error or a network failure it may
// have been applied: read the CDN before running it again. Unless NoWait is
// set, the call waits up to six minutes for ACTIVE; on ErrNotSettled the
// Output is the last good read and the update must not be repeated.
func (c *Client) UpdateWebAccelerator(ctx context.Context, in *UpdateWebAcceleratorInput) (*UpdateWebAcceleratorOutput, error) {
	const op = "cdn.UpdateWebAccelerator"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CDNID", in.CDNID); err != nil {
		return nil, err
	}
	if err := in.check(op); err != nil {
		return nil, err
	}
	raw, wa, err := c.readCDN(ctx, op, in.CDNID)
	if err != nil {
		return nil, err
	}
	if _, err := guard(op, kindUpdate, wa.Status); err != nil {
		return nil, err
	}
	merged, changed, err := mergeUpdate(op, raw, in)
	if err != nil {
		return nil, err
	}
	if !changed {
		return &UpdateWebAcceleratorOutput{WebAccelerator: *wa}, nil
	}
	r := call{op: op, method: http.MethodPut, parts: []string{"cdn", "update"}, body: merged, once: true, redactValues: userUUID(raw)}
	if _, err := c.do(ctx, r); err != nil {
		return nil, c.explainWrite401(ctx, op, in.CDNID, maybeLanded(err, "update"))
	}
	deadline := c.clock().Add(settleBound)
	var out *WebAccelerator
	if in.NoWait {
		out, err = c.detailByDeadline(ctx, op, in.CDNID, deadline)
		if err != nil {
			return &UpdateWebAcceleratorOutput{WebAccelerator: *wa}, notSettled(op, wa, err)
		}
	} else {
		out, err = c.settle(ctx, op, in.CDNID, settleActive, deadline, nil, wa)
	}
	if out == nil {
		return &UpdateWebAcceleratorOutput{WebAccelerator: *wa}, err
	}
	return &UpdateWebAcceleratorOutput{WebAccelerator: *out}, err
}

func userUUID(raw json.RawMessage) []string {
	var value struct {
		UserUUID string `json:"userUuid"`
	}
	if json.Unmarshal(raw, &value) != nil || value.UserUUID == "" {
		return nil
	}
	return []string{value.UserUUID}
}

// mergeUpdate applies in to the raw CDN object. It reports whether the
// result differs from raw.
func mergeUpdate(op string, raw json.RawMessage, in *UpdateWebAcceleratorInput) (json.RawMessage, bool, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false, fmt.Errorf("%w: %s: the CDN read is not a JSON object", core.ErrInvalidInput, op)
	}
	if len(in.SetRuleActions) > 0 || len(in.RemoveRuleActions) > 0 {
		actions, err := mergeActions(op, obj["defaultRuleAction"], in)
		if err != nil {
			return nil, false, err
		}
		obj["defaultRuleAction"] = actions
	}
	if len(in.Upstreams) > 0 {
		ups, err := mergeUpstreams(op, obj["upstreams"], in.Upstreams)
		if err != nil {
			return nil, false, err
		}
		obj["upstreams"] = ups
	}
	for key, val := range map[string]*string{
		"lbType": in.LBType, "sslId": in.CertificateID, "originHostHeader": in.OriginHostHeader,
	} {
		if val != nil {
			obj[key] = mustMarshal(*val)
		}
	}
	if in.FailOverErrorCodes != nil {
		obj["failOverErrorCode"] = mustMarshal(in.FailOverErrorCodes)
	}
	if in.CNames != nil {
		obj["cName"] = mustMarshal(in.CNames)
	}
	body := mustMarshal(obj)
	var before, after any
	if json.Unmarshal(raw, &before) != nil || json.Unmarshal(body, &after) != nil {
		return nil, false, fmt.Errorf("%w: %s: the merged CDN is not valid JSON", core.ErrInvalidInput, op)
	}
	return body, !reflect.DeepEqual(before, after), nil
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		// Only strings, string lists, and raw JSON from the server reach
		// here, none of which can fail to encode.
		panic(err)
	}
	return b
}

// mergeActions changes, adds, and drops rule actions by name, keeping the
// id and order of every action it keeps.
func mergeActions(op string, current json.RawMessage, in *UpdateWebAcceleratorInput) (json.RawMessage, error) {
	var actions []map[string]json.RawMessage
	if !emptyData(current) {
		if err := json.Unmarshal(current, &actions); err != nil {
			return nil, fmt.Errorf("%w: %s: the CDN's rule actions are not a list", core.ErrInvalidInput, op)
		}
	}
	nameOf := func(a map[string]json.RawMessage) string {
		var name string
		_ = json.Unmarshal(a["actionName"], &name)
		return name
	}
	remove := map[string]bool{}
	for _, name := range in.RemoveRuleActions {
		remove[name] = true
	}
	for _, set := range in.SetRuleActions {
		found := false
		for _, a := range actions {
			if nameOf(a) == set.Name {
				a["value"] = mustMarshal(set.Value)
				found = true
			}
		}
		if !found {
			actions = append(actions, map[string]json.RawMessage{
				"actionName": mustMarshal(set.Name),
				"value":      mustMarshal(set.Value),
				"order":      mustMarshal(0),
			})
		}
	}
	kept := make([]map[string]json.RawMessage, 0, len(actions))
	for _, a := range actions {
		if !remove[nameOf(a)] {
			kept = append(kept, a)
		}
	}
	return mustMarshal(kept), nil
}

// mergeUpstreams builds the upstreams to send: only the given entries. An
// entry with an ID keeps the ID in the JSON form the read used.
func mergeUpstreams(op string, current json.RawMessage, given []UpstreamInput) (json.RawMessage, error) {
	var existing []map[string]json.RawMessage
	if !emptyData(current) {
		if err := json.Unmarshal(current, &existing); err != nil {
			return nil, fmt.Errorf("%w: %s: the CDN's upstreams are not a list", core.ErrInvalidInput, op)
		}
	}
	out := make([]map[string]json.RawMessage, 0, len(given))
	for _, u := range given {
		typ := u.UpstreamType
		if typ == "" {
			typ = "httpOrigin"
		}
		entry := map[string]json.RawMessage{
			"priority":     mustMarshal(u.Priority),
			"ipaddress":    mustMarshal(u.IPAddress),
			"upstreamType": mustMarshal(typ),
			"originValue":  json.RawMessage("null"),
			"useSsl":       mustMarshal(u.UseSSL),
		}
		if u.OriginValue != nil {
			entry["originValue"] = mustMarshal(*u.OriginValue)
		}
		if u.ID != "" {
			rawID, ok := findUpstreamID(existing, u.ID)
			if !ok {
				return nil, fmt.Errorf("%w: %s: the CDN has no upstream with that ID", core.ErrInvalidInput, op)
			}
			entry["cdnUpstreamId"] = rawID
		}
		out = append(out, entry)
	}
	return mustMarshal(out), nil
}

func findUpstreamID(existing []map[string]json.RawMessage, id string) (json.RawMessage, bool) {
	for _, e := range existing {
		var got flexID
		if json.Unmarshal(e["cdnUpstreamId"], &got) == nil && string(got) == id {
			return e["cdnUpstreamId"], true
		}
	}
	return nil, false
}
