//go:build windows

package engine

import "syscall"

func consoleAttr() *syscall.SysProcAttr {
	// HideWindow is CREATE_NO_WINDOW under the name the standard library gives
	// it: the child is started with no console, which is what stops the engine's
	// console flashing open every time a stream begins. The C# host sets the
	// same flag for the same reason.
	return &syscall.SysProcAttr{HideWindow: true}
}
