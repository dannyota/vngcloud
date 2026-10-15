package cli

import (
	"context"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/iam"
)

func iamEnableUserMFAOp() Op[iam.Client] {
	op := Write("enable-user-mfa", (*iam.Client).EnableUserMFA, Destructive())
	// SDK partial outcomes retain state for library callers; CLI errors print none.
	op.call = func(_ *cobra.Command, client *iam.Client, ctx context.Context, in any) (any, error) {
		out, err := client.EnableUserMFA(ctx, in.(*iam.EnableUserMFAInput))
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	return op
}

func iamDisableUserMFAOp() Op[iam.Client] {
	op := Write("disable-user-mfa", (*iam.Client).DisableUserMFA, Destructive())
	op.call = func(_ *cobra.Command, client *iam.Client, ctx context.Context, in any) (any, error) {
		out, err := client.DisableUserMFA(ctx, in.(*iam.DisableUserMFAInput))
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	return op
}
