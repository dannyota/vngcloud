package core

func snapshotIAMUser(auth *IAMUserAuth) *IAMUserAuth {
	if auth == nil {
		return nil
	}
	totp := auth.TOTP
	if seed, ok := totp.(*SecretTOTP); ok && seed != nil {
		totp = &SecretTOTP{Secret: seed.Secret}
	}
	return &IAMUserAuth{RootEmail: auth.RootEmail, Username: auth.Username, Password: auth.Password, TOTP: totp, SigninBaseURL: auth.SigninBaseURL, TokenURL: auth.TokenURL, DashboardURI: auth.DashboardURI, HTTPClient: auth.HTTPClient}
}
