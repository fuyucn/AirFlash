//go:build !windows

package autostart

// Starting with the machine, on the platforms that are not Windows.
//
// This shell runs on Windows and Linux. Linux autostart is a .desktop file in
// ~/.config/autostart, which is a different mechanism from Windows' registry
// and is not written here. The setting is accepted and does nothing, rather
// than pretending to have worked.

// Set does nothing on a platform with no implementation.
func Set(enabled bool) error { return nil }

// Enabled reports that nothing is registered, because nothing can be.
func Enabled() (bool, error) { return false, nil }
