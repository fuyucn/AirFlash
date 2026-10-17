//go:build !windows

package localsound

// What the silence does on the platforms that are not Windows.
//
// The shell targets Windows first, and this exists so the package builds
// everywhere. A Linux build cannot silence a render endpoint through anything
// the standard library reaches, and pretending to have done it would leave the
// user hearing both copies while the app claimed otherwise. So it does nothing
// and says so.

func endpoints() []Endpoint { return nil }

func muteEndpoint(string) error { return nil }

func restore() error { return nil }
