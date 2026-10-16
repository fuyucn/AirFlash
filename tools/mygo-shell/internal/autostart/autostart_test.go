package autostart

import (
	"strings"
	"testing"
)

func TestTheStartupEntryCarriesTheStartupFlag(t *testing.T) {
	// Without it the shell cannot tell a launch at login from one by hand, and
	// would open the panel at every sign-in.
	cmd := Command(`C:\Program Files\AirFlash\AirFlash.exe`)
	if !strings.Contains(cmd, "--startup") {
		t.Errorf("command = %q, want it to carry --startup", cmd)
	}
}

func TestTheStartupEntryQuotesThePath(t *testing.T) {
	// The default install path has a space in it. Unquoted, the shell would be
	// launched with "C:\Program" as its executable and the rest as arguments,
	// which does nothing.
	cmd := Command(`C:\Program Files\AirFlash\AirFlash.exe`)
	if !strings.HasPrefix(cmd, `"C:\`) {
		t.Errorf("command = %q, want the path to open with a quote", cmd)
	}
	// The quote closes after the last path segment, not after the whole string.
	if !strings.Contains(cmd, `AirFlash.exe" --startup`) {
		t.Errorf("command = %q, want the path quoted and the flag outside it", cmd)
	}
}
