package cli

import (
	"strings"
	"unicode"
)

// renameTable overrides the mechanical kebab() conversion for specific Go
// identifiers where it is ambiguous or wrong: a capital run with nothing
// after it (VPCID) can't be split without more information, and a field
// meant to read as a search term (billing's cost-explorer Query) reads
// better under a different flag name entirely. It is consulted by both the
// flag-name derivation (an Input field name) and the operation-name check
// (an SDK method name); any name absent from it uses kebab(name) unchanged.
var renameTable = map[string]string{
	"VPCID": "vpc-id",
	"Query": "search",
}

// flagNameFor returns the flag name for an Input field's Go name.
func flagNameFor(fieldName string) string {
	if override, ok := renameTable[fieldName]; ok {
		return override
	}
	return kebab(fieldName)
}

// checkOpName returns an error unless opName is either kebab(methodName) or
// the rename table's override for methodName.
func checkOpName(methodName, opName string) error {
	if kebab(methodName) == opName {
		return nil
	}
	if override, ok := renameTable[methodName]; ok && override == opName {
		return nil
	}
	return newUsageError("operation %q: name does not match kebab(%q) = %q and is not in the rename table",
		opName, methodName, kebab(methodName))
}

// kebab converts a Go exported identifier to kebab-case. A run of capital
// letters followed by lowercase letters gives its last capital to the next
// word (VirtualIPAddressID -> virtual-ip-address-id, ListSSHKeys ->
// list-ssh-keys, ListOSImages -> list-os-images), except when the
// lowercase letters that follow are exactly a single trailing "s": that is
// the plural of the acronym, not a new word, so the whole run stays
// together (ListVPCs -> list-vpcs, ListWANIPs -> list-wanips).
func kebab(name string) string {
	words := splitWords(name)
	for i, w := range words {
		words[i] = strings.ToLower(w)
	}
	return strings.Join(words, "-")
}

func splitWords(name string) []string {
	runes := []rune(name)
	n := len(runes)
	var words []string

	for i := 0; i < n; {
		if !unicode.IsUpper(runes[i]) {
			j := i + 1
			for j < n && !unicode.IsUpper(runes[j]) {
				j++
			}
			words = append(words, string(runes[i:j]))
			i = j
			continue
		}

		// runes[i:j] is a run of consecutive capital letters.
		j := i + 1
		for j < n && unicode.IsUpper(runes[j]) {
			j++
		}

		switch {
		case j == n || !unicode.IsLower(runes[j]):
			// The run reaches the end of the identifier, or is followed by
			// something other than a lowercase letter: keep it whole.
			words = append(words, string(runes[i:j]))
			i = j
		default:
			k := j
			for k < n && unicode.IsLower(runes[k]) {
				k++
			}
			lower := runes[j:k]
			switch {
			case len(lower) == 1 && lower[0] == 's':
				// The plural-of-acronym exception: keep the whole run plus
				// the trailing "s" together as one word.
				words = append(words, string(runes[i:k]))
				i = k
			case j == i+1:
				// A single capital starting an ordinary word.
				words = append(words, string(runes[i:j])+string(lower))
				i = k
			default:
				// A real word follows a longer acronym: its last capital
				// belongs to that word, so this run's word stops one short
				// of it; the next iteration (a length-1 run) picks up the
				// capital it left behind together with lower.
				words = append(words, string(runes[i:j-1]))
				i = j - 1
			}
		}
	}
	return words
}
