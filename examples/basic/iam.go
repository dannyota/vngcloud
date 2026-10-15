package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/iam"
)

func showIAM(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	client := iam.New(cfg)
	users, err := client.ListUsers(ctx, &iam.ListUsersInput{Size: 1000})
	if err != nil {
		recordAccount(outputs, "iam/user", "IAM users", []iam.User(nil), err)
		return
	}
	recordAccount(outputs, "iam/user", "IAM users", users.Items, nil)
	if len(users.Items) == 0 {
		return
	}
	first := users.Items[0]
	if first.ID != "" {
		detail, getErr := client.GetUser(ctx, &iam.GetUserInput{UserID: first.ID})
		var user iam.UserDetail
		if detail != nil {
			user = detail.User
		}
		recordAccountOne(outputs, "iam/user_detail", "IAM user detail", user, getErr)
	}
	if first.Username != "" {
		filtered, listErr := client.ListUsers(ctx, &iam.ListUsersInput{Name: first.Username, Size: 1000})
		var rows []iam.User
		if filtered != nil {
			rows = filtered.Items
		}
		recordAccount(outputs, "iam/user_filtered", "IAM filtered users", rows, listErr)
	}
}
