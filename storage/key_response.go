package storage

import (
	"encoding/json"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/transport"
)

func keyResponseOperation(op string) bool {
	return op == "storage.CreateS3Key" || op == "storage.ListS3Keys"
}

func validateKeyResponse(op string, status int, raw json.RawMessage, credentials []string) error {
	invalid := func() error {
		message := "invalid key response; body withheld"
		if op == "storage.CreateS3Key" {
			message += keyMayExistHint
		}
		return &core.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: message}
	}
	var env envelope
	if jsonresponse.ValidateStrict(raw, &env) != nil || json.Unmarshal(raw, &env) != nil {
		return invalid()
	}
	var keys []createdS3Key
	if op == "storage.CreateS3Key" {
		var key createdS3Key
		if len(env.Data) > 0 && (jsonresponse.ValidateStrict(env.Data, &key) != nil || json.Unmarshal(env.Data, &key) != nil) {
			return invalid()
		}
		keys = append(keys, key)
	} else {
		data := env.Datas
		if len(data) == 0 || string(data) == "null" {
			data = env.Data
		}
		if len(data) > 0 && (jsonresponse.ValidateStrict(data, &keys) != nil || json.Unmarshal(data, &keys) != nil) {
			return invalid()
		}
	}
	for _, key := range keys {
		if key.SecretKey != "" {
			credentials = append(credentials, key.SecretKey)
		}
	}
	ordinary := []any{env.Code, env.ErrMsg}
	for _, key := range keys {
		ordinary = append(ordinary, key.S3Key)
	}
	for _, value := range ordinary {
		encoded, err := json.Marshal(value)
		if err != nil || transport.RedactValues(string(encoded), credentials...) != string(encoded) {
			return invalid()
		}
	}
	return nil
}

func safeKeyHTTPError(err *core.APIError) *core.APIError {
	safe := *err
	switch safe.Code {
	case "112", "114", "400", "401", "403", "404", "409", "429", "500", "502", "503", "IAM_PERMISSION_DENIED", "EmptyResponse":
	default:
		safe.Code = core.ResolvedCode(safe.StatusCode, "")
	}
	if safe.StatusCode != 0 || safe.Err == nil {
		safe.Message = "key response withheld"
	}
	return &safe
}
