package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/iam"
)

type iamResetUserPasswordOutput struct {
	iam.ResetUserPasswordOutput
	SecretFile string
}

// resetPasswordFileError preserves the reset outcome when persistence also
// fails. Its fixed message never retains file-operation error text.
type resetPasswordFileError struct {
	msg     string
	outcome error
}

func (e resetPasswordFileError) Error() string { return e.msg }
func (e resetPasswordFileError) Unwrap() error { return e.outcome }

func iamResetUserPasswordOp() Op[iam.Client] {
	return Op[iam.Client]{
		name: "reset-user-password", methodName: "ResetUserPassword", kind: kindWrite, destructive: true,
		beforeInput: beforeUserPasswordInput, guard: guardUserSecretPath,
		newInput:  func() any { return new(iam.ResetUserPasswordInput) },
		newOutput: func() any { return new(iamResetUserPasswordOutput) },
		extraFlags: func(cmd *cobra.Command) {
			cmd.Flags().String("password-file", "", "private password input file, or - for stdin (choose this or --secret-file)")
			cmd.Flags().String("secret-file", "", "new private file for a generated password (choose this or --password-file)")
		},
		call: func(cmd *cobra.Command, client *iam.Client, ctx context.Context, input any) (any, error) {
			return callIAMResetUserPassword(cmd, client, ctx, input, userPasswordFileOps())
		},
	}
}

func callIAMResetUserPassword(cmd *cobra.Command, client *iam.Client, ctx context.Context, input any, ops iamPasswordFileOps) (any, error) {
	in := *input.(*iam.ResetUserPasswordInput)
	passwordPath, _ := cmd.Flags().GetString("password-file")
	path, _ := cmd.Flags().GetString("secret-file")
	if passwordPath != "" {
		if in.PasswordLength != 0 {
			return nil, newUsageError("PasswordLength requires --secret-file")
		}
		password, err := readUserPassword(ctx, passwordPath, cmd.InOrStdin(), iamPasswordInputTimeout)
		if err != nil {
			return nil, err
		}
		in.Password = password
	}
	defer func() { in.Password = "" }()
	out, err := client.ResetUserPassword(ctx, &in)
	if out == nil {
		return nil, err
	}
	defer func() { out.Password = "" }()
	saved := ""
	if out.Password != "" {
		remains, writeErr := persistUserPassword(path, out.Password, ops)
		if writeErr != nil {
			outcome := "reset was accepted"
			switch {
			case errors.Is(err, iam.ErrUserWriteUncertain):
				outcome = "reset outcome is uncertain; the previous password may be unusable"
			case errors.Is(err, iam.ErrNotSettled):
				outcome = "reset was accepted but response handling did not settle"
			}
			remnant := ""
			if remains {
				remnant = "; a partial password file may remain"
			}
			return nil, resetPasswordFileError{
				msg:     fmt.Sprintf("%s; could not save generated password to %s for user %s%s; no further request was sent; coordinate a deliberate new reset with vngcloud iam reset-user-password --user-id %s --secret-file <fresh-private-path> --yes", outcome, path, in.UserID, remnant, in.UserID),
				outcome: err,
			}
		}
		saved = path
	}
	if err != nil {
		state := "password was not saved (supplied-password mode)"
		if saved != "" {
			state = "generated password saved to " + saved
		}
		return nil, fmt.Errorf("%s; do not retry automatically; coordinate recovery with the user and other administrators: %w", state, err)
	}
	if saved == "" {
		return &struct{}{}, nil
	}
	return &iamResetUserPasswordOutput{SecretFile: saved}, nil
}
