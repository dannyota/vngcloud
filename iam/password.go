package iam

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"unicode"
	"unicode/utf8"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

const passwordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789@"

func passwordLength(n int) (int, error) {
	if n == 0 {
		n = 32
	}
	if n < 8 || n > 50 {
		return 0, fmt.Errorf("%w: password length must be between 8 and 50", core.ErrInvalidInput)
	}
	return n, nil
}

func checkPassword(value string) error {
	if !utf8.ValidString(value) || len(value) > 4096 {
		return fmt.Errorf("%w: password must be valid UTF-8 and at most 4096 bytes", core.ErrInvalidInput)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: password must not contain controls", core.ErrInvalidInput)
		}
	}
	return nil
}

func passwordClasses(p []byte) bool {
	var mask byte
	for _, b := range p {
		switch {
		case b >= 'A' && b <= 'Z':
			mask |= 1
		case b >= 'a' && b <= 'z':
			mask |= 2
		case b >= '0' && b <= '9':
			mask |= 4
		case b == '@':
			mask |= 8
		}
	}
	return mask == 15
}

// generatePassword uses a private reader seam. Rejection preserves the uniform
// distribution over strings that satisfy all four classes.
func generatePassword(ctx context.Context, length int, entropy io.Reader) ([]byte, error) {
	n, err := passwordLength(length)
	if err != nil {
		return nil, err
	}
	candidate := make([]byte, n)
	defer clear(candidate)
	var draw [1]byte
	defer clear(draw[:])
	consumed := 0
	for range 256 {
		for i := 0; i < n; {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if consumed == 64<<10 {
				return nil, errors.New("iam: password entropy budget exhausted")
			}
			consumed++
			if _, err := io.ReadFull(entropy, draw[:]); err != nil {
				return nil, errors.New("iam: password entropy unavailable")
			}
			if draw[0] >= 252 {
				continue
			}
			candidate[i] = passwordAlphabet[int(draw[0])%len(passwordAlphabet)]
			i++
		}
		if passwordClasses(candidate) {
			return append([]byte(nil), candidate...), nil
		}
	}
	return nil, errors.New("iam: password candidate budget exhausted")
}

func createPassword(ctx context.Context, in *CreateUserInput) (vngcloud.Secret, error) {
	if in.Password != "" {
		if in.PasswordLength != 0 {
			return "", fmt.Errorf("%w: PasswordLength requires generation", core.ErrInvalidInput)
		}
		return "", checkPassword(in.Password.Reveal())
	}
	p, err := generatePassword(ctx, in.PasswordLength, rand.Reader)
	if err != nil {
		return "", err
	}
	defer clear(p)
	return vngcloud.Secret(string(p)), nil
}
