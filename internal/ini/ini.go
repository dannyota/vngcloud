// Package ini parses the AWS-style INI files vngcloud.LoadConfig reads:
// sections, "key = value" lines, and full-line "#" or ";" comments. It adds
// no dependency and does no value interpretation beyond trimming.
package ini

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// File is a parsed INI file: section name to its key/value pairs. Keys are
// lowercased; values keep their original case, inner spaces, and any "="
// signs after the first.
type File map[string]map[string]string

// Section returns the named section's keys, and whether the section exists.
func (f File) Section(name string) (map[string]string, bool) {
	section, ok := f[name]
	return section, ok
}

// bom is the UTF-8 encoding of U+FEFF, spelled as bytes so the source file
// itself never contains a literal byte order mark.
var bom = string([]byte{0xEF, 0xBB, 0xBF})

// Parse reads src as an INI file. name identifies the source in an error
// message; it never appears with any line content, so an error cannot repeat
// a sensitive value from a credentials file.
func Parse(name string, src io.Reader) (File, error) {
	file := make(File)
	scanner := bufio.NewScanner(src)

	section := ""
	haveSection := false
	lineNum := 0
	firstLine := true

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if firstLine {
			line = strings.TrimPrefix(line, bom)
			firstLine = false
		}
		trimmed := strings.TrimSpace(line)

		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";"):
			continue
		case strings.HasPrefix(trimmed, "["):
			if !strings.HasSuffix(trimmed, "]") {
				return nil, lineError(name, lineNum, "unclosed section header")
			}
			section = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			haveSection = true
			if _, ok := file[section]; !ok {
				file[section] = make(map[string]string)
			}
		default:
			idx := strings.Index(line, "=")
			if idx < 0 {
				return nil, lineError(name, lineNum, "line has no '='")
			}
			if !haveSection {
				return nil, lineError(name, lineNum, "key before any section")
			}
			key := strings.ToLower(strings.TrimSpace(line[:idx]))
			if key == "" {
				return nil, lineError(name, lineNum, "empty key")
			}
			file[section][key] = strings.TrimSpace(line[idx+1:])
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return file, nil
}

func lineError(name string, line int, reason string) error {
	return fmt.Errorf("%s:%d: %s", name, line, reason)
}
