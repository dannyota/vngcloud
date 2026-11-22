package containerregistry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ErrUserNotFound means a create has no attributable user identity. Partial
// output retains SecretKey but leaves User empty; name matches grant no
// cleanup authority.
var ErrUserNotFound = errors.New("containerregistry: created user not found")

// UserPermission grants Actions on one repository to a user CreateUser
// makes. Each action name is resolved against ListPermissions, matching the
// server's own action string exactly; the server's list is the authority,
// not a value this SDK checks offline.
type UserPermission struct {
	RepositoryID string
	Actions      []string
}

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

type createUserResponse struct {
	SecretKey string `json:"secretKey"`
}

// CreateUserInput creates a repository user (a "robot account"). Name is
// required, Permissions must hold at least one entry, each with a
// path-safe RepositoryID and at least one action, and DurationDays, when
// set, must be at least 1. All of this is checked, and nothing sent, before
// any request.
type CreateUserInput struct {
	// Name must not exactly match an existing user name.
	Name string `vngcloud:"required"`
	// Description is sent as description in the create body.
	Description string
	// DurationDays must be at least one when set.
	DurationDays *int
	// Permissions needs at least one entry, each with a path-safe repository ID
	// and at least one action.
	Permissions []UserPermission `vngcloud:"required"`
}

// CreateUserOutput retains the one-time SecretKey even when ownership cannot
// be confirmed. User stays empty for the documented secret-only response.
type CreateUserOutput struct {
	User      User
	SecretKey vngcloud.Secret
}

// CreateUser checks permissions and complete name-search evidence, then sends
// once. Its secret-only response cannot prove creation ownership, so the
// result retains SecretKey with ErrUserNotFound and an empty User. Persist
// the secret before handling the error; reconcile ownership before cleanup.
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

	privacy := &registryPrivacy{}
	defer func() { clear(privacy.credentials) }()
	var permissionItems []Permission
	err := privacy.request(ctx, c, transport.Request{Operation: op, Method: http.MethodGet, URL: c.vcrURL([]string{"user", "permissions"}, nil), OK: []int{200}}, &permissionItems)
	perms := &ListPermissionsOutput{Items: permissionItems}
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
				return nil, fmt.Errorf("%w: %s: Permissions[%d] names an unknown action; known actions: %s",
					core.ErrInvalidInput, op, i, strings.Join(knownActions(perms.Items), ", "))
			}
			policyIDs = append(policyIDs, id)
		}
		body.PermissionRequestList = append(body.PermissionRequestList, permissionRequestBody{RepoID: p.RepositoryID, PolicyIDList: policyIDs})
	}

	existing, err := c.listUsersExactMatch(ctx, in.Name, privacy)
	if err != nil {
		return nil, fmt.Errorf("%s: could not prove the requested user name is absent: %w", op, err)
	}
	if len(existing) > 0 {
		return nil, fmt.Errorf("%w: %s: requested name already exists; check list-users --name", core.ErrInvalidInput, op)
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
	var sent []string
	defer func() { clear(sent) }()
	req.SentCredentials = &sent
	req.Redact = append([]string(nil), privacy.credentials...)
	req.WithholdMessage = "registry response withheld"
	req.ClassifyError = func(status int, _ string) string {
		if code := core.ResolvedCode(status, ""); code != "" {
			return code
		}
		return "RequestFailed"
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, wrapAmbiguousUserCreateErr(op, err)
	}
	privacy.credentials = append(privacy.credentials, sent...)
	if resp.SecretKey == "" {
		return nil, &core.APIError{Operation: op, StatusCode: http.StatusOK,
			Message: "create response had no secret key; a user may exist, check list-users --name and reconcile ownership before cleanup"}
	}

	out := &CreateUserOutput{SecretKey: vngcloud.Secret(resp.SecretKey)}
	privacy.credentials = append(privacy.credentials, resp.SecretKey)
	return out, c.findCreatedUser(ctx, op, in.Name, privacy)
}

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

func wrapAmbiguousUserCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if isClientRejectionError(err) {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; check list-users --name and reconcile ownership before any cleanup: %w", op, err)
}

const maxUserListPages = 100

func (c *Client) listUsersExactMatch(ctx context.Context, name string, privacy ...*registryPrivacy) ([]User, error) {
	p := &registryPrivacy{}
	if len(privacy) != 0 {
		p = privacy[0]
	}
	defer func() {
		if len(privacy) == 0 {
			clear(p.credentials)
		}
	}()
	if p.contains(reflect.ValueOf(name)) {
		return nil, errors.New("containerregistry: unsafe lookup identity")
	}
	seen := make(map[string]bool)
	totalPages, totalItems := -1, -1
	var matches []User
	collected := 0
	for page := 1; page <= maxUserListPages; page++ {
		q := url.Values{}
		q.Set("name", name)
		q.Set("page", strconv.Itoa(page))
		q.Set("size", strconv.Itoa(core.DefaultPageSize))

		var resp userListConfirmResponse
		if err := p.request(ctx, c, transport.Request{
			Operation: "containerregistry.listUsersExactMatch",
			Method:    http.MethodGet,
			URL:       c.vcrURL([]string{"user"}, q),
			OK:        []int{200},
		}, &resp); err != nil {
			return nil, err
		}

		items := resp.items()
		if totalPages < 0 {
			totalPages, totalItems = *resp.TotalPage, *resp.TotalItem
		}
		if totalPages != *resp.TotalPage || totalItems != *resp.TotalItem || resp.Page != 0 && resp.Page != page || (page < totalPages && len(items) == 0) {
			return nil, errors.New("containerregistry: unstable or nonprogressing user pages")
		}
		for _, u := range items {
			if core.CheckPathID("containerregistry.ListUsers", "UserID", u.ID) != nil || seen[u.ID] {
				return nil, errors.New("containerregistry: missing or repeated user identity")
			}
			if strings.TrimSpace(u.Name) == "" || !utf8.ValidString(u.Name) || strings.ContainsFunc(u.Name, unicode.IsControl) {
				return nil, errors.New("containerregistry: missing or invalid user name")
			}
			seen[u.ID] = true
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

func (c *Client) findCreatedUser(ctx context.Context, op, name string, privacy ...*registryPrivacy) error {
	if _, err := c.listUsersExactMatch(ctx, name, privacy...); err != nil {
		return fmt.Errorf("%w: %s: candidate lookup failed; reconcile with list-users before any cleanup: %w", ErrUserNotFound, op, err)
	}
	return fmt.Errorf("%w: %s: a name match cannot prove creation ownership; reconcile with list-users before any cleanup", ErrUserNotFound, op)
}

// DeleteUserInput identifies the user to delete: the robot account's own id
// (User.ID, the "uuid" field), not its UserID field.
type DeleteUserInput struct {
	// UserID is required; use ASCII letters, digits, or hyphens.
	UserID string `vngcloud:"required"`
}

// DeleteUserOutput carries no result fields.
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

type userListConfirmResponse = registryPage[User]

func (c *Client) userFoundAfterServerError(ctx context.Context, id string) (bool, error) {
	q := url.Values{}
	q.Set("name", "")
	q.Set("size", strconv.Itoa(core.DefaultPageSize))

	var resp userListConfirmResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation:  "containerregistry.userFoundAfterServerError",
		Method:     http.MethodGet,
		URL:        c.vcrURL([]string{"user"}, q),
		OK:         []int{200},
		StrictJSON: true,
		MaxBody:    4 << 20,
	}, &resp); err != nil {
		return false, err
	}

	items := resp.items()
	seen := make(map[string]bool, len(items))
	for _, u := range items {
		if core.CheckPathID("containerregistry.ListUsers", "UserID", u.ID) != nil || seen[u.ID] {
			return false, errors.New("containerregistry: user list has unproven identities")
		}
		seen[u.ID] = true
	}
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
