//go:build windows

package autostart

// Starting with the machine, on Windows.
//
// The desktop host does this with one value under
// HKCU\Software\Microsoft\Windows\CurrentVersion\Run, and this is the same
// thing: a per-user entry, so it needs no administrator, named after the app so
// a value left behind by an older install is the same key and is replaced
// rather than accumulated.

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// runPath is where the current user's startup entries live.
var runPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// name is what the entry is called. It is the app's own name, so an entry left
// behind by an older install is the same key and is replaced.
const name = "AirFlash"

// Set adds or removes the startup entry.
//
// The command carries --startup, which is how the shell tells itself it was
// launched by the machine rather than by a person: one launched by hand opens
// the panel, one launched at login stays quiet in the tray and connects to the
// last receiver if the settings ask for that.
func Set(enabled bool) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runPath, registry.SET_VALUE)
	if err != nil {
		// The caller reports this: autostart is something the user asked for, so
		// a silent failure would leave them wondering why it does not work.
		return err
	}
	defer key.Close()

	if !enabled {
		// Absent is the same as disabled, so a missing value is not an error.
		err := key.DeleteValue(name)
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return key.SetStringValue(name, Command(exe))
}

// Enabled reports whether the startup entry is there.
//
// The registry is read rather than the settings, because the two can disagree:
// a user can remove the entry by hand, and an earlier install can leave one that
// a later install does not know about. Only the registry says what is true.
func Enabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runPath, registry.QUERY_VALUE)
	if err != nil {
		return false, err
	}
	defer key.Close()

	if _, _, err := key.GetStringValue(name); err != nil {
		if err == registry.ErrNotExist {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
