package transport

import "slices"

func (r *Request) resetSentCredentials() {
	if r.SentCredential != nil {
		*r.SentCredential = ""
	}
	if r.SentCredentials == nil {
		r.SentCredentials = new([]string)
	} else {
		*r.SentCredentials = nil
	}
}

func (r Request) recordSentCredential(credential string) {
	if r.SentCredential != nil {
		*r.SentCredential = credential
	}
	if credential != "" && !slices.Contains(*r.SentCredentials, credential) {
		*r.SentCredentials = append(*r.SentCredentials, credential)
	}
}
