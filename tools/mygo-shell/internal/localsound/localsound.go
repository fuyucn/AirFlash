// Package localsound silences and restores the PC's own speakers while a stream
// runs.
//
// The desktop host calls this "mute while streaming", and it does it by default:
// the audio is being sent to the HomePod, so hearing it from the laptop's
// speakers as well is the same voice twice, one copy a few hundred milliseconds
// behind the other.
//
// The interface is deliberately tiny, because everything it can be asked to do
// on Windows has to go through COM and none of that is testable away from the
// platform. What is testable is the bookkeeping - which endpoints were silenced,
// and what they were before - and that lives here.
package localsound

import "errors"

// Endpoint is one audio endpoint: where a sound can be rendered to or captured
// from.
type Endpoint struct {
	// ID is the endpoint's own identifier, which is stable across a reboot and
	// is what a setting is stored against. An adapter's name is not: a device
	// unplugged and replugged gets another one.
	ID string
	// Name is what the user calls it, as Windows names it.
	Name string
	// Capture says which direction the endpoint carries sound. A render
	// endpoint is a speaker; a capture one is a microphone or a loopback
	// device.
	Capture bool
}

// Endpoints are the live endpoints the shell could capture from, render to, or
// silence.
//
// Render endpoints only, in the order Windows lists them. A capture endpoint is
// not offered for silencing because muting a microphone does not stop the user
// hearing the stream - it changes what is recorded.
func Endpoints() []Endpoint { return endpoints() }

// Mute silences one endpoint, remembering what it was so Restore can put it
// back. It is idempotent: silence what is already silent, and the state
// remembered is the state it was first found in.
//
// Remembering rather than setting a fixed value matters because the user's own
// speakers were not necessarily on: silencing something that was already muted
// and then "restoring" it by unmuting would be the app changing a setting the
// user made.
//
// An empty id is refused here, before it reaches a platform call: the default
// render device is what "" would be read as, and on a machine with several
// speakers that is a different device from the one the caller named.
func Mute(id string) error {
	if id == "" {
		return errors.New("no audio endpoint was named")
	}
	return muteEndpoint(id)
}

// Restore returns every endpoint this silenced to the state it was in when the
// silence began, and forgets them.
//
// An endpoint that has gone cannot be restored, and a silence that outlives its
// device is the app's problem rather than the user's: the entry is dropped so a
// later Restore cannot "restore" a device that never comes back.
func Restore() error { return restore() }
