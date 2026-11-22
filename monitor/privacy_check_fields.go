package monitor

import (
	"encoding/json"
	"net/url"
	"strings"
)

func checkRequestCredentials(request CheckRequest) []string {
	values := monitorURLCredentials(nil, request.URL)
	for key, value := range request.Headers {
		values = monitorNamedCredentials(values, key, value)
	}
	for key, value := range request.Query {
		values = monitorNamedCredentials(values, key, value)
	}
	values = monitorNamedCredentials(values, "", request.Body)
	var body any
	decoder := json.NewDecoder(strings.NewReader(request.Body))
	decoder.UseNumber()
	if decoder.Decode(&body) == nil {
		values = monitorJSONCredentials(values, body)
	}
	return values
}

func monitorCredentialField(key string) bool {
	key = strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(key), "-", ""), "_", "")
	switch key {
	case "authorization", "proxyauthorization", "cookie", "setcookie", "key", "passwd", "pwd", "otp", "otpcode", "otpref":
		return true
	}
	for _, suffix := range []string{"auth", "authentication", "authkey", "password", "secret", "secretkey", "token", "apikey", "accesskey", "privatekey", "credential", "credentials", "signature"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func monitorNamedCredentials(values []string, key, value string) []string {
	scheme, _, _ := strings.Cut(strings.TrimSpace(value), " ")
	if monitorCredentialField(key) || strings.EqualFold(scheme, "Bearer") || strings.EqualFold(scheme, "Basic") {
		return monitorFieldSecrets(values, value)
	}
	return monitorURLCredentials(values, value)
}

func monitorURLCredentials(values []string, value string) []string {
	u, err := url.Parse(value)
	if err != nil {
		return values
	}
	if u.User != nil {
		password, _ := u.User.Password()
		values = append(values, u.User.Username(), password)
	}
	for key, parameters := range u.Query() {
		if monitorCredentialField(key) {
			values = append(values, parameters...)
		}
	}
	return values
}

func monitorJSONCredentials(values []string, value any) []string {
	switch v := value.(type) {
	case string:
		return monitorNamedCredentials(values, "", v)
	case []any:
		for _, item := range v {
			values = monitorJSONCredentials(values, item)
		}
	case map[string]any:
		for key, item := range v {
			if monitorCredentialField(key) {
				values = monitorJSONValues(values, item)
			} else {
				values = monitorJSONCredentials(values, item)
			}
		}
	}
	return values
}
