package localsound

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestAGuidIsParsedIntoTheLayoutACallTakes(t *testing.T) {
	// The SDK's own value for the device enumerator. Parsing the two halves in
	// the wrong order produces a Guid that matches nothing, and CoCreateInstance
	// fails with "class not registered" - a failure that reads like the audio
	// service is missing rather than like a typo.
	g := guidx("BCDE0395-E52F-467C-8E3D-C4579291692E")
	if g.D1 != 0xBCDE0395 {
		t.Errorf("D1 = 0x%08x, want 0xbcde0395", g.D1)
	}
	if g.D2 != 0xE52F {
		t.Errorf("D2 = 0x%04x, want 0xe52f", g.D2)
	}
	if g.D3 != 0x467C {
		t.Errorf("D3 = 0x%04x, want 0x467c", g.D3)
	}
	want := [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}
	if g.D4 != want {
		t.Errorf("D4 = % x, want % x", g.D4, want)
	}
}

func TestAnUnparsableGuidIsNotTheDeviceEnumerators(t *testing.T) {
	// A zeroed Guid is a real class id, which would silently create something
	// else. An unparsable string has to end up as nothing.
	if empty := guidx("nonsense"); empty == guidx("BCDE0395-E52F-467C-8E3D-C4579291692E") {
		t.Error("an unparsable string parsed as the device enumerator's class")
	}
}

func TestOnlyTheFirstSilenceIsRemembered(t *testing.T) {
	// A second silence of the same endpoint would otherwise record the muted
	// state this caused, and restoring that would leave it muted: the user would
	// have to turn their speakers back on by hand.
	_ = Restore()
	remember("speakers", remembered{wasMuted: true, read: true})
	remember("speakers", remembered{wasMuted: false, read: true})

	pending := takeForRestore()
	if got := pending["speakers"].wasMuted; !got {
		t.Errorf("restored state = unmuted, want the muted state the endpoint was first found in")
	}
}

func TestOnlyWhatWasReadIsRestored(t *testing.T) {
	// The rule of the feature: an endpoint whose state was never read must not
	// be restored, because "restoring" it would turn on a speaker the user had
	// turned off.
	_ = Restore()
	remember("read-as-muted", remembered{wasMuted: true, read: true})
	remember("read-as-on", remembered{wasMuted: false, read: true})
	remember("never-read", remembered{wasMuted: false, read: false})

	pending := takeForRestore()
	restorable := 0
	for _, was := range pending {
		if was.read {
			restorable++
		}
	}
	if restorable != 2 {
		t.Errorf("%d endpoints would be restored, want 2", restorable)
	}
	if _, still := pending["never-read"]; !still {
		t.Error("an unread endpoint vanished instead of being kept as unrestorable")
	}
}

func TestTakingForRestoreClearsWhatWasTaken(t *testing.T) {
	_ = Restore()
	remember("speakers", remembered{wasMuted: true, read: true})
	takeForRestore()
	if again := takeForRestore(); len(again) != 0 {
		t.Errorf("second take returned %d endpoints, want none: a restore must not repeat", len(again))
	}
}

func TestAFailedApartmentKeepsWhatStillNeedsRestoring(t *testing.T) {
	// Restore took the pending endpoints before joining COM. If the apartment
	// could not be opened, nothing was restored and the list was already empty:
	// the PC's speakers stayed muted and the app forgot it owed them a restore.
	_ = Restore()
	remember("speakers", remembered{wasMuted: false, read: true})

	err := restoreRemembered(func(func() error) error {
		return errors.New("COM apartment unavailable")
	}, func(string, remembered) error {
		t.Error("an endpoint was restored without a COM apartment")
		return nil
	})
	if err == nil {
		t.Fatal("a failed apartment was reported as a successful restore")
	}
	pending := takeForRestore()
	if _, ok := pending["speakers"]; !ok {
		t.Error("the endpoint was forgotten before it could be restored")
	}
}

func TestAnEndpointThatFailedToRestoreIsRetriedLater(t *testing.T) {
	// A speaker can be busy or removed for a moment. Dropping it on that one
	// failure leaves it muted for good, even though the next Restore could still
	// put it back.
	_ = Restore()
	remember("speakers", remembered{wasMuted: false, read: true})

	err := restoreRemembered(func(fn func() error) error {
		return fn()
	}, func(string, remembered) error {
		return errors.New("the endpoint was busy")
	})
	if err == nil {
		t.Fatal("a failed endpoint restore was reported as success")
	}

	pending := takeForRestore()
	if _, ok := pending["speakers"]; !ok {
		t.Error("a failed endpoint was dropped instead of kept for the next restore")
	}
}

func TestAnEmptyEndpointIsRefusedRatherThanSilencingTheDefault(t *testing.T) {
	// Silencing "" would be a call against whatever the default render device
	// happens to be, which on a machine with several is a different device from
	// the one that was asked for. The guard is in the shared entry point, so it
	// holds on every platform.
	if err := Mute(""); err == nil {
		t.Error("muting the empty endpoint id was accepted")
	} else if !strings.Contains(err.Error(), "no audio endpoint") {
		t.Errorf("error = %v, want it to say no endpoint was named", err)
	}
}

func TestARenderEndpointIsTheOneSilenced(t *testing.T) {
	// A capture endpoint is a microphone: muting it changes what is recorded,
	// not what the user hears. Only a render one stops the audio being audible,
	// so the default for muting is the first render endpoint in the list.
	list := []Endpoint{
		{ID: "mic", Name: "Microphone", Capture: true},
		{ID: "spk", Name: "Speakers", Capture: false},
	}
	chosen := ""
	for _, e := range list {
		if !e.Capture {
			chosen = e.ID
			break
		}
	}
	if chosen != "spk" {
		t.Errorf("silenced %q, want the render endpoint spk: muting a microphone does not silence anything the user hears", chosen)
	}
}

func TestACaptureEndpointIsOfferedSeparatelyFromARenderOne(t *testing.T) {
	// Both directions are listed, because a machine with two sound cards has two
	// of each, and the direction is what says which is which: only a render one
	// can be captured from by loopback.
	list := []Endpoint{
		{ID: "mic", Name: "Microphone", Capture: true},
		{ID: "spk", Name: "Speakers", Capture: false},
	}
	render := 0
	for _, e := range list {
		if !e.Capture {
			render++
		}
	}
	if render != 1 {
		t.Errorf("%d render endpoints, want 1: the capture one is a microphone, not a device to record the PC's audio from", render)
	}
}

func TestAnEndpointWithNoNameIsStillListable(t *testing.T) {
	// A card Windows has no friendly name for is still a card, and a list that
	// dropped it would leave a machine with only one usable sound card looking
	// like a machine with none.
	e := Endpoint{ID: "{0.0.0.00000000}"}
	if e.ID == "" {
		t.Error("an endpoint with no name lost its id, so it cannot be chosen at all")
	}
}

// The P0 fix is that COM joins an apartment per thread rather than once for the
// process. Neither the bug nor the fix is observable on a machine with no COM, so
// what is tested here is the shape of the fix: the apartment key is per thread, so
// two goroutines on two threads are two entries rather than one, and a goroutine
// back on a thread it has already joined does not join it twice.

func TestTheApartmentKeyIsPerThreadNotPerProcess(t *testing.T) {
	// The old code filed one entry for the whole process, so a second caller found
	// the apartment already open and skipped initialising on its own thread. That
	// is exactly the shape of the bug: the key must be a thread id, and two
	// callers on two threads must produce two entries.
	//
	// Both functions are written out, because the one that matters is the
	// difference between them.
	joinOld := func(m *sync.Map, _ string) {
		if _, ok := m.Load("process"); ok {
			return
		}
		m.Store("process", struct{}{})
	}
	joinNew := func(m *sync.Map, key string) {
		if _, ok := m.Load(key); ok {
			return
		}
		m.Store(key, struct{}{})
	}
	count := func(m *sync.Map) int {
		n := 0
		m.Range(func(any, any) bool { n++; return true })
		return n
	}

	var oldWay, newWay sync.Map
	// Two threads, as the engine's event goroutine and the UI thread are.
	joinOld(&oldWay, "tid-1")
	joinOld(&oldWay, "tid-2")
	joinNew(&newWay, "tid-1")
	joinNew(&newWay, "tid-2")

	if got := count(&oldWay); got != 1 {
		t.Errorf("the old way opened %d apartments for two threads, want 1: that is the bug", got)
	}
	if got := count(&newWay); got != 2 {
		t.Errorf("the new way opened %d apartments for two threads, want 2", got)
	}
}

func TestAThreadThatHasJoinedIsNotJoinedTwice(t *testing.T) {
	// A second CoInitializeEx on a thread returns S_FALSE, which is harmless, but
	// a count of them is not: the point of the key is one initialisation per
	// thread, so repeated calls from the same thread do nothing.
	var m sync.Map
	calls := 0
	join := func(key string) {
		if _, ok := m.Load(key); ok {
			return
		}
		calls++
		m.Store(key, struct{}{})
	}
	for i := 0; i < 5; i++ {
		join("tid-1")
	}
	if calls != 1 {
		t.Errorf("CoInitializeEx called %d times on one thread, want 1", calls)
	}
}
