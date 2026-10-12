package cli

import (
	"errors"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestS3CredentialsFile(t *testing.T) {
	good := "[default]\naws_access_key_id = synthetic-id\naws_secret_access_key = synthetic=secret==\n"
	for name, tc := range map[string]struct{ content, accessKey string }{
		"LF":                   {good, "synthetic-id"},
		"CRLF":                 {strings.ReplaceAll(good, "\n", "\r\n"), "synthetic-id"},
		"comments":             {"# comment\n; comment\n\n" + good, "synthetic-id"},
		"UTF8":                 {strings.ReplaceAll(good, "synthetic-id", "clé"), "clé"},
		"ASCII whitespace":     {strings.ReplaceAll(good, "synthetic-id", "\t synthetic-id \t"), "synthetic-id"},
		"non-ASCII whitespace": {strings.ReplaceAll(good, "synthetic-id", "\u00a0synthetic-id\u00a0"), "\u00a0synthetic-id\u00a0"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseS3Credentials([]byte(tc.content))
			if err != nil || got.AccessKeyID.Reveal() != tc.accessKey || got.SecretAccessKey.Reveal() != "synthetic=secret==" {
				t.Fatalf("valid pair rejected or changed: %v", err)
			}
		})
	}
	for name, content := range map[string]string{
		"duplicate section": good + "[default]\n", "duplicate key": good + "aws_access_key_id = other\n",
		"duplicate secret": good + "aws_secret_access_key = hidden\n",
		"extra section":    good + "[other]\n", "extra field": good + "aws_session_token = hidden\n",
		"missing section": strings.TrimPrefix(good, "[default]\n"), "missing key": "[default]\naws_access_key_id = synthetic-id\n",
		"empty": strings.ReplaceAll(good, "synthetic-id", ""), "quote": strings.ReplaceAll(good, "synthetic-id", "\"hidden\""),
		"interpolation": strings.ReplaceAll(good, "synthetic-id", "${hidden}"), "percent interpolation": strings.ReplaceAll(good, "synthetic-id", "%(hidden)s"),
		"inline hash": strings.ReplaceAll(good, "synthetic-id", "synthetic-id # hidden"), "inline semicolon": strings.ReplaceAll(good, "synthetic-id", "synthetic-id ; hidden"),
		"control": strings.ReplaceAll(good, "synthetic-id", "hidden\x00"), "tab": strings.ReplaceAll(good, "synthetic-id", "hidden\tvalue"),
		"invalid UTF8": good + "\xff", "oversize": good + strings.Repeat(" ", 16*1024), "bare CR": strings.ReplaceAll(good, "synthetic-id", "hidden\rvalue"),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseS3Credentials([]byte(content))
			if !errors.Is(err, vngcloud.ErrCredentialsFile) || got.AccessKeyID.Reveal() != "" || strings.Contains(err.Error(), "hidden") {
				t.Fatalf("unsafe invalid result: %v", err)
			}
		})
	}
}
