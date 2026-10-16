//go:build !windows

package main

import (
	"os"
	"strconv"
	"strings"
)

// processMemory falls back to RSS on non-Windows hosts, so the app still
// builds and runs off Windows (the reported numbers are Windows-only).
func processMemory() (int64, int64) {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, 0
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0, 0
	}
	pages, _ := strconv.ParseInt(f[1], 10, 64)
	return pages * int64(os.Getpagesize()), 0
}
