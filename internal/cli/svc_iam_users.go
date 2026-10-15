package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/iam"
)

type iamCreateUserOutput struct {
	iam.CreateUserOutput
	SecretFile string
}

func iamCreateUserOp() Op[iam.Client] {
	return Op[iam.Client]{
		name: "create-user", methodName: "CreateUser", kind: kindWrite,
		beforeInput: beforeUserPasswordInput, guard: guardUserSecretPath,
		newInput:  func() any { return new(iam.CreateUserInput) },
		newOutput: func() any { return new(iamCreateUserOutput) },
		extraFlags: func(cmd *cobra.Command) {
			cmd.Flags().String("password-file", "", "private password input file, or - for stdin (choose this or --secret-file)")
			cmd.Flags().String("secret-file", "", "new private file for a generated password (choose this or --password-file)")
		},
		call: callIAMCreateUser,
	}
}

func callIAMCreateUser(cmd *cobra.Command, client *iam.Client, ctx context.Context, input any) (any, error) {
	in := *input.(*iam.CreateUserInput)
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
	out, err := client.CreateUser(ctx, &in)
	if out == nil {
		return nil, err
	}
	defer func() { out.Password = "" }()
	saved := ""
	if out.Password != "" {
		remains, writeErr := persistUserPassword(path, out.Password, userPasswordFileOps())
		if writeErr != nil {
			target, id := "username "+out.User.Username, "<id>"
			if out.CreatedID != "" {
				target, id = "user "+out.CreatedID, out.CreatedID
			}
			outcome := "create was accepted"
			if errors.Is(err, iam.ErrUserCreateUncertain) {
				outcome = "create outcome is uncertain and password ownership is unproven"
			}
			if errors.Is(err, iam.ErrCreateUnconfirmed) {
				outcome = "create was accepted but not confirmed"
			}
			remnant := ""
			if remains {
				remnant = "; a partial password file may remain"
			}
			return nil, secretFileFailedError{msg: fmt.Sprintf("%s; could not save generated password to %s for %s%s; no cloud cleanup was attempted; establish ownership and current state, then delete manually with vngcloud iam delete-user --user-id %s --yes", outcome, path, target, remnant, id)}
		}
		saved = path
	}
	if err != nil {
		state := "password was not saved (supplied-password mode)"
		if saved != "" {
			state = "generated password saved to " + saved
		}
		if errors.Is(err, iam.ErrUserCreateUncertain) {
			return nil, fmt.Errorf("%s; password ownership is unproven; reconcile with list-users --name and other administrators before trusting the password, resetting or deleting any candidate; do not retry: %w", state, err)
		}
		return nil, fmt.Errorf("%s: %w", state, err)
	}
	if saved == "" {
		result := *out
		result.Password = ""
		return &result, nil
	}
	result := &iamCreateUserOutput{CreateUserOutput: *out, SecretFile: saved}
	result.Password = ""
	return result, nil
}
