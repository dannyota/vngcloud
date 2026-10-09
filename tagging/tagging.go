// Package tagging reads and writes GreenNode resource tags. One PUT
// endpoint on the vServer gateway serves every resource type: TagResource
// and UntagResource each read a resource's whole tag list, apply one key's
// change to the user tags, send the user tags back, and confirm the result
// with another read, so neither call ever drops a tag the caller did not
// name.
//
// Every resource carries system tags the platform manages (vng.zone,
// vng.region, vng.createdBy, confirmed live on a virtual IP address): the
// tag PUT replaces only the user tag list, so it never sends and never
// touches a system tag. TagResource and UntagResource refuse with
// ErrSystemTag, and send nothing, when Key names one: either it starts
// with the "vng." prefix those system tags use, or the pre-write read
// finds an existing system tag under that exact Key.
//
// ResourceType is sent to the server exactly as given. ResourceTypeVirtualIPAddress
// is confirmed live to accept a tag write for free. VNG Cloud's own Go SDK
// also names SERVER, VOLUME, and LOAD-BALANCER for this call, on paid
// resources this package has not tried.
package tagging

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

var (
	// ErrSystemTag means a tag write was refused because Key names a
	// system tag, either by its "vng." prefix or by matching an existing
	// system tag the pre-write read found. Nothing was sent.
	ErrSystemTag = errors.New("tagging: key is a system tag")

	// ErrNotSettled means a tag write's PUT was sent, and may have reached
	// the server, but the confirming read did not come back matching it:
	// either that read itself failed, or another writer changed the tags
	// in between. The returned Output still holds the last tags a read
	// returned, so the caller reads the tags again before writing once
	// more, rather than repeating this same call blind.
	ErrNotSettled = errors.New("tagging: write accepted but not settled")
)

// ResourceTypeVirtualIPAddress is the recorded accepted resource type for a
// private virtual IP address.
const ResourceTypeVirtualIPAddress = "VIRTUAL-IP-ADDRESS"

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

// systemTagPrefix is the key prefix GreenNode's own system tags use
// (vng.zone, vng.region, vng.createdBy, confirmed live). TagResource and
// UntagResource refuse any Key with this prefix before they even read the
// resource's tags, since the platform reserves it.
const systemTagPrefix = "vng."

// TagResourceInput sets Key to Value on ResourceID, leaving every other tag
// on the resource unchanged. ResourceType is sent exactly as given; see the
// package doc for the resource types confirmed or named for it.
type TagResourceInput struct {
	ResourceID   string `vngcloud:"required"`
	ResourceType string `vngcloud:"required"`
	Key          string `vngcloud:"required"`

	Value string
}

// TagResourceOutput is the resource's whole tag list, system tags included,
// after the write. Previous is Key's value before the write, or nil when
// the resource had no such tag, so a caller can undo the write by setting
// Key back to *Previous, or by removing it if Previous is nil.
type TagResourceOutput struct {
	Tags     []Tag
	Previous *string
	Changed  bool
}

// TagResource sets Key to Value on ResourceID.
//
// It refuses with ErrSystemTag, sending nothing, when Key starts with
// "vng." or names an existing system tag. Otherwise it reads every tag on
// the resource; when Key is already set to Value among the user tags, it
// returns at once with Changed false and sends nothing.
//
// Otherwise it sends every user tag read, with Key's value replaced or
// added, in one PUT: system tags are never included, since the PUT
// replaces only the user tag list and leaves system tags untouched. PUT is
// idempotent, so the transport's normal retries apply. TagResource then
// reads the tags again to confirm the user tags equal what was sent. A
// mismatch, or a failure of that confirming read, returns an error wrapping
// ErrNotSettled. The Output holds the confirming read's tags on a mismatch,
// or the last tags read before the write when that read fails.
func (c *Client) TagResource(ctx context.Context, in *TagResourceInput) (*TagResourceOutput, error) {
	const op = "tagging.TagResource"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ResourceID", in.ResourceID); err != nil {
		return nil, err
	}
	if strings.HasPrefix(in.Key, systemTagPrefix) {
		return nil, fmt.Errorf("%w: %s: key %q", ErrSystemTag, op, in.Key)
	}

	current, err := c.listTags(ctx, op, in.ResourceID)
	if err != nil {
		return nil, err
	}
	system, user := splitTags(current)
	if systemTagKey(system, in.Key) {
		return nil, fmt.Errorf("%w: %s: resource %s: key %q", ErrSystemTag, op, in.ResourceID, in.Key)
	}

	next, previous, changed := applyTag(user, in.Key, in.Value)
	if !changed {
		return &TagResourceOutput{Tags: current, Previous: previous, Changed: false}, nil
	}
	return c.writeTags(ctx, op, in.ResourceID, in.ResourceType, current, next, previous)
}

// UntagResourceInput removes Key from ResourceID's tags.
type UntagResourceInput struct {
	ResourceID   string `vngcloud:"required"`
	ResourceType string `vngcloud:"required"`
	Key          string `vngcloud:"required"`
}

// UntagResourceOutput is the resource's whole tag list, system tags
// included, after the write. Previous is Key's value before the write, or
// nil when the resource had no such tag.
type UntagResourceOutput struct {
	Tags     []Tag
	Previous *string
	Changed  bool
}

// UntagResource removes Key from ResourceID's tags, following the same
// refuse-read-send-confirm shape as TagResource: it refuses with
// ErrSystemTag, sending nothing, when Key starts with "vng." or names an
// existing system tag; it returns at once with Changed false when Key is
// already absent from the user tags; and otherwise it sends every other
// user tag read and confirms the result with another read, wrapping
// ErrNotSettled on a mismatch or a failed confirm. The confirmed PUT
// replaces only the user tag list, so a resource's system tags are never
// sent and never touched.
func (c *Client) UntagResource(ctx context.Context, in *UntagResourceInput) (*UntagResourceOutput, error) {
	const op = "tagging.UntagResource"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ResourceID", in.ResourceID); err != nil {
		return nil, err
	}
	if strings.HasPrefix(in.Key, systemTagPrefix) {
		return nil, fmt.Errorf("%w: %s: key %q", ErrSystemTag, op, in.Key)
	}

	current, err := c.listTags(ctx, op, in.ResourceID)
	if err != nil {
		return nil, err
	}
	system, user := splitTags(current)
	if systemTagKey(system, in.Key) {
		return nil, fmt.Errorf("%w: %s: resource %s: key %q", ErrSystemTag, op, in.ResourceID, in.Key)
	}

	next, previous, changed := applyUntag(user, in.Key)
	if !changed {
		return &UntagResourceOutput{Tags: current, Previous: previous, Changed: false}, nil
	}
	out, err := c.writeTags(ctx, op, in.ResourceID, in.ResourceType, current, next, previous)
	if out == nil {
		return nil, err
	}
	return &UntagResourceOutput{Tags: out.Tags, Previous: out.Previous, Changed: out.Changed}, err
}

// splitTags separates tags into its system and user tags, each in the
// order tags held them.
func splitTags(tags []Tag) (system, user []Tag) {
	for _, tag := range tags {
		if tag.SystemTag {
			system = append(system, tag)
		} else {
			user = append(user, tag)
		}
	}
	return system, user
}

// systemTagKey reports whether key matches one of system's keys. Callers
// check the "vng." prefix separately, before ever reading a resource's
// tags, so this only needs to catch a system tag under a key that prefix
// would miss.
func systemTagKey(system []Tag, key string) bool {
	for _, tag := range system {
		if tag.Key == key {
			return true
		}
	}
	return false
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
		delete(values, tag.Key)
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

// writeTags sends next as ResourceID's whole user tag list, never system,
// and confirms the result by reading the tags again. previous is threaded
// through to the returned Output unchanged; it plays no part in the write
// itself. current is returned if the confirming read fails, because it is
// the last tag list the SDK observed.
func (c *Client) writeTags(ctx context.Context, op, resourceID, resourceType string, current, next []Tag, previous *string) (*TagResourceOutput, error) {
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
		return &TagResourceOutput{Tags: current, Previous: previous, Changed: true},
			fmt.Errorf("%w: %s: resource %s: confirm read failed: %w", ErrNotSettled, op, resourceID, err)
	}
	_, confirmedUser := splitTags(confirmed)
	if !tagsEqual(confirmedUser, next) {
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
