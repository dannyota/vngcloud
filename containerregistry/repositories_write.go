package containerregistry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// repoNamePattern is the server's own rule for a repository Name, confirmed
// by a live 400 on a name outside it: 6 to 20 characters, only a-z, 0-9,
// '_', and '-', starting with a letter or digit (not '_' or '-').
// CreateRepository checks it before any request, since the server's only
// feedback otherwise is that same 400 after a round trip.
var repoNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{5,19}$`)

// checkRepositoryName returns an error wrapping core.ErrInvalidInput when
// name does not match repoNamePattern.
func checkRepositoryName(op, name string) error {
	if repoNamePattern.MatchString(name) {
		return nil
	}
	return fmt.Errorf("%w: %s requires Name to be 6 to 20 characters, only a-z, 0-9, '_', and '-', and to start with a letter or digit, got %q",
		core.ErrInvalidInput, op, name)
}

var (
	// ErrRepositoryNotEmpty means DeleteRepository refused because the
	// repository's pre-delete read showed ImageCount above 0. Nothing was
	// sent; emptying a repository is image work for docker or the console.
	ErrRepositoryNotEmpty = errors.New("containerregistry: repository holds images")

	// ErrNotSettled means a repository create or delete was accepted but
	// did not reach its settled state within the wait's bound, or the wait
	// itself failed to read or sleep, such as from a canceled ctx. For a
	// create, the repository exists and must not be created again; for a
	// delete, the delete was sent and a rerun is safe.
	ErrNotSettled = errors.New("containerregistry: write accepted but not settled")
)

// isVCRServerError reports whether err is a *core.APIError with a 5xx
// status: the trigger for GetRepository's list confirm. The reference
// documents only 500 as an error status for repository calls, besides 401,
// so how a missing repository's read answers is unconfirmed; this SDK
// treats an ambiguous 5xx the same way network ACLs already treat their own
// read, which also answers 500 rather than 404 for a deleted resource.
func isVCRServerError(err error) bool {
	var apiErr *core.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode >= 500 && apiErr.StatusCode < 600
}

// is4xxAPIError reports whether err is a *core.APIError whose StatusCode is
// 4xx, meaning the server rejected the request outright and never acted on
// it.
func is4xxAPIError(err error) bool {
	var apiErr *core.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500
}

// repositoryFoundAfterServerError lists repositories once and reports
// whether id is present, for GetRepository's list confirm after a 5xx. A
// list that comes back short of the account's own count, more than one page
// or fewer items than TotalItem, is not a reliable absence: id could simply
// be on a page this call never asked for. GetRepository treats any error
// from this method as "fall back to the original 5xx," so it returns an
// error instead of a bare false in that case, rather than answering a
// question the single-page list cannot actually settle.
func (c *Client) repositoryFoundAfterServerError(ctx context.Context, id string) (bool, error) {
	out, err := c.ListRepositories(ctx, nil)
	if err != nil {
		return false, err
	}
	for _, r := range out.Items {
		if r.ID == id {
			return true, nil
		}
	}
	if out.TotalPage > 1 || len(out.Items) < out.TotalItem {
		return false, fmt.Errorf("containerregistry: repository list confirm: got %d of %d item(s) across %d page(s); inconclusive",
			len(out.Items), out.TotalItem, out.TotalPage)
	}
	return false, nil
}

// GetRepositoryInput identifies the repository to read.
type GetRepositoryInput struct {
	RepositoryID string `vngcloud:"required"`
}

type GetRepositoryOutput struct {
	Repository Repository
}

// GetRepository reads a repository.
//
// How a missing repository's read answers is unconfirmed: the reference
// documents only 500 besides 401 for this call, never 404. A plain 404 (an
// id that was never valid) becomes core.ErrNotFound through the shared
// transport, with no extra call. After any other error with a 5xx status,
// GetRepository calls ListRepositories once and checks whether
// RepositoryID is still listed: absent, it returns an error wrapping
// core.ErrNotFound; listed, or if the list call itself fails, it returns
// the original 5xx.
func (c *Client) GetRepository(ctx context.Context, in *GetRepositoryInput) (*GetRepositoryOutput, error) {
	const op = "containerregistry.GetRepository"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "RepositoryID", in.RepositoryID); err != nil {
		return nil, err
	}

	var repo Repository
	getErr := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.vcrURL([]string{"repository", in.RepositoryID}, nil),
		OK:        []int{200},
	}, &repo)
	if getErr == nil {
		return &GetRepositoryOutput{Repository: repo}, nil
	}
	if !isVCRServerError(getErr) {
		return nil, getErr
	}
	found, listErr := c.repositoryFoundAfterServerError(ctx, in.RepositoryID)
	if listErr != nil || found {
		return nil, getErr
	}
	return nil, fmt.Errorf("%w: %s: repository %s", core.ErrNotFound, op, in.RepositoryID)
}

// CreateRepositoryInput creates a private repository. There is no Public
// field: per the design, every repository this SDK creates is private,
// since a public one accepts anonymous push, letting anyone store images on
// the account's quota under the account's name.
//
// Name must be 6 to 20 characters, only a-z, 0-9, '_', and '-', starting
// with a letter or digit; see repoNamePattern.
type CreateRepositoryInput struct {
	Name         string `vngcloud:"required"`
	QuotaLimitGB int    `vngcloud:"required"`

	NoWait bool
}

type CreateRepositoryOutput struct {
	Repository Repository
}

// createRepositoryBody is CreateRepository's request body. isPublic is
// always false.
type createRepositoryBody struct {
	RepoName   string `json:"repoName"`
	QuotaLimit int    `json:"quotaLimit"`
	IsPublic   bool   `json:"isPublic"`
}

// CreateRepository creates a private repository.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError, the
// repository may exist, and the caller runs list-repositories --name
// <name> and matches the exact name (the server applies no account prefix;
// Repository.Name equals the Input's Name) before creating it again, rather
// than retrying blind.
//
// The create response carries no status to wait on: a live create has been
// observed to complete in about 4 seconds, with the repository already
// readable through GetRepository by the time the POST returns. Without
// NoWait, CreateRepository confirms the new repository with one such read,
// tolerating a 404 by polling every 2 seconds for up to 60 seconds of
// elapsed time in case that visibility ever lags. If the wait's bound runs
// out, or a read or a sleep in that wait fails, such as from a canceled
// ctx, the returned error wraps ErrNotSettled and the Output still holds
// the repository: the last one a read returned, or, if none did, the one
// the create response itself carried. Either way the Output is never nil
// and the caller keeps the new repository's id.
func (c *Client) CreateRepository(ctx context.Context, in *CreateRepositoryInput) (*CreateRepositoryOutput, error) {
	const op = "containerregistry.CreateRepository"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := checkRepositoryName(op, in.Name); err != nil {
		return nil, err
	}
	if in.QuotaLimitGB < 1 {
		return nil, fmt.Errorf("%w: %s requires QuotaLimitGB to be at least 1, got %d", core.ErrInvalidInput, op, in.QuotaLimitGB)
	}

	var repo Repository
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.vcrURL([]string{"repository"}, nil),
		Body:      createRepositoryBody{RepoName: in.Name, QuotaLimit: in.QuotaLimitGB, IsPublic: false},
		OK:        []int{202},
	}
	if err := c.c.DoJSON(ctx, req, &repo); err != nil {
		return nil, wrapAmbiguousRepositoryCreateErr(op, err)
	}
	if repo.ID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: http.StatusAccepted,
			Message: "create response had no id; list-repositories --name <name> and match the exact name before creating again"}
	}
	if in.NoWait {
		return &CreateRepositoryOutput{Repository: repo}, nil
	}

	confirmed, waitErr := c.waitRepositoryConfirmed(ctx, op, repo.ID)
	if confirmed == nil {
		confirmed = &repo
	}
	return &CreateRepositoryOutput{Repository: *confirmed}, waitErr
}

// wrapAmbiguousRepositoryCreateErr wraps err, from the create POST op just
// sent, with a hint to list repositories before creating again, unless err
// is already a 4xx *core.APIError: a 4xx means the server rejected the
// request outright, so nothing was created and the exact same call is safe
// to retry. Any other error leaves whether the repository was created
// unknown.
func wrapAmbiguousRepositoryCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if is4xxAPIError(err) {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list-repositories --name <name> and match the exact name before creating again: %w", op, err)
}

// waitRepositoryConfirmed is CreateRepository's post-create confirm unless
// NoWait is set. The create response carries no status field, so this reads
// repositoryID with GetRepository until the read succeeds, tolerating a 404
// by polling rather than failing at once, since a repository just created
// may not be readable for a moment; any other read failure stops the wait
// and is returned as is.
//
// It returns the last repository a read returned alongside the outcome: nil
// error once a read succeeds, or an error wrapping ErrNotSettled once the
// bound runs out or a read or a sleep fails. The returned repository is nil
// only when no read ever succeeded, in which case the caller falls back to
// whatever the create response itself produced.
func (c *Client) waitRepositoryConfirmed(ctx context.Context, op, repositoryID string) (*Repository, error) {
	var repo *Repository
	err := poll(ctx, c.now, c.sleep, repoPollInterval, repoPollBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetRepository(ctx, &GetRepositoryInput{RepositoryID: repositoryID})
			if err != nil {
				if core.IsNotFound(err) {
					return false, nil
				}
				return true, err
			}
			repo = &out.Repository
			return true, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: repository %s was not confirmed by a read within %s; the repository exists and this create must not be repeated",
				ErrNotSettled, op, repositoryID, repoPollBound)
		},
	)
	if err != nil && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: repository %s: %w", ErrNotSettled, op, repositoryID, err)
	}
	return repo, err
}

// DeleteRepositoryInput identifies the repository to delete.
type DeleteRepositoryInput struct {
	RepositoryID string `vngcloud:"required"`

	NoWait bool
}

type DeleteRepositoryOutput struct{}

// DeleteRepository reads the repository first and returns
// ErrRepositoryNotEmpty, sending nothing, when ImageCount is above 0:
// emptying a repository is image work for docker or the console. Attached
// users do not block the delete: they keep existing and lose access to the
// deleted repository. That read also gives the SDK's ordinary not-found
// sentinel, through GetRepository, when RepositoryID no longer exists,
// including on a retried delete.
//
// DELETE is idempotent and keeps the transport's normal retries. Without
// NoWait, DeleteRepository then waits for the repository to become
// unreadable, polling GetRepository every 2 seconds for up to 60 seconds of
// elapsed time: a plain 404, or, on an ambiguous 5xx, absence from
// ListRepositories, both through GetRepository's own not-found handling. If
// the bound runs out, or a read or sleep fails, the returned error wraps
// ErrNotSettled; a rerun is safe either way, since DeleteRepository always
// reads first.
func (c *Client) DeleteRepository(ctx context.Context, in *DeleteRepositoryInput) (*DeleteRepositoryOutput, error) {
	const op = "containerregistry.DeleteRepository"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "RepositoryID", in.RepositoryID); err != nil {
		return nil, err
	}

	current, err := c.GetRepository(ctx, &GetRepositoryInput{RepositoryID: in.RepositoryID})
	if err != nil {
		return nil, err
	}
	if current.Repository.ImageCount > 0 {
		return nil, fmt.Errorf("%w: %s: repository %s has %d image(s); delete them first",
			ErrRepositoryNotEmpty, op, in.RepositoryID, current.Repository.ImageCount)
	}

	var deleted Repository
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.vcrURL([]string{"repository", in.RepositoryID}, nil),
		OK:        []int{202},
	}
	if err := c.c.DoJSON(ctx, req, &deleted); err != nil {
		return nil, err
	}
	if in.NoWait {
		return &DeleteRepositoryOutput{}, nil
	}
	if err := c.waitRepositoryAbsent(ctx, op, in.RepositoryID); err != nil {
		return &DeleteRepositoryOutput{}, err
	}
	return &DeleteRepositoryOutput{}, nil
}

// waitRepositoryAbsent is DeleteRepository's post-delete wait unless NoWait
// is set: it reads repositoryID with GetRepository until the read reports
// core.ErrNotFound. GetRepository itself resolves that from either a plain
// 404 or, on an ambiguous 5xx, absence from ListRepositories, so this single
// check covers both signals. Any other read failure stops the wait and is
// returned as is.
func (c *Client) waitRepositoryAbsent(ctx context.Context, op, repositoryID string) error {
	err := poll(ctx, c.now, c.sleep, repoPollInterval, repoPollBound,
		func(ctx context.Context) (bool, error) {
			_, err := c.GetRepository(ctx, &GetRepositoryInput{RepositoryID: repositoryID})
			if err == nil {
				return false, nil
			}
			if core.IsNotFound(err) {
				return true, nil
			}
			return true, err
		},
		func() error {
			return fmt.Errorf("%w: %s: repository %s did not become unreadable within %s; delete was sent and a rerun is safe",
				ErrNotSettled, op, repositoryID, repoPollBound)
		},
	)
	if err != nil && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: repository %s: %w", ErrNotSettled, op, repositoryID, err)
	}
	return err
}
