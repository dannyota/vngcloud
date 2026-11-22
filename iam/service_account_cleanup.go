package iam

type serviceAccountCleanup struct {
	id          string
	credentials []string
}

// CleanupInput returns the attributable create ID and a private copy of the
// operation's credentials for guarded deletion. Unproven create IDs stay empty.
func (out *CreateServiceAccountOutput) CleanupInput() *DeleteServiceAccountInput {
	if out == nil || out.cleanup == nil {
		return &DeleteServiceAccountInput{}
	}
	copied := *out.cleanup
	copied.credentials = append([]string(nil), copied.credentials...)
	return &DeleteServiceAccountInput{ServiceAccountID: copied.id, cleanup: &copied}
}

func newServiceAccountCleanup(id string, opts userReadOptions) *serviceAccountCleanup {
	credentials := append([]string(nil), opts.Redact...)
	if opts.SentCredentials != nil {
		credentials = append(credentials, (*opts.SentCredentials)...)
	}
	return &serviceAccountCleanup{id: id, credentials: credentials}
}
