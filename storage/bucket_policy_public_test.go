package storage

import (
	"errors"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func statementDoc(effect, principal string) string {
	return `{"Version":"2012-10-17","Statement":[{"Effect":"` + effect + `","Principal":` + principal +
		`,"Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}]}`
}

func TestPolicyHasPublicPrincipal(t *testing.T) {
	const named = `{"AWS":["arn:aws:iam:::user/u:sa-n"]}`
	tests := []struct {
		name   string
		policy string
		want   bool
	}{
		{"string star", statementDoc("Allow", `"*"`), true},
		{"AWS string star", statementDoc("Allow", `{"AWS":"*"}`), true},
		{"AWS list star", statementDoc("Allow", `{"AWS":["*"]}`), true},
		{"AWS list with a star among ARNs", statementDoc("Allow", `{"AWS":["arn:aws:iam:::user/a","*"]}`), true},
		{"ARN with a wildcard", statementDoc("Allow", `{"AWS":"arn:aws:iam:::user/u:*"}`), true},
		{"string with a wildcard", statementDoc("Allow", `"arn:aws:iam:::user/*"`), true},
		{"second statement is public", `{"Statement":[` +
			`{"Effect":"Allow","Principal":` + named + `,"Action":"s3:GetObject","Resource":"*"},` +
			`{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"*"}]}`, true},
		{"Deny with a star", statementDoc("Deny", `"*"`), false},
		{"Deny with AWS star", statementDoc("Deny", `{"AWS":["*"]}`), false},
		{"named ARN list", statementDoc("Allow", named), false},
		{"named ARN string", statementDoc("Allow", `{"AWS":"arn:aws:iam:::user/u:sa-n"}`), false},
		{"named string", statementDoc("Allow", `"arn:aws:iam:::user/u:sa-n"`), false},
		{"service principal", statementDoc("Allow", `{"Service":"s3.amazonaws.com"}`), false},
		{"Deny star then named Allow", `{"Statement":[` +
			`{"Effect":"Deny","Principal":"*","Action":"s3:*","Resource":"*"},` +
			`{"Effect":"Allow","Principal":` + named + `,"Action":"s3:GetObject","Resource":"*"}]}`, false},
		{"star in a Resource only", `{"Statement":[{"Effect":"Allow","Principal":` + named + `,"Action":"s3:*","Resource":"*"}]}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PolicyHasPublicPrincipal(tt.policy)
			if err != nil || got != tt.want {
				t.Fatalf("got %v, err %v, want %v", got, err, tt.want)
			}
		})
	}
}

func TestPolicyHasPublicPrincipalRefusesWhatPutRefuses(t *testing.T) {
	for name, policy := range map[string]string{
		"empty":             "",
		"invalid JSON":      `{"Statement":[`,
		"not an object":     `[]`,
		"no Statement":      `{}`,
		"no Principal":      `{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`,
		"only NotPrincipal": `{"Statement":[{"Effect":"Allow","NotPrincipal":"x","Action":"s3:*","Resource":"*"}]}`,
		"repeated Principal": `{"Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:*","Resource":"*",` +
			`"Principal":{"AWS":["arn:aws:iam:::user/u"]}}]}`,
		"repeated Statement": `{"Statement":[],"Statement":[` +
			`{"Effect":"Allow","Principal":"*","Action":"s3:*","Resource":"*"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := PolicyHasPublicPrincipal(policy)
			if got || !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("got %v, err %v, want false and ErrInvalidInput", got, err)
			}
			if strings.Contains(err.Error(), "arn:") {
				t.Fatalf("the error quotes the policy: %v", err)
			}
		})
	}
}
