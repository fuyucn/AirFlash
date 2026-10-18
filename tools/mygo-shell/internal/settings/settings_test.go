package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fuyucn/airflash/tools/mygo-shell/internal/equalizer"
)

func TestDefaultsTargetFiveHundredMilliseconds(t *testing.T) {
	// 500 ms is the value that made a first-generation HomePod play smoothly.
	if got := Default().LatencyMs; got != 500 {
		t.Fatalf("default latency = %d, want 500", got)
	}
}

func TestLightweightDropsTheTrendAndMotion(t *testing.T) {
	s := Default()
	s.Mode = ModeLightweight
	s.ShowTrend = true

	got := s.normalized()
	if got.ShowTrend {
		t.Error("lightweight mode must not keep the trend chart")
	}
}

func TestLightweightRedrawsLessOftenThanStandard(t *testing.T) {
	standard := Default()
	light := Default()
	light.Mode = ModeLightweight

	if light.RedrawIntervalMs() <= standard.RedrawIntervalMs() {
		t.Errorf("lightweight redraws every %d ms, standard every %d ms; lightweight must be less often",
			light.RedrawIntervalMs(), standard.RedrawIntervalMs())
	}
}

func TestNormalizeClampsToEngineRanges(t *testing.T) {
	cases := []struct {
		in   Settings
		want Settings
	}{
		{Settings{LatencyMs: 0}, Settings{LatencyMs: minLatencyMs}},
		{Settings{LatencyMs: 99999}, Settings{LatencyMs: maxLatencyMs}},
		// The desktop's Real-time mode asks for 120 ms, and it has to survive
		// the round trip through settings: the floor here used to be 200, which
		// silently raised it and made the desktop's most aggressive mode
		// unreachable from the shell.
		{Settings{LatencyMs: 120}, Settings{LatencyMs: 120}},
		{Settings{Gain: -1}, Settings{Gain: 0}},
		{Settings{Gain: 4}, Settings{Gain: 1}},
		{Settings{SampleRate: 12345}, Settings{SampleRate: 44100}},
		{Settings{SampleRate: 48000}, Settings{SampleRate: 48000}},
	}
	for _, c := range cases {
		got := c.in.normalized()
		if c.want.LatencyMs != 0 && got.LatencyMs != c.want.LatencyMs {
			t.Errorf("latency %d -> %d, want %d", c.in.LatencyMs, got.LatencyMs, c.want.LatencyMs)
		}
		if c.want.Gain != 0 && got.Gain != c.want.Gain {
			t.Errorf("gain %v -> %v, want %v", c.in.Gain, got.Gain, c.want.Gain)
		}
		if c.want.SampleRate != 0 && got.SampleRate != c.want.SampleRate {
			t.Errorf("rate %d -> %d, want %d", c.in.SampleRate, got.SampleRate, c.want.SampleRate)
		}
	}
}

func TestUnknownModeFallsBackToStandard(t *testing.T) {
	s := Settings{Mode: "turbo"}
	if got := s.normalized().Mode; got != ModeStandard {
		t.Errorf("mode %q -> %q, want %q", "turbo", got, ModeStandard)
	}
}

func TestStoreRoundTripsAndSurvivesABrokenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shell.json")

	st := Open(path)
	st.Update(func(s *Settings) {
		s.LatencyMs = 750
		s.Mode = ModeLightweight
	})

	again := Open(path)
	cur := again.Current()
	if cur.LatencyMs != 750 {
		t.Errorf("reloaded latency = %d, want 750", cur.LatencyMs)
	}
	if cur.Mode != ModeLightweight {
		t.Errorf("reloaded mode = %q, want %q", cur.Mode, ModeLightweight)
	}

	// A corrupt file must fall back to the defaults rather than fail to start.
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := Open(path)
	if broken.Current().LatencyMs != Default().LatencyMs {
		t.Errorf("corrupt file gave latency %d, want the default %d",
			broken.Current().LatencyMs, Default().LatencyMs)
	}
}

func TestAutoNeverGoesBelowTheFloorTheUserSet(t *testing.T) {
	// Auto means "you decide", not "ignore what I said". The setting is the
	// fastest the user is willing to accept.
	s := Default()
	s.LatencyAuto = true
	s.LatencyMs = 500

	if got := s.EffectiveLatencyMs(300); got != 500 {
		t.Errorf("target = %d, want the floor 500: Auto must not go faster than the user asked", got)
	}
}

func TestAutoUsesTheAdaptedTargetWhenItIsHigher(t *testing.T) {
	s := Default()
	s.LatencyAuto = true
	s.LatencyMs = 500

	if got := s.EffectiveLatencyMs(800); got != 800 {
		t.Errorf("target = %d, want the adapted 800", got)
	}
}

func TestAutoFallsBackToTheSettingBeforeAnySession(t *testing.T) {
	s := Default()
	s.LatencyAuto = true
	s.LatencyMs = 500

	if got := s.EffectiveLatencyMs(0); got != 500 {
		t.Errorf("target = %d, want the setting 500 when nothing has been learned yet", got)
	}
}

func TestWithAutoOffTheSettingIsUsedExactly(t *testing.T) {
	s := Default()
	s.LatencyAuto = false
	s.LatencyMs = 750

	if got := s.EffectiveLatencyMs(1200); got != 750 {
		t.Errorf("target = %d, want exactly the setting 750 when Auto is off", got)
	}
}

func TestAutoIsOnByDefault(t *testing.T) {
	// The point of the change: the user should not have to pick a number.
	if !Default().LatencyAuto {
		t.Error("Auto should be on by default")
	}
}

func TestEveryNamedLatencyIsWithinWhatTheEngineAccepts(t *testing.T) {
	// A target outside the engine's range is a start that fails, or a silent
	// clamp the user never sees.
	for _, mode := range LatencyChoices() {
		ms := NamedLatencyMs(mode)
		if ms < minLatencyMs || ms > maxLatencyMs {
			t.Errorf("%s asks for %d ms, outside the engine's %d..%d", mode, ms, minLatencyMs, maxLatencyMs)
		}
	}
}

func TestTheNamedLatenciesAreTheDesktopHosts(t *testing.T) {
	// Real-time, normal and buffered are the desktop's own three, with its own
	// numbers. If the two hosts disagree about what "buffered" means, a user
	// who tries both gets two different results from the same word.
	want := map[LatencyMode]int{
		LatencyRealtime: 120,
		LatencyNormal:   200,
		LatencyBuffered: 500,
	}
	for mode, ms := range want {
		if got := NamedLatencyMs(mode); got != ms {
			t.Errorf("%s = %d ms, want the desktop's %d", mode, got, ms)
		}
	}
}

func TestAReceiverOverridesOnlyWhatItSets(t *testing.T) {
	// A receiver with one field set inherits the rest. This is the whole reason
	// the fields are pointers: a HomePod that wants 500 ms while everything else
	// uses the global 200 needs one field, not a full copy.
	s := Default()
	s.LatencyMs = 200
	s.Gain = 1.0
	five := 500
	s.PerReceiver = map[string]ReceiverOptions{
		"homepod": {LatencyMs: &five},
	}

	if got := s.LatencyFor("homepod"); got != 500 {
		t.Errorf("homepod latency = %d, want its own 500", got)
	}
	if got := s.LatencyFor("something-else"); got != 200 {
		t.Errorf("another receiver latency = %d, want the global 200", got)
	}
	// Nothing else was set, so the rest is the global.
	if got := s.GainFor("homepod"); got != 1.0 {
		t.Errorf("homepod gain = %v, want the global 1.0", got)
	}
}

func TestAReceiverWithNothingSetUsesEverythingGlobal(t *testing.T) {
	s := Default()
	s.LatencyMs = 750
	off := false
	s.PerReceiver = map[string]ReceiverOptions{
		// Present but empty: still the global for every field.
		"tv": {Hidden: &off},
	}
	if got := s.LatencyFor("tv"); got != 750 {
		t.Errorf("latency = %d, want the global 750", got)
	}
}

func TestHiddenKeepsAReceiverWithoutForgettingIt(t *testing.T) {
	// Hidden is the one setting that is not a value to inherit: it is a fact
	// about one receiver, so it only ever comes from that receiver.
	s := Default()
	hidden := true
	s.PerReceiver = map[string]ReceiverOptions{"tv": {Hidden: &hidden}}
	shown := false
	s.PerReceiver["other"] = ReceiverOptions{Hidden: &shown}

	if !s.HiddenFor("tv") {
		t.Error("a receiver set to hidden is shown")
	}
	if s.HiddenFor("other") {
		t.Error("a receiver set to shown is hidden")
	}
	if s.HiddenFor("never-seen") {
		t.Error("an unset receiver defaults to hidden")
	}
}

func TestAnIdentityWithSeparatorsMatchesOneWithout(t *testing.T) {
	// A HomePod's device id is a MAC, and MACs are written with colons, dashes
	// or nothing depending on who wrote them down. Treating "AA:BB:..." and
	// "aabb..." as different devices would leave a receiver with two rows in the
	// panel and two sets of settings.
	if !devicesMatch("42FB:06B5:10BD", "42FB06B510BD") {
		t.Error("a MAC written two ways did not match itself")
	}
	if devicesMatch("42FB06B510BD", "42FB06B510BE") {
		t.Error("two different MACs matched")
	}
}

func TestAnEndpointIsOnlyEqualToItself(t *testing.T) {
	// An endpoint identity is made of its address and port, which a MAC-style
	// normalisation would collapse into another device: "10.0.10.31:7000" must
	// not be reduced to a MAC-shaped fragment that matches something else.
	if devicesMatch("10.0.10.31:7000", "10.0.10.32:7000") {
		t.Error("two different endpoints matched")
	}
	if !devicesMatch("10.0.10.31:7000", "10.0.10.31:7000") {
		t.Error("an endpoint did not match itself")
	}
}

func TestAnAliasIsFoundWhateverWayTheKeyWasWritten(t *testing.T) {
	s := Default()
	s.SetAlias("42FB:06B5:10BD", "Living Room")

	if got := s.AliasFor("42FB06B510BD"); got != "Living Room" {
		t.Errorf("alias for the same MAC written another way = %q, want Living Room", got)
	}
}

func TestAnAliasReplacesThePreviousOne(t *testing.T) {
	// One name for one receiver is the whole point: a second name must not leave
	// two entries that a reader has to choose between.
	s := Default()
	s.SetAlias("42FB06B510BD", "Living Room")
	s.SetAlias("42FB:06B5:10BD", "Study")

	if got := s.AliasFor("42FB06B510BD"); got != "Study" {
		t.Errorf("alias = %q, want Study", got)
	}
	if len(s.ReceiverAliases) != 1 {
		t.Errorf("%d aliases stored, want 1", len(s.ReceiverAliases))
	}
}

func TestAnEmptyAliasRemovesTheName(t *testing.T) {
	s := Default()
	s.SetAlias("42FB06B510BD", "Living Room")
	s.SetAlias("42FB06B510BD", "")

	if got := s.AliasFor("42FB06B510BD"); got != "" {
		t.Errorf("alias = %q, want it removed", got)
	}
}

func TestAManualReceiverNeedsAUsableAddress(t *testing.T) {
	cases := []struct {
		name string
		in   ManualReceiver
	}{
		{"no address", ManualReceiver{Name: "Study", Port: 7000}},
		{"a hostname rather than an address", ManualReceiver{Host: "homepod.local", Port: 7000}},
		{"a port above the range", ManualReceiver{Host: "10.0.10.31", Port: 70000}},
	}
	for _, c := range cases {
		s := Default()
		if err := s.AddManualReceiver(c.in); err == nil {
			t.Errorf("%s was accepted", c.name)
		}
	}
}

func TestAManualReceiverIsRefusedForTheReasonItWasGiven(t *testing.T) {
	// Which refusal the user gets is the whole point of the message: "needs an
	// address" and "not an IP address" send them to fix different things. A test
	// that only asserts an error happened cannot tell a reordering of these
	// checks from a working one, which is how the checks were nearly merged.
	cases := []struct {
		name string
		in   ManualReceiver
		want string
	}{
		{
			name: "nothing typed at all",
			in:   ManualReceiver{},
			want: "a manual receiver needs an address",
		},
		{
			// A name is not an address, so the entry still has nothing to connect
			// to and the address message is the one that says so.
			name: "a name with no address",
			in:   ManualReceiver{Name: "Study", Port: 7000},
			want: "a manual receiver needs an address",
		},
		{
			name: "a hostname rather than an address",
			in:   ManualReceiver{Host: "homepod.local", Port: 7000},
			want: "a manual receiver's address is not an IP address",
		},
		{
			// An empty address with no name is the first guard, which asks for
			// the address itself rather than for a better-formed one.
			name: "an empty address and no name",
			in:   ManualReceiver{Host: "", Port: 7000},
			want: "a manual receiver needs an address",
		},
		{
			// An empty address is refused before it reaches the IP check: the id
			// is derived from it and an empty one derives nothing, so the address
			// message is what comes back whatever else was typed.
			name: "an empty address with a name",
			in:   ManualReceiver{Name: "Study", Host: "", Port: 7000},
			want: "a manual receiver needs an address",
		},
		{
			// An address that is there but is not one is the IP check.
			name: "a hostname with a name",
			in:   ManualReceiver{Name: "Study", Host: "homepod.local", Port: 7000},
			want: "a manual receiver's address is not an IP address",
		},
		{
			name: "a port above the range",
			in:   ManualReceiver{Host: "10.0.10.31", Port: 70000},
			want: "a manual receiver's port must be between 1 and 65535",
		},
		{
			name: "a port below the range",
			in:   ManualReceiver{Host: "10.0.10.31", Port: -1},
			want: "a manual receiver's port must be between 1 and 65535",
		},
	}
	for _, c := range cases {
		s := Default()
		err := s.AddManualReceiver(c.in)
		if err == nil {
			t.Errorf("%s was accepted", c.name)
			continue
		}
		if err.Error() != c.want {
			t.Errorf("%s: error = %q, want %q", c.name, err.Error(), c.want)
		}
		// A refused entry must not be stored: the list is what the panel reads.
		if len(s.ManualReceivers) != 0 {
			t.Errorf("%s: %d entries were stored anyway", c.name, len(s.ManualReceivers))
		}
	}
}

func TestTheSameAddressIsRefusedWithTheAddress(t *testing.T) {
	// The duplicate message names what is duplicated, which is the one thing
	// that tells the user why their second entry did not appear.
	s := Default()
	if err := s.AddManualReceiver(ManualReceiver{Host: "10.0.10.31", Port: 7000}); err != nil {
		t.Fatal(err)
	}
	err := s.AddManualReceiver(ManualReceiver{Host: "10.0.10.31", Port: 7000})
	if err == nil {
		t.Fatal("the same address was added twice")
	}
	if want := "10.0.10.31:7000 is already in the list"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestAManualReceiverIsAcceptedWithAUsableAddress(t *testing.T) {
	s := Default()
	if err := s.AddManualReceiver(ManualReceiver{Host: "10.0.10.31", Port: 7000}); err != nil {
		t.Fatalf("a usable manual receiver was refused: %v", err)
	}
	if len(s.ManualReceivers) != 1 {
		t.Fatalf("%d manual receivers, want 1", len(s.ManualReceivers))
	}
	// The id is derived, so the same device a browse reports and one a user typed
	// in are the same receiver as far as settings are concerned.
	if got := s.ManualReceiverAt(0).Id; got != "10.0.10.31" {
		t.Errorf("id = %q, want the address", got)
	}
	// A port that was not given defaults to the one AirPlay 2 uses.
	empty := Default()
	if err := empty.AddManualReceiver(ManualReceiver{Host: "10.0.10.31"}); err != nil {
		t.Fatalf("a receiver with no port was refused: %v", err)
	}
}

func TestTheSameAddressIsNotListedTwice(t *testing.T) {
	// Two entries for one address are two rows for one device, and the user
	// cannot tell which is which.
	s := Default()
	if err := s.AddManualReceiver(ManualReceiver{Host: "10.0.10.31", Port: 7000}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddManualReceiver(ManualReceiver{Host: "10.0.10.31", Port: 7000}); err == nil {
		t.Error("the same address was added twice")
	}
	// The same address on another port is another speaker and is allowed.
	if err := s.AddManualReceiver(ManualReceiver{Host: "10.0.10.31", Port: 7001}); err != nil {
		t.Errorf("the same address on another port was refused: %v", err)
	}
}

func TestADroppedManualReceiverLeavesTheOthers(t *testing.T) {
	s := Default()
	_ = s.AddManualReceiver(ManualReceiver{Host: "10.0.10.31", Port: 7000})
	_ = s.AddManualReceiver(ManualReceiver{Host: "10.0.10.32", Port: 7000})

	s.DropManualReceiver(0)
	if len(s.ManualReceivers) != 1 {
		t.Fatalf("%d receivers left, want 1", len(s.ManualReceivers))
	}
	if s.ManualReceivers[0].Host != "10.0.10.32" {
		t.Errorf("the wrong receiver was dropped: %s", s.ManualReceivers[0].Host)
	}
	// An index that is not there does nothing rather than panicking.
	s.DropManualReceiver(9)
	if len(s.ManualReceivers) != 1 {
		t.Errorf("%d receivers after a bad drop, want 1", len(s.ManualReceivers))
	}
}

func TestAManualReceiverWithNoUsableAddressIsDroppedOnLoad(t *testing.T) {
	// It came from a settings file, and one with no address is a row in the panel
	// that cannot be played to.
	s := Default()
	s.ManualReceivers = []ManualReceiver{
		{Id: "42FB06B510BD", Host: "10.0.10.31", Port: 7000},
		{Id: "", Host: "10.0.10.32", Port: 7000},
		{Id: "42FB06B510BE", Host: "10.0.10.33", Port: 0},
	}
	got := s.normalized()
	if len(got.ManualReceivers) != 1 {
		t.Fatalf("%d manual receivers survived, want 1", len(got.ManualReceivers))
	}
	if got.ManualReceivers[0].Host != "10.0.10.31" {
		t.Errorf("kept %s, want the one usable receiver", got.ManualReceivers[0].Host)
	}
}

func TestALaunchByHandIsVisibleByDefaultButStartsInTheTray(t *testing.T) {
	// A tray app that opens nothing on a hand launch gives the user no way to
	// know it is running. The tray panel is what opens, so a launch is visible
	// without being a window. The window is opt-in through this setting.
	s := Default()
	if s.OpenAtLaunch {
		t.Error("OpenAtLaunch should default to false: the tray panel is the launch surface")
	}
}

func TestTheSettingIsStoredAndReloaded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shell.json")
	st := Open(path)
	st.Update(func(s *Settings) { s.OpenAtLaunch = true })

	if !Open(path).Current().OpenAtLaunch {
		t.Error("OpenAtLaunch did not survive a reload")
	}
}

func TestEveryBoundedFieldIsBounded(t *testing.T) {
	// Three fields are clamped, and the rule for all three comes from the engine's
	// validation. A clamp applied to one and forgotten on another is the failure
	// this guards, so all three are driven past both ends here.
	s := Settings{
		LatencyMs:         -50,
		Gain:              4,
		ReconnectAttempts: 9999,
	}
	got := s.normalized()
	if got.LatencyMs < minLatencyMs || got.LatencyMs > maxLatencyMs {
		t.Errorf("latency = %d, outside %d..%d", got.LatencyMs, minLatencyMs, maxLatencyMs)
	}
	if got.Gain < 0 || got.Gain > 1 {
		t.Errorf("gain = %v, outside 0..1", got.Gain)
	}
	if got.ReconnectAttempts < 1 || got.ReconnectAttempts > 20 {
		t.Errorf("attempts = %d, outside 1..20", got.ReconnectAttempts)
	}

	// And the same at the other end, so a ceiling is not the only side bound.
	low := Settings{LatencyMs: 0, Gain: -1, ReconnectAttempts: 0}.normalized()
	if low.LatencyMs < minLatencyMs || low.Gain < 0 || low.ReconnectAttempts < 1 {
		t.Errorf("a value below every floor was not raised: %+v", low)
	}
}

func TestASettingThatCouldNotBeWrittenIsReported(t *testing.T) {
	// A setting the user made that did not reach the disk is the one failure
	// they cannot see: the control moved, the interface changed, and nothing
	// says the next launch will not have it. A path that cannot be created
	// makes the write fail, which is what a full or read-only disk does.
	store := Open(filepath.Join(t.TempDir(), "settings.json"))
	if store.WriteErr() != nil {
		t.Fatalf("a fresh store reports %v, want no error", store.WriteErr())
	}
	store.Update(func(s *Settings) { s.LatencyMs = 250 })
	if err := store.WriteErr(); err != nil {
		t.Errorf("a good write reported %v", err)
	}

	// The same directory used as the file's path: the write cannot land.
	blocked := t.TempDir()
	bad := Open(blocked)
	bad.Update(func(s *Settings) { s.LatencyMs = 300 })
	if bad.WriteErr() == nil {
		t.Error("a write that could not land reported no error, so the user is told nothing")
	}
	// The setting is still in effect for this run: a failed write loses the
	// setting for the next launch, not this one.
	if got := bad.Current().LatencyMs; got != 300 {
		t.Errorf("latency = %d, want 300 while the app runs", got)
	}
}

func TestAManualReceiverKeepsTheIdItWasGiven(t *testing.T) {
	// The id is only derived when the entry did not bring one. One that did -
	// an entry written by another version, or an id that is not the address -
	// must survive: overwriting it would make the alias and the per-receiver
	// settings point at a key that no longer exists.
	s := Default()
	if err := s.AddManualReceiver(ManualReceiver{Id: "custom-id", Host: "10.0.10.31", Port: 7000}); err != nil {
		t.Fatalf("an entry with its own id was refused: %v", err)
	}
	if got := s.ManualReceivers[0].Id; got != "custom-id" {
		t.Errorf("id = %q, want the one given", got)
	}
}

func TestABlankExplicitIdIsKeptAsItWasGiven(t *testing.T) {
	// An id that was given is used as it came, so a blank one is stored blank -
	// which is what this records. It is a guard that only ever sees the derived
	// id, so a blank explicit id is the one thing it cannot catch.
	s := Default()
	err := s.AddManualReceiver(ManualReceiver{Id: "   ", Host: "10.0.10.31", Port: 7000})
	if err != nil {
		t.Fatalf("an entry with a blank id was refused: %v", err)
	}
	if got := s.ManualReceivers[0].Id; got != "   " {
		t.Errorf("id = %q, want it kept as given", got)
	}
}

func TestASuccessfulWriteClearsAnEarlierFailure(t *testing.T) {
	// writeErr was set when a write failed and never cleared, so one bad write -
	// a full disk, a settings file a sync tool held open - put a red warning on
	// the settings page that never went away, even though every change after it
	// reached the disk. The warning has to describe the last write.
	dir := t.TempDir()
	// A path under a file rather than a directory: MkdirAll fails, so the write
	// fails and leaves nothing behind that would make the next one succeed.
	blocker := filepath.Join(dir, "notadir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	failing := filepath.Join(blocker, "settings.json")

	// The same store, written twice: once to a path that cannot be written and
	// once to one that can. A Store keeps its path, so the path is set directly
	// rather than through Open - which is what makes this the same store.
	s := &Store{path: failing, cur: Default()}
	s.Update(func(s *Settings) { s.Gain = 0.5 })
	if s.WriteErr() == nil {
		t.Skip("this platform allowed a write under a file")
	}

	s.path = filepath.Join(dir, "settings.json")
	s.Update(func(s *Settings) { s.Gain = 0.3 })
	if s.WriteErr() != nil {
		t.Errorf("a successful write left the earlier failure in place: %v", s.WriteErr())
	}
	if got := s.Current().Gain; got != 0.3 {
		t.Errorf("gain = %v, want the later write to have been applied", got)
	}
}

func TestAFailedWriteLeavesTheLastGoodFileIntact(t *testing.T) {
	// Update writes with os.WriteFile, which truncates the file before writing.
	// Anything that stops the write half way leaves a truncated JSON document,
	// and Open cannot parse that - so every setting the user chose is gone on
	// the next launch, with nothing said.
	//
	// The property that protects the user is not that a write always succeeds;
	// it is that a write which fails leaves the previous, complete file readable.
	// That is what writing to a temporary file and renaming it gives.
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// A first write that works, and is read back.
	s := Open(path)
	s.Update(func(s *Settings) { s.LatencyMs = 500 })
	if got := Open(path).Current().LatencyMs; got != 500 {
		t.Fatalf("the first write did not reach the disk: %d", got)
	}

	// Now make the next write fail part way. A directory where the temporary
	// file would go means the rename cannot happen; the point under test is what
	// the settings file itself holds afterwards.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.path = filepath.Join(dir, "sub", "settings.json")
	s.Update(func(s *Settings) { s.LatencyMs = 120 })

	// The real file - the one Open reads - must still hold the last good value.
	if raw, err := os.ReadFile(path); err != nil {
		t.Fatalf("the last good settings file is gone: %v", err)
	} else {
		var probe Settings
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Fatalf("the last good settings file is no longer readable: %v", err)
		}
		if probe.LatencyMs != 500 {
			t.Errorf("the last good file holds %d ms, want the 500 ms that was saved", probe.LatencyMs)
		}
	}
}

func TestAWriteRefusesToReplaceADirectoryWithAFile(t *testing.T) {
	// The atomic write removes the old file before renaming onto it, because
	// Windows will not rename onto an existing file. Removing whatever is there
	// is not the same as removing a file: on a path that is a directory, the
	// rename then succeeds and the directory is gone, replaced by the settings
	// file. A user who had something at that path loses it, and nothing says so.
	blocked := t.TempDir()
	s := Open(blocked)
	s.Update(func(s *Settings) { s.LatencyMs = 300 })

	if s.WriteErr() == nil {
		t.Error("a write over a directory reported success: the directory would be replaced")
	}
	st, err := os.Stat(blocked)
	if err != nil {
		t.Fatalf("the directory is gone: %v", err)
	}
	if !st.IsDir() {
		t.Error("the directory was replaced by a file")
	}
}

func TestCurrentHandsOutACopyTheCallerCannotCorrupt(t *testing.T) {
	// Current returns the Settings value, so its slices and maps still point at
	// the store's own data. Every call site reads and none writes, so nothing is
	// broken today - but a caller that changed one would edit the store from
	// outside its lock, with no write to disk and no test failing. A value that
	// cannot be edited is the only version of this that stays safe.
	s := Open(filepath.Join(t.TempDir(), "settings.json"))
	s.Update(func(s *Settings) {
		s.ManualReceivers = []ManualReceiver{{Id: "a", Host: "{{IP_gxfw5vpu}}", Port: 7000}}
		s.ReceiverAliases = map[string]string{"a": "Living Room"}
		s.PerReceiver = map[string]ReceiverOptions{"a": {Volume: ptr(80)}}
		s.Sessions = []Session{{TargetMs: 500}}
	})

	got := s.Current()

	// Mutating what Current returned must not reach the store.
	got.ManualReceivers[0].Id = "changed"
	got.ManualReceivers[0].Port = 1
	got.ReceiverAliases["a"] = "changed"
	got.PerReceiver["a"] = ReceiverOptions{}
	got.Sessions[0].TargetMs = 999
	got.Sessions = append(got.Sessions, Session{TargetMs: 1})

	after := s.Current()
	if after.ManualReceivers[0].Id != "a" {
		t.Errorf("a caller's edit reached the store: receiver id = %q", after.ManualReceivers[0].Id)
	}
	if after.ManualReceivers[0].Port != 7000 {
		t.Errorf("a caller's edit reached the store: port = %d", after.ManualReceivers[0].Port)
	}
	if after.ReceiverAliases["a"] != "Living Room" {
		t.Errorf("a caller's edit reached the store: alias = %q", after.ReceiverAliases["a"])
	}
	if after.PerReceiver["a"].Volume == nil || *after.PerReceiver["a"].Volume != 80 {
		t.Error("a caller's edit reached the store: per-receiver volume")
	}
	if after.Sessions[0].TargetMs != 500 {
		t.Errorf("a caller's edit reached the store: session target = %d", after.Sessions[0].TargetMs)
	}
	if len(after.Sessions) != 1 {
		t.Errorf("a caller's append reached the store: %d sessions", len(after.Sessions))
	}
}

func ptr[T any](v T) *T { return &v }

// Every reference-typed field of Settings must be copied by clone, or Current
// hands out something the caller can edit in place. A hand-written list would
// stop being true the moment a field is added - which is when it matters, since
// the new field would be shared and nothing would say so. Reflection walks the
// struct instead, and fills every reference field at once through a JSON round
// trip so each has something in it.
func TestEveryReferenceFieldIsCloned(t *testing.T) {
	// A document with a value in every reference field the type has.
	raw := []byte(`{
	  "manual_receivers":[{"id":"a","host":"10.0.10.31","port":7000}],
	  "receiver_aliases":{"a":"Living Room"},
	  "receivers":{"a":{"volume":80}},
	  "sessions":[{"target_ms":500}]
	}`)
	var full Settings
	if err := json.Unmarshal(raw, &full); err != nil {
		t.Fatal(err)
	}

	ty := reflect.TypeOf(Settings{})
	checked := 0
	for i := 0; i < ty.NumField(); i++ {
		switch ty.Field(i).Type.Kind() {
		case reflect.Slice, reflect.Map:
		default:
			continue
		}
		name := ty.Field(i).Name
		checked++
		clone := full.clone()
		originalStore := full
		original := reflect.ValueOf(&originalStore).Elem().Field(i)
		cloned := reflect.ValueOf(&clone).Elem().Field(i)
		switch ty.Field(i).Type.Kind() {
		case reflect.Slice:
			if original.Len() == 0 {
				t.Fatalf("%s has no value to test the copy with", name)
			}
			// Grow the original; a shared clone grows with it.
			original.Set(reflect.AppendSlice(original,
				reflect.MakeSlice(original.Type(), 1, 1)))
			if cloned.Len() != original.Len()-1 {
				t.Errorf("%s is shared with the copy Current returns", name)
			}
		case reflect.Map:
			if original.Len() == 0 {
				t.Fatalf("%s has no value to test the copy with", name)
			}
			n := cloned.Len()
			kv := reflect.New(original.Type().Key()).Elem()
			vv := reflect.New(original.Type().Elem()).Elem()
			original.SetMapIndex(kv, vv)
			if cloned.Len() != n {
				t.Errorf("%s is shared with the copy Current returns", name)
			}
		}
	}
	if checked < 4 {
		t.Fatalf("checked only %d reference fields; the walk is not reading the struct", checked)
	}
}

func TestAPanickingCallbackDoesNotLockTheStoreForever(t *testing.T) {
	// Update releases its lock with an explicit Unlock rather than a defer, so
	// a callback that panics - a setting the shell cannot handle, a nil map
	// write - leaves the store locked for good. Every read and every later write
	// then blocks, and the app is dead: nothing can be changed and nothing can be
	// read, with no error to show for it.
	s := Open(filepath.Join(t.TempDir(), "settings.json"))

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("the panic reached the caller: %v", r)
			}
		}()
		s.Update(func(*Settings) { panic("this callback cannot finish") })
	}()

	// The store must still be usable.
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Update(func(cur *Settings) { cur.Gain = 0.5 })
	}()
	select {
	case <-done:
		if got := s.Current().Gain; got != 0.5 {
			t.Errorf("gain = %v, want the later write to have landed", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the store is locked forever after a panicking callback")
	}
}

func TestAnOutOfRangeEqualizerIsClampedOnLoad(t *testing.T) {
	// normalized checks the band slice's length and nothing else, so a settings
	// file with a gain of 999 or a preamp of 100 was sent to the engine as it
	// stood. The engine validates -12..=12 and refuses the whole equalizer, so
	// the curve the user sees is not the curve being heard - and a hand-edited
	// file, or one from a build with a different limit, is enough to do it.
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	raw := `{"equalizer":{"enabled":true,"preamp_db":100,
	  "band_gains_db":[999,0,0,0,0,0,0,0,0,-999]}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	eq := Open(path).Current().Equalizer
	if eq.PreampDB != equalizer.LimitDB {
		t.Errorf("preamp = %v, want it clamped to %v", eq.PreampDB, float64(equalizer.LimitDB))
	}
	if eq.BandGainsDB[0] != equalizer.LimitDB {
		t.Errorf("band 0 = %v, want %v", eq.BandGainsDB[0], float64(equalizer.LimitDB))
	}
	if eq.BandGainsDB[9] != -equalizer.LimitDB {
		t.Errorf("band 9 = %v, want %v", eq.BandGainsDB[9], float64(-equalizer.LimitDB))
	}
}
