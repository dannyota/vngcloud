package transport

import (
	"encoding/json"

	"danny.vn/vngcloud/internal/jsonresponse"
)

func decodeSuccessBody(req Request, statusCode int, body []byte, out any) (int, error) {
	if req.AllowEmptyBody && len(body) == 0 {
		if req.BodyWasEmpty != nil {
			*req.BodyWasEmpty = true
		}
		return statusCode, nil
	}
	if out != nil && (len(body) > 0 || req.StrictJSON) {
		var err error
		if req.StrictJSON {
			err = jsonresponse.ValidateStrict(body, out)
		}
		if err == nil {
			err = json.Unmarshal(body, out)
		}
		if err != nil {
			if req.discovery {
				return statusCode, &APIError{Operation: req.Operation, StatusCode: statusCode, Code: "InvalidResponse", Message: "response failed to decode; body withheld"}
			}
			if req.Sensitive {
				// Sensitive responses withhold validation and decode error details.
				return statusCode, &APIError{Operation: req.Operation, Message: "response failed to decode; body withheld"}
			}
			return statusCode, &APIError{Operation: req.Operation, Err: sanitizeDecodeError(err)}
		}
	}
	return statusCode, nil
}

func bodyFailure(req Request, status int, cause error) *APIError {
	err := &APIError{Operation: req.Operation, Err: sanitizeNetworkError(cause, req.redactValues())}
	if req.KeepObservedStatus {
		err.StatusCode = status
		err.KeptObservedStatus = true
	}
	return err
}
