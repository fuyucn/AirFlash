//go:build !windows

package engine

import "syscall"

// Nothing to hide on the other platforms: a child inherits the parent's
// terminal, and a shell running from one is running in it by choice.
func consoleAttr() *syscall.SysProcAttr { return nil }
