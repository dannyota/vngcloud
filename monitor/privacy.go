package monitor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"danny.vn/vngcloud/internal/transport"
)

type monitorPrivacy struct {
	values      monitorReadCredentials
	modelValues monitorReadCredentials
	credentials monitorReadCredentials
	exhausted   bool
}

func monitorPrivateContext(ctx context.Context, values ...string) (context.Context, *monitorPrivacy) {
	p := &monitorPrivacy{}
	p.add(values...)
	return transport.WithCredentialObserver(ctx, func(value string) ([]string, error) {
		p.add(value)
		if !p.credentials.add([]string{value}) {
			p.exhausted = true
		}
		if p.exhausted {
			return nil, errors.New("monitor: credential safety limit exceeded")
		}
		return p.values, nil
	}), p
}

func (p *monitorPrivacy) add(values ...string) {
	if !p.values.add(values) || !p.modelValues.add(values) {
		p.exhausted = true
	}
}

func (p *monitorPrivacy) addCheckRequest(request CheckRequest) {
	if !p.values.add(checkRequestSecrets(request)) || !p.modelValues.add(checkRequestCredentials(request)) {
		p.exhausted = true
	}
}

func channelSecrets(address string, headers []ChannelHeader, headerField, otp, ref string) []string {
	values := []string{headerField, otp, ref}
	values = monitorFieldSecrets(values, address)
	for _, header := range headers {
		values = monitorFieldSecrets(values, header.Value)
	}
	if len(headers) == 0 {
		values = monitorJSONSecrets(values, headerField)
	}
	return values
}

func checkRequestSecrets(request CheckRequest) []string {
	values := monitorFieldSecrets(nil, request.URL)
	values = append(values, request.Body)
	values = monitorJSONSecrets(values, request.Body)
	for _, value := range request.Headers {
		values = monitorFieldSecrets(values, value)
	}
	for _, value := range request.Query {
		values = monitorFieldSecrets(values, value)
	}
	return values
}

func monitorFieldSecrets(values []string, value string) []string {
	values = append(values, value)
	if u, err := url.Parse(value); err == nil {
		if u.User != nil {
			password, _ := u.User.Password()
			values = append(values, u.User.Username(), password)
		}
		for _, parameters := range u.Query() {
			values = append(values, parameters...)
		}
	}
	scheme, token, _ := strings.Cut(strings.TrimSpace(value), " ")
	token = strings.TrimSpace(token)
	if strings.EqualFold(scheme, "Bearer") {
		values = append(values, token)
	}
	if strings.EqualFold(scheme, "Basic") {
		if decoded, err := base64.StdEncoding.DecodeString(token); err == nil {
			username, password, _ := strings.Cut(string(decoded), ":")
			values = append(values, string(decoded), username, password)
		}
	}
	return values
}

func monitorJSONSecrets(values []string, raw string) []string {
	var value any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return values
	}
	return monitorJSONValues(values, value)
}

func monitorJSONValues(values []string, value any) []string {
	switch v := value.(type) {
	case string:
		return monitorFieldSecrets(values, v)
	case json.Number:
		return append(values, v.String())
	case []any:
		for _, item := range v {
			values = monitorJSONValues(values, item)
		}
	case map[string]any:
		for _, item := range v {
			values = monitorJSONValues(values, item)
		}
	}
	return values
}
