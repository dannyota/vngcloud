// Package billingresources shares the central billing resource transport.
package billingresources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

const ListOperation = "billing.ListResources"
const AccountOperation = "billingresources.GetAccount"

// Resource validates typed fields before storage uses the billing list.
type Resource struct {
	ArtifactID       string          `json:"artifactId"`
	ArtifactType     string          `json:"artifactType"`
	Product          string          `json:"product"`
	RenewType        string          `json:"renewType"`
	BillingType      string          `json:"billingType"`
	EndBillingTime   *int64          `json:"endBillingTime"`
	RenewPeriod      *int64          `json:"renewPeriod"`
	Channel          *int64          `json:"channel"`
	IsRenewing       *bool           `json:"isRenewing"`
	ArtifactName     string          `json:"artifactName"`
	StartBillingTime *int64          `json:"startBillingTime"`
	DateLeft         *int64          `json:"dateLeft"`
	Cost             *float64        `json:"cost"`
	Status           json.RawMessage `json:"status"`
	StatusUI         json.RawMessage `json:"statusUI"`
	BillingElements  []struct {
		SKU      string          `json:"sku"`
		Quantity json.RawMessage `json:"quantity"`
		MetaKey  json.RawMessage `json:"metaKey"`
	} `json:"billingElements"`
}

func route(c *core.Client, parts ...string) string {
	return c.RouteURL(routes.Route{Product: routes.ProductBilling, Version: "gateway/api/v1", Parts: parts})
}

func responseError(op string, status int) error {
	return &core.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "billing response had invalid structure"}
}

func object(raw json.RawMessage) bool {
	b := bytes.TrimSpace(raw)
	return len(b) > 0 && b[0] == '{'
}

func exchange(ctx context.Context, c *core.Client, req transport.Request) (json.RawMessage, int, error) {
	var raw json.RawMessage
	var credential string
	req.SentCredential = &credential
	req.NoRedirect = true
	// Account data and resource tags must not enter routine captures or errors.
	req.Sensitive = true
	req.WithholdMessage = "billing response withheld"
	status, err := c.DoJSONStatus(ctx, req, &raw)
	if err != nil {
		var api *core.APIError
		if errors.As(err, &api) {
			safe := *api
			if safe.StatusCode == 0 && status > 0 {
				safe.StatusCode = status
			}
			safe.Code = core.ResolvedCode(safe.StatusCode, "")
			safe.Message = "billing response withheld"
			return nil, status, &safe
		}
		return nil, status, err
	}
	var env struct {
		Code json.RawMessage `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if status != http.StatusOK || !object(raw) || json.Unmarshal(raw, &env) != nil || !successCode(env.Code) || !object(env.Data) {
		return nil, status, responseError(req.Operation, status)
	}
	return env.Data, status, nil
}

// List requests the observed unfiltered list without inferring pagination.
func List(ctx context.Context, c *core.Client) (json.RawMessage, error) {
	data, status, err := exchange(ctx, c, transport.Request{Operation: ListOperation, Method: http.MethodGet, URL: route(c, "resources")})
	if err != nil {
		return nil, err
	}
	var wire struct {
		Items []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &wire) != nil || wire.Items == nil {
		return nil, responseError(ListOperation, status)
	}
	var typed struct {
		Items                 []Resource `json:"data"`
		TotalAutoRenews       *int64     `json:"totalAutoRenews"`
		TotalGoodResources    *int64     `json:"totalGoodResources"`
		TotalWarningResources *int64     `json:"totalWarningResources"`
		TotalAlertedResources *int64     `json:"totalAlertedResources"`
		TotalExpiredResources *int64     `json:"totalExpiredResources"`
		Extra                 struct {
			WarningThresholds json.RawMessage `json:"warningThresholds"`
			AlarmThresholds   json.RawMessage `json:"alarmThresholds"`
		} `json:"extra"`
	}
	if json.Unmarshal(data, &typed) != nil {
		return nil, responseError(ListOperation, status)
	}
	for _, item := range wire.Items {
		if !object(item) {
			return nil, responseError(ListOperation, status)
		}
	}
	return data, nil
}

// Account resolves a positive integral account identity for this operation only.
func Account(ctx context.Context, c *core.Client) (string, error) {
	data, status, err := exchange(ctx, c, transport.Request{Operation: AccountOperation, Method: http.MethodGet, URL: route(c, "home", "user-info")})
	if err != nil {
		return "", err
	}
	var wire struct {
		AccountID json.RawMessage `json:"accountId"`
	}
	if json.Unmarshal(data, &wire) != nil || len(wire.AccountID) == 0 || len(wire.AccountID) > 128 {
		return "", responseError(AccountOperation, status)
	}
	var number json.Number
	if json.Unmarshal(wire.AccountID, &number) != nil || bytes.TrimSpace(wire.AccountID)[0] == '"' {
		return "", responseError(AccountOperation, status)
	}
	account, ok := new(big.Rat).SetString(number.String())
	if !ok || !account.IsInt() || !account.Num().IsInt64() || account.Sign() <= 0 {
		return "", responseError(AccountOperation, status)
	}
	return strconv.FormatInt(account.Num().Int64(), 10), nil
}

type Setting struct {
	Product       string        `json:"product"`
	ArtifactType  string        `json:"artifactType"`
	ArtifactID    string        `json:"artifactId"`
	Channel       int64         `json:"channel"`
	AutoRenewInfo AutoRenewInfo `json:"autoRenewInfo"`
}

type AutoRenewInfo struct {
	IsEnable bool `json:"isEnable"`
	Period   int  `json:"period"`
}

// Put sends a single target setting without retry or redirect.
func Put(ctx context.Context, c *core.Client, op, account string, setting Setting) error {
	data, status, err := exchange(ctx, c, transport.Request{Operation: op, Method: http.MethodPut, URL: route(c, "resources", "autoRenew"), Body: []Setting{setting}, Once: true, NoRedirect: true, Redact: []string{account}, Headers: map[string]string{"portal-user-id": account}})
	if err != nil {
		return err
	}
	var wire struct {
		SuccessAll *bool             `json:"successAll"`
		Errors     []json.RawMessage `json:"errorAutoRenewResources"`
	}
	if json.Unmarshal(data, &wire) != nil || wire.SuccessAll == nil {
		return responseError(op, status)
	}
	if !*wire.SuccessAll || len(wire.Errors) > 0 {
		return &core.APIError{Operation: op, StatusCode: status, Code: "AutoRenewRejected", Message: fmt.Sprintf("server refused auto-renew setting; error count %d", len(wire.Errors))}
	}
	if wire.Errors == nil {
		return responseError(op, status)
	}
	return nil
}

func successCode(raw json.RawMessage) bool {
	if len(raw) == 0 || len(raw) > 128 || bytes.TrimSpace(raw)[0] == '"' {
		return false
	}
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return false
	}
	code, ok := new(big.Rat).SetString(number.String())
	return ok && code.Cmp(big.NewRat(200, 1)) == 0
}
