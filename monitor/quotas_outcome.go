package monitor

import (
	"errors"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
)

// ErrQuotaChanged means a valid identity or expiry guard no longer matches.
var ErrQuotaChanged = errors.New("monitor: quota changed")

// QuotaMutationOutcome reports transmission evidence separately from settlement.
type QuotaMutationOutcome struct {
	Sent  string
	Stage string
	Error *QuotaOutcomeError
}

// QuotaOutcomeError contains fixed classifications only, never response text.
type QuotaOutcomeError struct {
	Code      string
	Operation string
	Status    int
}

func quotaOutcomeError(op string, err error, status int) *QuotaOutcomeError {
	if err == nil {
		return nil
	}
	code := "RequestFailed"
	var api *core.APIError
	if errors.As(err, &api) {
		code = api.Code
		if code == "" {
			code = "RequestFailed"
		}
		if status == 0 {
			status = api.StatusCode
		}
	}
	switch {
	case errors.Is(err, dns.ErrNotSettled):
		code = "NotSettled"
	case errors.Is(err, ErrQuotaChanged):
		code = "QuotaChanged"
		status = 0
	case errors.Is(err, ErrUnpriced):
		code = "Unpriced"
		status = 0
	case errors.Is(err, ErrPriceAboveMax):
		code = "PriceAboveMax"
		status = 0
	case errors.Is(err, core.ErrNoCredentials), errors.Is(err, core.ErrCredentialsFile):
		code = "NoCredentials"
	case errors.Is(err, core.ErrInvalidConfig):
		code = "InvalidConfig"
	case errors.Is(err, ErrUnexpectedStatus):
		code = "UnexpectedStatus"
	case errors.Is(err, core.ErrInvalidInput) && !errors.As(err, &api):
		code = "InvalidUsage"
	case errors.Is(err, core.ErrTokenCache):
		code = "TokenCacheFailed"
	}
	var login *core.ServiceAccountLoginError
	if !errors.Is(err, dns.ErrNotSettled) && errors.As(err, &login) {
		code = "ServiceAccountLoginFailed"
		if status == 0 {
			status = login.Status
		}
	}
	return &QuotaOutcomeError{Code: code, Operation: op, Status: status}
}
