package autostart

// Command is what the startup entry runs.
//
// It is here, with no platform behind it, because it is the one part of autostart
// whose shape can be asserted without a registry: it has to be quoted, since an
// install path has a space in it, and carry --startup, so the shell knows it was
// launched by the machine rather than by hand.
func Command(exe string) string {
	return `"` + exe + `" --startup`
}
