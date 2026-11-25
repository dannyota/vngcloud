//go:build livewrite && !darwin && !linux

package livetest_test

import "os"

func natHoldOwned(os.FileInfo) bool { return false }
