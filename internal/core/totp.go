package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type TOTPProvider interface {
	GetCode(ctx context.Context) (string, error)
}

type TOTPFunc func(ctx context.Context) (string, error)

func (f TOTPFunc) GetCode(ctx context.Context) (string, error) {
	return f(ctx)
}

type SecretTOTP struct {
	Secret string
}

func (s *SecretTOTP) GetCode(context.Context) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(
		normalizeTOTPSecret(s.Secret),
	)
	if err != nil {
		return "", fmt.Errorf("decode TOTP secret: %w", err)
	}

	now := time.Now().Unix()
	if now < 0 {
		return "", errors.New("generate TOTP code: invalid time")
	}
	counter := uint64(now / 30)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(buf)
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", code%1_000_000), nil
}

func normalizeTOTPSecret(secret string) string {
	secret = strings.TrimSpace(secret)
	if strings.HasPrefix(strings.ToLower(secret), "otpauth://") {
		if u, err := url.Parse(secret); err == nil {
			if value := u.Query().Get("secret"); value != "" {
				secret = value
			}
		}
	}
	replacer := strings.NewReplacer(" ", "", "-", "")
	secret = replacer.Replace(secret)
	return strings.ToUpper(strings.TrimRight(secret, "="))
}
