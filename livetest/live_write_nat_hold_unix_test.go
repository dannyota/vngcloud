//go:build livewrite && (darwin || linux)

package livetest_test

import (
	"os"
	"syscall"
)

func natHoldOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}
