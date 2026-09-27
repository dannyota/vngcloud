// Package tagging reads and writes GreenNode resource tags. One PUT
// endpoint on the vServer gateway serves every resource type: TagResource
// reads a resource's whole tag list, applies one key's change, sends the
// whole list back, and confirms the result with another read, so it never
// drops a tag the caller did not name.
//
// TagResource refuses to write to a resource that already carries any
// system tag, with ErrSystemTag, until a live check shows what the tag PUT
// does to one. ResourceType is sent to the server exactly as given; the
// package exports no resource type constant yet, since none has been
// confirmed live to accept a tag write for free.
package tagging

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

var (
	// ErrSystemTag means a tag write was refused because the resource
	// already carries a system tag, found by the pre-write read every write
	// makes. Nothing was sent: whether the tag PUT resends a system tag
	// unchanged or drops it is not yet confirmed live.
	ErrSystemTag = errors.New("tagging: resource has a system tag")

	// ErrNotSettled means a tag write's PUT was sent, and may have reached
	// the server, but the confirming read did not come back matching it:
	// either that read itself failed, or another writer changed the tags
	// in between. The returned Output still holds the last tags a read
	// returned, so the caller reads the tags again before writing once
	// more, rather than repeating this same call blind.
	ErrNotSettled = errors.New("tagging: write accepted but not settled")
)

// Tag is one key/value pair on a resource. SystemTag is true for a tag the
// platform manages.
type Tag struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	SystemTag bool   `json:"systemTag"`
	CreatedAt string `json:"createdAt"`
}

// Client is the tagging service client. It uses the vServer gateway, the
// same one network.Client uses.
type Client struct {
	c *core.Client
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

// ListResourceTagsInput reads every tag on a resource, whatever its type.
type ListResourceTagsInput struct {
	ResourceID string `vngcloud:"required"`
}

type ListResourceTagsOutput struct {
	Items []Tag
}

// ListResourceTags reads every tag on ResourceID. The read accepts any
// resource id; it does not itself confirm that ResourceID is a type the
// tag write also accepts.
func (c *Client) ListResourceTags(ctx context.Context, in *ListResourceTagsInput) (*ListResourceTagsOutput, error) {
	const op = "tagging.ListResourceTags"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ResourceID", in.ResourceID); err != nil {
		return nil, err
	}
	tags, err := c.listTags(ctx, op, in.ResourceID)
	if err != nil {
		return nil, err
	}
	return &ListResourceTagsOutput{Items: tags}, nil
}

// TagResourceInput sets Key to Value on ResourceID, leaving every other tag
// on the resource unchanged. ResourceType is sent exactly as given; no
// constant is exported yet for it, since none has been confirmed live to
// accept a tag write for free.
type TagResourceInput struct {
	ResourceID   string `vngcloud:"required"`
	ResourceType string `vngcloud:"required"`
	Key          string `vngcloud:"required"`

	Value string
}

// TagResourceOutput is the resource's tags after the write. Previous is
// Key's value before the write, or nil when the resource had no such tag,
// so a caller can undo the write by setting Key back to *Previous, or by
// removing it if Previous is nil.
type TagResourceOutput struct {
	Tags     []Tag
	Previous *string
	Changed  bool
}

// TagResource sets Key to Value on ResourceID.
//
// It first reads every tag on the resource. If any of them is a system tag,
// it returns ErrSystemTag and sends nothing. Otherwise, when Key is already
// set to Value, it returns at once with Changed false and sends nothing.
//
// Otherwise it sends every tag read, with Key's value replaced or added, in
// one PUT: PUT is idempotent, so the transport's normal retries apply. It
// then reads the tags again to confirm they equal what was sent. A
// mismatch, or a failure of that confirming read, returns an error wrapping
// ErrNotSettled, and the Output falls back to the list TagResource intended
// to write, or, on a mismatch, the tags the confirming read actually found.
func (c *Client) TagResource(ctx context.Context, in *TagResourceInput) (*TagResourceOutput, error) {
	const op = "tagging.TagResource"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ResourceID", in.ResourceID); err != nil {
		return nil, err
	}

	current, err := c.listTags(ctx, op, in.ResourceID)
	if err != nil {
		return nil, err
	}
	if sys, ok := firstSystemTag(current); ok {
		return nil, fmt.Errorf("%w: %s: resource %s has system tag %q", ErrSystemTag, op, in.ResourceID, sys.Key)
	}

	next, previous, changed := applyTag(current, in.Key, in.Value)
	if !changed {
		return &TagResourceOutput{Tags: current, Previous: previous, Changed: false}, nil
	}
	return c.writeTags(ctx, op, in.ResourceID, in.ResourceType, next, previous)
}

// untagResourceInput removes Key from ResourceID's tags. It stays
// unexported until a live check confirms that the tag PUT drops a key left
// off the list, rather than leaving it in place under upsert semantics; see
// the package doc.
type untagResourceInput struct {
	ResourceID   string `vngcloud:"required"`
	ResourceType string `vngcloud:"required"`
	Key          string `vngcloud:"required"`
}

type untagResourceOutput struct {
	Tags     []Tag
	Previous *string
	Changed  bool
}

// untagResource removes Key from ResourceID's tags, following the same
// read-refuse-send-confirm shape as TagResource: it refuses a resource that
// carries any system tag with ErrSystemTag, returns at once with Changed
// false when Key is already absent, and otherwise sends every other tag
// read and confirms the result with another read, wrapping ErrNotSettled on
// a mismatch or a failed confirm.
func (c *Client) untagResource(ctx context.Context, in *untagResourceInput) (*untagResourceOutput, error) {
	const op = "tagging.UntagResource"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ResourceID", in.ResourceID); err != nil {
		return nil, err
	}

	current, err := c.listTags(ctx, op, in.ResourceID)
	if err != nil {
		return nil, err
	}
	if sys, ok := firstSystemTag(current); ok {
		return nil, fmt.Errorf("%w: %s: resource %s has system tag %q", ErrSystemTag, op, in.ResourceID, sys.Key)
	}

	next, previous, changed := applyUntag(current, in.Key)
	if !changed {
		return &untagResourceOutput{Tags: current, Previous: previous, Changed: false}, nil
	}
	out, err := c.writeTags(ctx, op, in.ResourceID, in.ResourceType, next, previous)
	if out == nil {
		return nil, err
	}
	return &untagResourceOutput{Tags: out.Tags, Previous: out.Previous, Changed: out.Changed}, err
}

// applyTag returns the tag list a write for key and value should send
// against current, the value key held before the write (nil when key was
// absent), and whether anything changes. It copies current rather than
// mutating it, and never drops an entry current holds for a key other than
// key.
func applyTag(current []Tag, key, value string) (next []Tag, previous *string, changed bool) {
	for i, tag := range current {
		if tag.Key != key {
			continue
		}
		v := tag.Value
		if tag.Value == value {
			return current, &v, false
		}
		next = make([]Tag, len(current))
		copy(next, current)
		next[i].Value = value
		return next, &v, true
	}
	next = make([]Tag, len(current), len(current)+1)
	copy(next, current)
	next = append(next, Tag{Key: key, Value: value})
	return next, nil, true
}

// applyUntag returns the tag list a removal of key should send against
// current, the value key held before the removal (nil when key was
// already absent), and whether anything changes. It never drops an entry
// current holds for a key other than key.
func applyUntag(current []Tag, key string) (next []Tag, previous *string, changed bool) {
	next = make([]Tag, 0, len(current))
	for _, tag := range current {
		if tag.Key == key {
			v := tag.Value
			previous = &v
			changed = true
			continue
		}
		next = append(next, tag)
	}
	if !changed {
		return current, nil, false
	}
	return next, previous, true
}

func firstSystemTag(tags []Tag) (Tag, bool) {
	for _, tag := range tags {
		if tag.SystemTag {
			return tag, true
		}
	}
	return Tag{}, false
}

// tagsEqual reports whether a and b name the same set of key/value pairs,
// regardless of order. It compares Key and Value only, since the list a
// write sends never carries SystemTag or CreatedAt.
func tagsEqual(a, b []Tag) bool {
	if len(a) != len(b) {
		return false
	}
	values := make(map[string]string, len(a))
	for _, tag := range a {
		values[tag.Key] = tag.Value
	}
	if len(values) != len(a) {
		// A duplicate key in a means a can never equal a set of distinct
		// keys; treating it as a mismatch is the safe default.
		return false
	}
	for _, tag := range b {
		v, ok := values[tag.Key]
		if !ok || v != tag.Value {
			return false
		}
	}
	return true
}

// tagWriteBody is the tag PUT's request body. The API also accepts tags and
// zoneId at the top level, and an optional isEdited boolean on each tag
// request; the SDK sends none of them.
type tagWriteBody struct {
	ResourceID     string       `json:"resourceId"`
	ResourceType   string       `json:"resourceType"`
	TagRequestList []tagRequest `json:"tagRequestList"`
}

type tagRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// listTags reads ResourceID's tags. The response is a bare JSON array, not
// wrapped in a "data" field.
func (c *Client) listTags(ctx context.Context, op, resourceID string) ([]Tag, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var tags []Tag
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.tagURL(projectID, resourceID),
		OK:        []int{200},
	}, &tags); err != nil {
		return nil, err
	}
	if tags == nil {
		tags = []Tag{}
	}
	return tags, nil
}

// writeTags sends next as ResourceID's whole tag list and confirms the
// result by reading the tags again. previous is threaded through to the
// returned Output unchanged; it plays no part in the write itself.
func (c *Client) writeTags(ctx context.Context, op, resourceID, resourceType string, next []Tag, previous *string) (*TagResourceOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	list := make([]tagRequest, len(next))
	for i, tag := range next {
		list[i] = tagRequest{Key: tag.Key, Value: tag.Value}
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.tagURL(projectID, resourceID),
		Body:      tagWriteBody{ResourceID: resourceID, ResourceType: resourceType, TagRequestList: list},
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	// The PUT above already succeeded, so from here on every error means
	// the write may have landed and must not be sent again.
	confirmed, err := c.listTags(ctx, op, resourceID)
	if err != nil {
		return &TagResourceOutput{Tags: next, Previous: previous, Changed: true},
			fmt.Errorf("%w: %s: resource %s: confirm read failed: %w", ErrNotSettled, op, resourceID, err)
	}
	if !tagsEqual(confirmed, next) {
		return &TagResourceOutput{Tags: confirmed, Previous: previous, Changed: true},
			fmt.Errorf("%w: %s: resource %s: another writer may have changed the tags", ErrNotSettled, op, resourceID)
	}
	return &TagResourceOutput{Tags: confirmed, Previous: previous, Changed: true}, nil
}

func (c *Client) tagURL(projectID, resourceID string) string {
	return c.c.RouteURL(routes.Route{
		Product: routes.ProductVServer,
		Version: "v2",
		Parts:   []string{projectID, "tag", "resource", resourceID},
	})
}
