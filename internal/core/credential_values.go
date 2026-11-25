package core

import "strings"

// ContainsCredential reports whether a retained value contains a nonempty credential.
func ContainsCredential(credentials []string, values ...string) bool {
	for _, credential := range credentials {
		for _, value := range values {
			if credential != "" && strings.Contains(value, credential) {
				return true
			}
		}
	}
	return false
}
