package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/routes"
)

func (c *Client) writeProvenance() error {
	expected := endpoints.ResolveIAMUser(c.c.Region(), endpoints.Overrides{})
	if c.c.Region() != "hcm-3" || !c.c.UsesIAMUserLogin() || !c.c.UsesDefaultAuthEndpoints() || c.c.Endpoint(routes.ProductDatabase) != expected.Database {
		return fmt.Errorf("%w: database writes require hcm-3, SDK IAM-user login, and default endpoints", core.ErrInvalidConfig)
	}
	return nil
}

func contextSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var errPollTimeout = errors.New("database: wait timed out")

func (c *Client) poll(ctx context.Context, step func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, pollBound)
	defer cancel()
	deadline := c.now().Add(pollBound)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if done, err := step(ctx); done {
			return err
		}
		remaining := deadline.Sub(c.now())
		if remaining <= 0 {
			return errPollTimeout
		}
		if err := c.sleep(ctx, min(pollInterval, remaining)); err != nil {
			return err
		}
	}
}

func settleWait(err error, what string) error {
	if err == nil || errors.Is(err, ErrFailed) || errors.Is(err, ErrNotSettled) {
		return err
	}
	return fmt.Errorf("%w: %s confirmation interrupted", ErrNotSettled, what)
}

func reflects(inst Instance, password string) bool {
	raw, _ := json.Marshal(inst)
	escaped, _ := json.Marshal(password)
	text := string(raw)
	return strings.Contains(text, password) || strings.Contains(text, strings.Trim(string(escaped), `"`))
}

var requestFieldNames = map[string]string{
	"name": "Name", "password": "Password", "user.password": "Password",
	"user.name": "Username", "username": "Username",
	"databases.name": "DatabaseName", "user.databases.name": "DatabaseName", "databasename": "DatabaseName",
	"volumetype": "StorageTypeID", "volumesize": "StorageSize", "packageid": "FlavorID", //nolint:misspell // lowercased API field packageId
	"locatezoneid": "ZoneID", "netids": "SubnetID",
}

var indexPattern = regexp.MustCompile(`\[[0-9]*\]`)

const maxFieldErrors = 5

func parseFieldErrors(body []byte) []string {
	var env struct {
		FieldError []struct {
			Field string `json:"field"`
		} `json:"fieldError"`
	}
	if json.Unmarshal(body, &env) != nil {
		return nil
	}
	var out []string
	for i, e := range env.FieldError {
		if i == 20 || len(out) == maxFieldErrors {
			break
		}
		name, ok := requestFieldNames[strings.ToLower(indexPattern.ReplaceAllString(e.Field, ""))]
		if ok && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

func writeError(op string, err error, fields []string) error {
	safe := safeError(op, err)
	var provider *core.APIError
	if errors.As(err, &provider) && provider.StatusCode >= 400 {
		safe.Message = "request rejected by the API"
		if len(fields) > 0 {
			safe.Message = "invalid input rejected by the API: " + strings.Join(fields, ", ")
		}
	}
	return safe
}

func leaksDecoded(raw []byte, secrets []string) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	for {
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			return false
		}
		if err != nil {
			return true
		}
		if text, ok := token.(string); ok {
			for _, s := range secrets {
				if s != "" && strings.Contains(text, s) {
					return true
				}
			}
		}
	}
}

func definitiveWriteError(err error, notSent bool, status int) bool {
	if notSent {
		return true
	}
	var api *core.APIError
	if errors.As(err, &api) {
		status = api.StatusCode
	}
	return status >= 400 && status < 500
}
