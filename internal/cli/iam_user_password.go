package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
)

const iamPasswordInputTimeout = 30 * time.Second

func beforeUserPasswordInput(cmd *cobra.Command) error {
	password, _ := cmd.Flags().GetString("password-file")
	secret, _ := cmd.Flags().GetString("secret-file")
	if (password == "") == (secret == "") {
		return newUsageError("exactly one of --password-file and --secret-file is required")
	}
	if password != "" && cmd.Flags().Changed("password-length") {
		return newUsageError("--password-length requires --secret-file")
	}
	raw, _ := cmd.Flags().GetString("cli-input-json")
	if password == "-" && len(raw) >= 7 && raw[:7] == "file://" {
		return newUsageError("--password-file - cannot share input with file:// JSON")
	}
	return nil
}

func guardUserSecretPath(cmd *cobra.Command, _ any) error {
	path, _ := cmd.Flags().GetString("secret-file")
	if path != "" {
		return checkSecretFilePath(path)
	}
	return nil
}

func readUserPassword(ctx context.Context, path string, stdin io.Reader, timeout time.Duration) (vngcloud.Secret, error) {
	if path != "-" {
		f, err := openRegularFileNonBlocking(path)
		if err != nil {
			return "", newUsageError("--password-file: cannot open input")
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return "", newUsageError("--password-file: input must be a regular file")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return "", newUsageError("--password-file: input must be private (mode 0600)")
		}
		return readUserPasswordBytes(ctx, f)
	}
	if isTerminal(stdin) {
		return "", newUsageError("--password-file - refuses a terminal")
	}
	inputCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Real stdin pipes use deadlines so cancellation also releases the read.
	// A generic Reader cannot be interrupted; its fallback owns and clears its
	// buffer when the read returns, even if the command has already canceled.
	if f, ok := stdin.(*os.File); ok {
		deadline, _ := inputCtx.Deadline()
		if err := f.SetReadDeadline(deadline); err == nil {
			finished := make(chan struct{})
			stop := context.AfterFunc(inputCtx, func() { _ = f.SetReadDeadline(time.Now()); close(finished) })
			defer func() {
				if !stop() {
					<-finished
				}
				_ = f.SetReadDeadline(time.Time{})
			}()
			return readUserPasswordBytes(inputCtx, f)
		}
	}
	type result struct {
		secret vngcloud.Secret
		err    error
	}
	done := make(chan result)
	go func() {
		secret, err := readUserPasswordBytes(inputCtx, stdin)
		select {
		case done <- result{secret, err}:
		case <-inputCtx.Done():
		}
	}()
	select {
	case r := <-done:
		return r.secret, r.err
	case <-inputCtx.Done():
		return "", newUsageError("--password-file: input canceled or timed out")
	}
}

func readUserPasswordBytes(ctx context.Context, r io.Reader) (vngcloud.Secret, error) {
	if ctx.Err() != nil {
		return "", newUsageError("--password-file: input canceled or timed out")
	}
	data, err := io.ReadAll(io.LimitReader(r, 4097))
	defer clear(data)
	if err != nil || ctx.Err() != nil {
		return "", newUsageError("--password-file: input could not be read")
	}
	if len(data) > 4096 {
		return "", newUsageError("--password-file: input exceeds 4096 bytes")
	}
	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
		if len(data) > 0 && data[len(data)-1] == '\r' {
			data = data[:len(data)-1]
		}
	}
	if len(data) == 0 || !utf8.Valid(data) {
		return "", newUsageError("--password-file: input must be non-empty valid UTF-8")
	}
	for _, r := range string(data) {
		if unicode.IsControl(r) {
			return "", newUsageError("--password-file: input must not contain controls")
		}
	}
	return vngcloud.Secret(string(data)), nil
}

type iamPasswordFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type iamPasswordFileOps struct {
	open   func(string) (iamPasswordFile, error)
	remove func(string) error
}

func persistUserPassword(path string, password vngcloud.Secret, ops iamPasswordFileOps) (remains bool, err error) {
	f, err := ops.open(path)
	if err != nil {
		return false, errors.New("password file could not be created exclusively")
	}
	data := append([]byte(password.Reveal()), '\n')
	defer clear(data)
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return ops.remove(path) != nil, errors.New("password file persistence failed")
	}
	return false, nil
}

func userPasswordFileOps() iamPasswordFileOps {
	return iamPasswordFileOps{
		open:   func(path string) (iamPasswordFile, error) { return openSecretFileExclusive(path, secretFileMode) },
		remove: os.Remove,
	}
}
