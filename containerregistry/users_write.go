package containerregistry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ErrUserNotFound means CreateUser's own lookup, after a successful create,
// found zero or more than one row matching the input name: the Output still
// holds SecretKey, since the create itself succeeded, but User is left
// unfilled. list-users --name <name> finds the row by hand.
var ErrUserNotFound = errors.New("containerregistry: created user not found")

// UserPermission grants Actions on one repository to a user CreateUser
// makes. Each action name is resolved against ListPermissions, matching the
// server's own action string exactly; the server's list is the authority,
// not a value this SDK checks offline.
type UserPermission struct {
	RepositoryID string
	Actions      []string
}

// createUserBody is CreateUser's request body. Duration is a pointer so a
// nil DurationDays omits the key entirely, which the console calls "no
// expiration".
type createUserBody struct {
	Name                  string                  `json:"name"`
	Description           string                  `json:"description,omitempty"`
	Duration              *int                    `json:"duration,omitempty"`
	PermissionRequestList []permissionRequestBody `json:"permissionRequestList"`
}

type permissionRequestBody struct {
	RepoID       string   `json:"repoId"`
	PolicyIDList []string `json:"policyIdList"`
}

// createUserResponse is CreateUser's success body: the reference documents
// only this one field, with no user id.
type createUserResponse struct {
	SecretKey string `json:"secretKey"`
}

// CreateUserInput creates a repository user (a "robot account"). Name is
// required, Permissions must hold at least one entry, each with a
// path-safe RepositoryID and at least one action, and DurationDays, when
// set, must be at least 1. All of this is checked, and nothing sent, before
// any request.
type CreateUserInput struct {
	Name         string `vngcloud:"required"`
	Description  string
	DurationDays *int
	Permissions  []UserPermission `vngcloud:"required"`
}

// CreateUserOutput's SecretKey is a vngcloud.Secret, never a plain string:
// printing, logging, or JSON-encoding the Output gives "[redacted]" for
// this field, and Reveal is the only way to read the value back out. User
// itself never carries a secret. SecretKey is filled whenever the create
// itself succeeded, even when User could not be resolved by the lookup: see
// ErrUserNotFound.
type CreateUserOutput struct {
	User      User
	SecretKey vngcloud.Secret
}

// CreateUser creates a repository user.
//
// It checks Input shape, entirely offline; reads ListPermissions and maps
// each requested action to its policy id, since the server's own list is
// the authority for action names (an unknown action is core.ErrInvalidInput
// naming the ones ListPermissions did return, and nothing is sent); then,
// before sending anything, lists users by Name and refuses with
// core.ErrInvalidInput, naming list-users --name <name>, if a row already
// matches the name exactly. That check walks every page and fails closed,
// returning an error rather than a guess, when the listing cannot account
// for every row (see listUsersExactMatch); either way this is the same
// failure mode as any other pre-create validation, nothing sent, so it
// costs nothing to check first. Only then does it send the create itself,
// with transport.Request.Sensitive set, so the response never reaches the
// configured response-capture hook and a decode failure never quotes it.
//
// The response carries only a secret key, no user id, so CreateUser finds
// the new user with the same exact-name lookup: a live capture confirms
// the server applies no account prefix to a user's name. Exactly one match
// fills User; zero or more than one, or the lookup itself failing closed,
// leaves User unfilled and returns an error wrapping ErrUserNotFound,
// naming list-users --name <name> to check by hand. Either way SecretKey
// is already set on the returned Output, since the create itself succeeded
// and the secret is shown once: a lookup failure never drops it.
//
// The create is a POST, sent with Once (transport.Request.Once, ADR 0003
// rule 3): a followed redirect would resend the same create at the
// Location the server names, creating a second user with a second secret,
// so Once refuses to follow one. A 401 is not the same kind of risk: the
// API gateway rejects a stale or invalid token before the request ever
// reaches the create logic, so it creates nothing, and
// isClientRejectionError treats it as an outright rejection like any other
// 4xx (see isClientRejectionError). After any other error
// isClientRejectionError does not accept as an outright rejection, such as
// a 5xx or a network failure, the user may exist, and the caller runs
// list-users --name <name> and deletes a match, since it has already lost
// its secret, before creating again rather than retrying blind. A 200
// response with an empty secretKey is treated the same way, through a
// *core.APIError naming the same check.
func (c *Client) CreateUser(ctx context.Context, in *CreateUserInput) (*CreateUserOutput, error) {
	const op = "containerregistry.CreateUser"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if len(in.Permissions) == 0 {
		return nil, fmt.Errorf("%w: %s requires at least one permission", core.ErrInvalidInput, op)
	}
	for i, p := range in.Permissions {
		if err := core.CheckPathID(op, fmt.Sprintf("Permissions[%d].RepositoryID", i), p.RepositoryID); err != nil {
			return nil, err
		}
		if len(p.Actions) == 0 {
			return nil, fmt.Errorf("%w: %s requires Permissions[%d] to have at least one action", core.ErrInvalidInput, op, i)
		}
	}
	if in.DurationDays != nil && *in.DurationDays < 1 {
		return nil, fmt.Errorf("%w: %s requires DurationDays to be at least 1, got %d", core.ErrInvalidInput, op, *in.DurationDays)
	}

	perms, err := c.ListPermissions(ctx, nil)
	if err != nil {
		return nil, err
	}
	actionToPolicyID := make(map[string]string, len(perms.Items))
	for _, p := range perms.Items {
		actionToPolicyID[p.Action] = p.ID
	}

	body := createUserBody{Name: in.Name, Description: in.Description, Duration: in.DurationDays}
	for i, p := range in.Permissions {
		policyIDs := make([]string, 0, len(p.Actions))
		for _, action := range p.Actions {
			id, ok := actionToPolicyID[action]
			if !ok {
				return nil, fmt.Errorf("%w: %s: Permissions[%d] names unknown action %q; known actions: %s",
					core.ErrInvalidInput, op, i, action, strings.Join(knownActions(perms.Items), ", "))
			}
			policyIDs = append(policyIDs, id)
		}
		body.PermissionRequestList = append(body.PermissionRequestList, permissionRequestBody{RepoID: p.RepositoryID, PolicyIDList: policyIDs})
	}

	existing, err := c.listUsersExactMatch(ctx, in.Name)
	if err != nil {
		return nil, fmt.Errorf("%s: could not confirm a user named %q does not already exist: %w", op, in.Name, err)
	}
	if len(existing) > 0 {
		return nil, fmt.Errorf("%w: %s: a user named %q already exists; check list-users --name %q",
			core.ErrInvalidInput, op, in.Name, in.Name)
	}

	var resp createUserResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.vcrURL([]string{"user"}, nil),
		Body:      body,
		OK:        []int{200},
		Sensitive: true,
		Once:      true,
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, wrapAmbiguousUserCreateErr(op, in.Name, err)
	}
	if resp.SecretKey == "" {
		return nil, &core.APIError{Operation: op, StatusCode: http.StatusOK,
			Message: fmt.Sprintf("create response had no secret key; a user named %q may exist, check with list-users --name %q", in.Name, in.Name)}
	}

	out := &CreateUserOutput{SecretKey: vngcloud.Secret(resp.SecretKey)}
	user, findErr := c.findCreatedUser(ctx, op, in.Name)
	if findErr != nil {
		return out, findErr
	}
	out.User = *user
	return out, nil
}

// knownActions returns the sorted, de-duplicated Action values in items, for
// CreateUser's unknown-action error message.
func knownActions(items []Permission) []string {
	seen := make(map[string]bool, len(items))
	var actions []string
	for _, p := range items {
		if !seen[p.Action] {
			seen[p.Action] = true
			actions = append(actions, p.Action)
		}
	}
	sort.Strings(actions)
	return actions
}

// wrapAmbiguousUserCreateErr wraps err from the create POST op just sent,
// unless err is a *core.APIError isClientRejectionError accepts: a
// rejection outright, so nothing was created and the exact same call is
// safe to retry. This includes a 401: the API gateway rejects a stale or
// invalid token before the request reaches the create logic, so a 401
// always creates nothing (see isClientRejectionError). Any other error,
// such as a 5xx, a network failure, or a redirect status the create's Once
// request refused to follow, leaves whether the user reached the server
// unknown, and a user found by list afterward has already lost its secret
// and should be deleted rather than kept.
func wrapAmbiguousUserCreateErr(op, name string, err error) error {
	if err == nil {
		return nil
	}
	if isClientRejectionError(err) {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; a created user has lost its secret, check with list-users --name %q and delete it: %w",
		op, name, err)
}

// maxUserListPages bounds the page walk in listUsersExactMatch: far more
// pages than a personal account's user list should ever need, so a server
// that never reaches its own reported last page is treated as an error
// rather than an unbounded loop.
const maxUserListPages = 100

// listUsersExactMatch walks every page of GET v1/user filtered by name,
// keeping only rows whose Name equals name exactly: the server's own name
// filter matches by substring, not exact name, so a caller after only the
// first page could miss a real match on a later one, or wrongly treat a
// substring match as the name being taken. It is the shared basis for
// CreateUser's own two exact-name lookups: the pre-create check that
// refuses a create when the name is already taken, and findCreatedUser's
// post-create lookup.
//
// It decodes each page itself, the same shape userFoundAfterServerError
// uses, rather than through ListUsers, so it can tell "the response
// carried no totals" apart from "the response said 0": TotalPage and
// TotalItem being pointers is what makes that possible. It fails closed,
// returning an error instead of a possibly-incomplete slice, whenever a
// page cannot be trusted to account for every row: TotalPage or TotalItem
// absent, the walk exceeding maxUserListPages before reaching the
// reported last page, or the items collected across every page not adding
// up to the server's own TotalItem. Both callers must refuse to guess
// rather than risk a false negative: a missed duplicate name, or a missed
// row that would have resolved the user CreateUser just made.
func (c *Client) listUsersExactMatch(ctx context.Context, name string) ([]User, error) {
	var matches []User
	collected := 0
	for page := 1; page <= maxUserListPages; page++ {
		q := url.Values{}
		q.Set("name", name)
		q.Set("page", strconv.Itoa(page))
		q.Set("size", strconv.Itoa(core.DefaultPageSize))

		var resp userListConfirmResponse
		if err := c.c.DoJSON(ctx, transport.Request{
			Operation: "containerregistry.listUsersExactMatch",
			Method:    http.MethodGet,
			URL:       c.vcrURL([]string{"user"}, q),
			OK:        []int{200},
		}, &resp); err != nil {
			return nil, err
		}

		items := resp.items()
		for _, u := range items {
			if u.Name == name {
				matches = append(matches, u)
			}
		}
		collected += len(items)

		if resp.TotalPage == nil || resp.TotalItem == nil {
			return nil, fmt.Errorf("containerregistry: user list for %q: response carried no totals; inconclusive", name)
		}
		if page >= *resp.TotalPage {
			if collected != *resp.TotalItem {
				return nil, fmt.Errorf("containerregistry: user list for %q: collected %d of %d item(s) across %d page(s); inconclusive",
					name, collected, *resp.TotalItem, *resp.TotalPage)
			}
			return matches, nil
		}
	}
	return nil, fmt.Errorf("containerregistry: user list for %q: exceeded %d pages without reaching the reported last page; inconclusive",
		name, maxUserListPages)
}

// findCreatedUser lists users by name and keeps rows whose Name equals name
// exactly, for CreateUser's post-create lookup: a live capture confirms the
// server applies no account prefix to a user's name, so an exact match is
// the only correct one. Exactly one match is returned; zero or more than
// one, or the list itself failing closed (see listUsersExactMatch), returns
// an error wrapping ErrUserNotFound, since the secret has already been
// issued either way and must not be treated as lost just because the
// lookup could not settle it.
func (c *Client) findCreatedUser(ctx context.Context, op, name string) (*User, error) {
	matches, err := c.listUsersExactMatch(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: created user %q could not be confirmed by list-users --name %q: %w", ErrUserNotFound, op, name, name, err)
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("%w: %s: %d user(s) named like %q found by list-users --name %q; check that list and delete an unwanted match",
			ErrUserNotFound, op, len(matches), name, name)
	}
	return &matches[0], nil
}

// DeleteUserInput identifies the user to delete: the robot account's own id
// (User.ID, the "uuid" field), not its UserID field.
type DeleteUserInput struct {
	UserID string `vngcloud:"required"`
}

type DeleteUserOutput struct{}

// DeleteUser deletes a repository user. A user holds no data of its own, so
// there is no pre-delete guard.
//
// How a missing user's read answers is unconfirmed: the reference documents
// only 500 besides 401 for this call, never 404. A plain 404 becomes
// core.ErrNotFound through the shared transport, with no extra call. After
// any other error with a 5xx status, DeleteUser calls ListUsers once and
// checks whether UserID is still listed: absent, it returns an error
// wrapping core.ErrNotFound; listed, or if the list call itself fails, it
// returns the original 5xx. This mirrors GetRepository's own list confirm.
func (c *Client) DeleteUser(ctx context.Context, in *DeleteUserInput) (*DeleteUserOutput, error) {
	const op = "containerregistry.DeleteUser"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "UserID", in.UserID); err != nil {
		return nil, err
	}

	delErr := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.vcrURL([]string{"user", in.UserID}, nil),
		OK:        []int{200},
	}, nil)
	if delErr == nil {
		return &DeleteUserOutput{}, nil
	}
	if !isVCRServerError(delErr) {
		return nil, delErr
	}
	found, listErr := c.userFoundAfterServerError(ctx, in.UserID)
	if listErr != nil || found {
		return nil, delErr
	}
	return nil, fmt.Errorf("%w: %s: user %s", core.ErrNotFound, op, in.UserID)
}

// userListConfirmResponse is the shape userFoundAfterServerError decodes on
// its own, rather than through ListUsers: TotalPage and TotalItem are
// pointers so the confirm can tell "the response carried no totals" (nil)
// apart from "the response said 0" (non-nil, pointing at 0). This mirrors
// repositoryListConfirmResponse.
type userListConfirmResponse struct {
	ListData  []User `json:"listData"`
	Data      []User `json:"data"`
	TotalPage *int   `json:"totalPage"`
	TotalItem *int   `json:"totalItem"`
}

// items returns whichever of ListData or Data the response carried.
func (r *userListConfirmResponse) items() []User {
	if r.ListData != nil {
		return r.ListData
	}
	return r.Data
}

// userFoundAfterServerError lists users once, asking for a page large
// enough to hold the account's whole user list, and reports whether id is
// present, for DeleteUser's list confirm after a 5xx. This mirrors
// repositoryFoundAfterServerError: absence is conclusive only when the
// response actually carries totals that account for every row, and any
// other shape returns an error rather than a bare false, since
// DeleteUser treats an error here as "fall back to the original 5xx."
func (c *Client) userFoundAfterServerError(ctx context.Context, id string) (bool, error) {
	q := url.Values{}
	q.Set("name", "")
	q.Set("size", strconv.Itoa(core.DefaultPageSize))

	var resp userListConfirmResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "containerregistry.userFoundAfterServerError",
		Method:    http.MethodGet,
		URL:       c.vcrURL([]string{"user"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return false, err
	}

	items := resp.items()
	for _, u := range items {
		if u.ID == id {
			return true, nil
		}
	}
	if resp.TotalPage == nil || resp.TotalItem == nil {
		return false, errors.New("containerregistry: user list confirm: response carried no totals; inconclusive")
	}
	if *resp.TotalPage > 1 || len(items) != *resp.TotalItem {
		return false, fmt.Errorf("containerregistry: user list confirm: got %d of %d item(s) across %d page(s); inconclusive",
			len(items), *resp.TotalItem, *resp.TotalPage)
	}
	return false, nil
}
