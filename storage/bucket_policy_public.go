package storage

import (
	"encoding/json"
	"strings"
)

// PolicyHasPublicPrincipal reports whether a bucket policy document grants
// anonymous access: some statement whose Effect is not "Deny" has a Principal
// that is a string containing "*", or an object whose "AWS" value is such a
// string or an array holding one. Any "*" counts, not only "*" alone, since
// how the server matches a wildcard inside a principal is unverified.
//
// It returns the error PutBucketPolicy would return for the same document, so
// a caller can check a document once. PutBucketPolicy does not ask for
// consent; a caller that should ask before opening a bucket to everyone calls
// this first.
func PolicyHasPublicPrincipal(policy string) (bool, error) {
	const op = "storage.PolicyHasPublicPrincipal"
	if err := checkPolicyDocument(op, policy); err != nil {
		return false, err
	}
	// checkPolicyDocument has shown that each decode below succeeds.
	members, err := decodeObject([]byte(strings.TrimSpace(policy)))
	if err != nil {
		return false, err
	}
	var statements []json.RawMessage
	if err := json.Unmarshal(members["Statement"], &statements); err != nil {
		return false, err
	}
	for _, raw := range statements {
		statement, err := decodeObject(raw)
		if err != nil {
			return false, err
		}
		var effect string
		if err := json.Unmarshal(statement["Effect"], &effect); err != nil {
			return false, err
		}
		if effect == "Deny" {
			continue
		}
		public, err := principalIsPublic(statement["Principal"])
		if err != nil {
			return false, err
		}
		if public {
			return true, nil
		}
	}
	return false, nil
}

func principalIsPublic(raw json.RawMessage) (bool, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.Contains(text, "*"), nil
	}
	members, err := decodeObject(raw)
	if err != nil {
		return false, err
	}
	aws := members["AWS"]
	if json.Unmarshal(aws, &text) == nil {
		return strings.Contains(text, "*"), nil
	}
	var list []string
	_ = json.Unmarshal(aws, &list) // a value of another type has no public entry
	for _, v := range list {
		if strings.Contains(v, "*") {
			return true, nil
		}
	}
	return false, nil
}
