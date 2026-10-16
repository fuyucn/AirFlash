package ui

import "runtime"

// isWindows reports the platform at run time.
//
// The panel draws its own surface only where the platform does not: a Windows
// flyout is rounded and outlined by the system, a macOS popover gets its own
// material through Vibrancy, and on Linux nothing does, so the panel draws a
// border.
func isWindows() bool { return runtime.GOOS == "windows" }
