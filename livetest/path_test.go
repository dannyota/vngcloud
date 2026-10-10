//go:build live || livewrite

package livetest_test

import (
	"fmt"
	"os"
	"path/filepath"
)

// repoPath keeps live credentials, captures, and fixtures under the module root.
func repoPath(parts ...string) string {
	dir, err := os.Getwd()
	if err != nil {
		panic(fmt.Errorf("find module root: %w", err))
	}
	for {
		info, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil && !info.IsDir() {
			return filepath.Join(append([]string{dir}, parts...)...)
		}
		if err != nil && !os.IsNotExist(err) {
			panic(fmt.Errorf("find module root: %w", err))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("find module root: go.mod not found")
		}
		dir = parent
	}
}
