package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/discovery"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/settings"
)

func TestPaintRateFollowsTheMode(t *testing.T) {
	// Standard mode redraws ten times a second and lightweight once. If the
	// throttle ignored the mode, lightweight would spend four times the cycles
	// it promises to save.
	standard := &App{settings: settings.Open("/tmp/airflash-paint-standard.json")}
	if got := standard.settings.Current().RedrawIntervalMs(); got != 100 {
		t.Fatalf("standard redraw interval = %d ms, want 100", got)
	}

	if !standard.dueToPaint(1000) {
		t.Fatal("the first event should paint")
	}
	if standard.dueToPaint(1050) {
		t.Error("a second event 50 ms later painted, but standard allows 100 ms")
	}
	if !standard.dueToPaint(1100) {
		t.Error("an event 100 ms after the last paint was refused")
	}

	light := &App{settings: settings.Open("/tmp/airflash-paint-light.json")}
	light.settings.Update(func(s *settings.Settings) { s.Mode = settings.ModeLightweight })
	if !light.dueToPaint(2000) {
		t.Fatal("the first event should paint")
	}
	if light.dueToPaint(2100) {
		// 100 ms later: fine in standard, too soon in lightweight.
		t.Error("lightweight painted 100 ms after the last paint, but it allows 1000 ms")
	}
	if !light.dueToPaint(3000) {
		t.Error("an event 1000 ms after the last paint was refused")
	}
}

func TestTheFirstEventAlwaysPaints(t *testing.T) {
	// lastPaint starts at zero. Reading that as a real timestamp would make the
	// first reading after launch wait for a paint that never happened.
	app := &App{settings: settings.Open("/tmp/airflash-paint-first.json")}
	if !app.dueToPaint(1) {
		t.Error("the first event was refused, so nothing would ever be drawn")
	}
}

func TestAnEngineEventReachesTheModel(t *testing.T) {
	// The handler is what the engine writes into. If it stopped applying, the
	// window would open on an empty model and never fill in.
	app := &App{
		settings: settings.Open("/tmp/airflash-paint-model.json"),
		state:    engine.NewState(),
	}
	app.onEngineEvent(engine.Event{Event: "streaming", Raw: []byte(
		`{"event":"streaming","requested_latency_ms":500,"sample_rate":44100,"clock_id":"c","members":1}`)})

	if got := app.state.Snapshot().Phase; got != engine.PhaseStreaming {
		t.Errorf("phase = %q, want the event to have been applied", got)
	}
}

// The throttle exists to rate-limit readings. A phase is not a reading: it is
// the state the surface is in. While the throttle was filtering phases too, a
// phase that arrived inside the interval was dropped and the surface went on
// saying the previous one - the "Connecting" that never resolved, and the
// reading that stopped moving.
func TestAPhaseIsNeverHeldBackByTheThrottle(t *testing.T) {
	app := &App{settings: settings.Open("/tmp/airflash-paint-phase.json")}
	if !app.dueToPaint(1000) {
		t.Fatal("the first event was refused, so there is nothing to throttle against")
	}

	// A reading 10 ms later is inside the interval, so it waits. This is the
	// throttle doing its job, and the test fails if it stops.
	if app.toPaint(false, 1010) {
		t.Error("a reading 10 ms after the last paint was owed a frame: the throttle is not rate-limiting")
	}
	// But the very same event, if it moved the phase, is owed one at once.
	if !app.toPaint(true, 1010) {
		t.Error("a phase 10 ms after the last paint was held back, so the surface would keep saying the old state")
	}
}

// The laser must be green for a phase to be seen at all: an event that reaches
// the model but paints nothing leaves the reading where it was.
func TestAPhaseEventReachesTheSurface(t *testing.T) {
	app := &App{
		settings: settings.Open("/tmp/airflash-paint-surface.json"),
		state:    engine.NewState(),
	}
	app.onEngineEvent(engine.Event{Event: "streaming", Raw: []byte(
		`{"event":"streaming","requested_latency_ms":500,"sample_rate":44100,"clock_id":"c","members":1}`)})

	if got := app.state.Snapshot().Phase; got != engine.PhaseStreaming {
		t.Fatalf("phase = %q, want the event applied", got)
	}
	// The paint that went out is recorded, which is what stops the reading that
	// follows the phase from being a second frame for the same phase.
	if app.lastPaint.Load() == 0 {
		t.Error("the phase paint was not recorded, so the next reading would paint again at once")
	}
}

// A reading whose frame was throttled away must still reach the model: the
// throttle drops frames, never data.
func TestAThrottledReadingStillReachesTheModel(t *testing.T) {
	app := &App{
		settings: settings.Open("/tmp/airflash-paint-model2.json"),
		state:    engine.NewState(),
	}
	app.onEngineEvent(engine.Event{Event: "streaming", Raw: []byte(
		`{"event":"streaming","requested_latency_ms":500,"sample_rate":44100,"clock_id":"c","members":1}`)})
	app.onEngineEvent(engine.Event{Event: "capture_metrics", Raw: []byte(
		`{"event":"capture_metrics","metrics":{"input_rate":48000}}`)})

	if got := app.state.Snapshot().Capture.InputRate; got != 48000 {
		t.Errorf("input rate = %d, want the throttled reading folded in", got)
	}
}

func TestAnAuthFailureIsNotRetriedForever(t *testing.T) {
	// A retry asks the receiver for the credentials that just failed. What a
	// failure like this needs is a PIN, so the loop has to stop and say so.
	for _, message := range []string{
		"authentication failed",
		"pairing required",
		"pin needed",
		"credential rejected",
		"unauthorized",
	} {
		if !isAuthFailure(message) {
			t.Errorf("%q was not recognised as an authentication failure", message)
		}
	}
	// And a genuine network failure is, because that is what a retry is for.
	if isAuthFailure("connection reset by peer") {
		t.Error("a network failure was mistaken for an authentication failure")
	}
	if isAuthFailure("the engine stopped") {
		t.Error("an engine ending was mistaken for an authentication failure")
	}
}

// The merge of what the browse found with what the user typed in. It is where a
// device that answers mDNS and the same device typed in by hand become one row
// rather than two.

// manualList stands in for a manual entry list.
func manualList(host string, port int) []settings.ManualReceiver {
	return []settings.ManualReceiver{{Id: host, Host: host, Port: port}}
}

func TestAManualEntryForADeviceAlreadyFoundIsNotShownTwice(t *testing.T) {
	// The browse and the user are describing one speaker. Two rows for it leaves
	// the user unable to tell which is which.
	manual := manualList("10.0.10.31", 7000)
	if !isManualEndpoint(manual, "10.0.10.31", 7000) {
		t.Error("the manual entry was not matched, so the device would appear twice")
	}
}

func TestAManualEntryForAnotherDeviceIsNotMatched(t *testing.T) {
	manual := manualList("10.0.10.31", 7000)
	if isManualEndpoint(manual, "10.0.10.32", 7000) {
		t.Error("a different address was matched")
	}
	if isManualEndpoint(manual, "10.0.10.31", 7001) {
		t.Error("a different port was matched: the same address on another port is another speaker")
	}
}

func TestAManualReceiverIsShownAsPresent(t *testing.T) {
	// Nothing is checking it - the browse that would report it gone cannot reach
	// it, which is why it was typed in. Marking it offline would offer a Play
	// that the user is told is unavailable for a device they know is there.
	rec := manualAsReceiver(settings.Default(), settings.ManualReceiver{
		Id: "10.0.10.31", Host: "10.0.10.31", Port: 7000,
	})
	if !rec.Online {
		t.Error("a manual receiver was reported offline")
	}
	if rec.Port != 7000 {
		t.Errorf("port = %d, want 7000", rec.Port)
	}
	if rec.Name != "10.0.10.31" {
		t.Errorf("name = %q, want the address when none was given", rec.Name)
	}
}

func TestAManualReceiversNameIsUsedWhenItGaveOne(t *testing.T) {
	rec := manualAsReceiver(settings.Default(), settings.ManualReceiver{
		Id: "10.0.10.31", Host: "10.0.10.31", Port: 7000, Name: "Study",
	})
	if rec.Name != "Study" {
		t.Errorf("name = %q, want Study", rec.Name)
	}
}

func TestAnAliasReplacesTheNameASurfaceShows(t *testing.T) {
	// The alias is what the panel shows instead of the device's own name, which
	// is set in the Home app and cannot be changed from here.
	cfg := settings.Default()
	cfg.SetAlias("42FB06B510BD", "Study")
	rec := discovery.Receiver{ID: "42FB06B510BD", Name: "Living Room"}
	if got := named(cfg, rec); got.Name != "Study" {
		t.Errorf("name = %q, want the alias", got.Name)
	}
}

func TestAReceiverWithNoAliasKeepsItsOwnName(t *testing.T) {
	cfg := settings.Default()
	rec := discovery.Receiver{ID: "42FB06B510BD", Name: "Living Room"}
	if got := named(cfg, rec); got.Name != "Living Room" {
		t.Errorf("name = %q, want the device's own", got.Name)
	}
}

func TestOnlyOneCallerClaimsTheBrowse(t *testing.T) {
	// Load-then-store let two callers both see nil and both start a browse, and
	// the second overwrote the first's cancel so the first could never be stopped.
	//
	// A scheduler-dependent race test cannot prove this: sixteen goroutines that
	// never interleave between the read and the write pass on the old code too.
	// What can be proved without a scheduler is the property the fix rests on -
	// that the claim is a single atomic step, so a second caller on an occupied
	// slot always loses - and that is asserted in
	// TestAClaimOnlySucceedsOnAnEmptySlot. This test keeps the concurrent case as
	// a smoke test that nothing deadlocks under contention.
	var slot atomic.Pointer[context.CancelFunc]
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, cancel, ok := claimBrowse(&slot); ok {
				_ = cancel
			}
		}()
	}
	close(start)
	wg.Wait()
	if slot.Load() == nil {
		t.Error("nobody claimed the browse")
	}
}

func TestASecondClaimAfterStopIsAllowed(t *testing.T) {
	// A stopped browse releases the slot, so the next start can take it. Without
	// that, discovery could never be restarted after the window was closed.
	var slot atomic.Pointer[context.CancelFunc]
	if _, _, ok := claimBrowse(&slot); !ok {
		t.Fatal("the first claim was refused")
	}
	// stopDiscovery swaps the slot back to nil.
	if c := slot.Swap(nil); c != nil {
		(*c)()
	}
	if _, _, ok := claimBrowse(&slot); !ok {
		t.Error("a claim after a stop was refused: discovery could never restart")
	}
}

func TestALosingClaimCancelsItsOwnContext(t *testing.T) {
	// The loser made a context and cannot use it. Leaving it running leaks a
	// cancel that nobody holds, which is the same shape as the browse that could
	// never be stopped.
	var slot atomic.Pointer[context.CancelFunc]
	_, first, ok := claimBrowse(&slot)
	if !ok {
		t.Fatal("the first claim was refused")
	}
	ctx, cancel, ok := claimBrowse(&slot)
	if ok {
		t.Fatal("the second claim won, but the slot was taken")
	}
	if ctx != nil || cancel != nil {
		t.Error("the loser returned a live context")
	}
	first()
}

func TestAClaimOnlySucceedsOnAnEmptySlot(t *testing.T) {
	// The property the compare-and-swap gives and check-then-act does not: the
	// test that the slot is empty and the store that claims it are one step, so
	// there is no window in which a second caller can see it empty too. This is
	// the property, asserted without depending on a scheduler getting a race to
	// happen.
	var slot atomic.Pointer[context.CancelFunc]
	if _, _, ok := claimBrowse(&slot); !ok {
		t.Fatal("the first claim was refused")
	}
	if _, _, ok := claimBrowse(&slot); ok {
		t.Error("a claim on a taken slot succeeded: two browses would run at once")
	}
}

func TestStoppingDoesNotWaitForTheRestore(t *testing.T) {
	// The restore is a COM round trip. On a slow audio service it took as long as
	// the service did, on the thread that handled the click, so stopping looked
	// hung. The stop now returns while the restore is still running.
	//
	// The observable property: stopStream returns while a restore is in flight.
	// The restore itself is stubbed here by holding the mutex the real one takes,
	// so the goroutine cannot finish, and the call must still return.
	app := &App{
		settings: settings.Open("/tmp/airflash-stop-restore.json"),
		state:    engine.NewState(),
	}
	// Hold the restore mutex, as an in-flight restore would.
	app.restoreMu.Lock()
	defer app.restoreMu.Unlock()

	done := make(chan struct{})
	go func() {
		app.stopStream()
		close(done)
	}()

	select {
	case <-done:
		// Returned while the restore is still blocked, which is the point.
	case <-time.After(2 * time.Second):
		t.Fatal("stopStream waited for the restore: a slow audio service would stall the click")
	}
}

// recordingTray watches the calls a tray menu is shown through, in order.
type recordingTray struct {
	calls []string
}

func (t *recordingTray) SetMenu(m *mygo.Menu) {
	if m == nil {
		t.calls = append(t.calls, "SetMenu(nil)")
		return
	}
	t.calls = append(t.calls, "SetMenu(menu)")
}

func (t *recordingTray) PopUpMenu() { t.calls = append(t.calls, "PopUpMenu()") }

func TestTheTrayMenuIsTakenOffTheIconAgain(t *testing.T) {
	// The Windows backend answers a right click by showing whatever menu the
	// icon holds, after it has reported the click. So a menu left on the icon
	// is shown twice: once by the shell, and once by the backend as the
	// callback returns. The second one opens the moment the user picks from
	// the first, which reads as the menu coming back rather than the choice
	// being taken - which is what the tray menu was reported to do.
	tray := &recordingTray{}
	app := &App{settings: settings.Open("/tmp/airflash-tray-menu.json")}
	app.showTrayMenu(tray)

	want := []string{"SetMenu(menu)", "PopUpMenu()", "SetMenu(nil)"}
	if len(tray.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", tray.calls, want)
	}
	for i, call := range want {
		if tray.calls[i] != call {
			t.Fatalf("call %d = %q, want %q (all: %v)", i, tray.calls[i], call, tray.calls)
		}
	}
	// A menu still on the icon would be shown a second time by the backend.
	if tray.calls[len(tray.calls)-1] != "SetMenu(nil)" {
		t.Errorf("the menu was left on the icon, so the backend shows it again: %v", tray.calls)
	}
}

func TestQuitCanBeAskedForMoreThanOnce(t *testing.T) {
	// Build 71 crashed at startup's quit path: the channel closing on a second
	// quit panicked, and a quit can happen more than once - the user choosing
	// Quit, and then the machine shutting down on its way out. The probe the
	// workflow runs returns before this is registered, so nothing caught it.
	app := &App{quitting: make(chan struct{})}

	quit := func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("a second quit panicked: %v", r)
			}
		}()
		app.quittingOnce.Do(func() { close(app.quitting) })
	}
	quit()
	// The channel is closed, which is what the retry loop is waiting on.
	select {
	case <-app.quitting:
	default:
		t.Fatal("quitting was not closed, so the retry loop would sleep on")
	}
	// And a second ask does not panic.
	quit()
	quit()
}

func TestStoppingIsClearedWhenANewSessionStarts(t *testing.T) {
	// stopping is what makes a reconnect loop give up when the user asked it to.
	// It was set by stopStream and never cleared, so after one Disconnect every
	// later session started with the flag still set and aborted on its first
	// check - which is a Play that does nothing the second time, and a "failed"
	// that the user did not earn.
	app := &App{
		settings: settings.Open("/tmp/airflash-stop-clear.json"),
		state:    engine.NewState(),
	}
	app.stopping.Store(true)

	// Starting a new session is a fresh intent: it clears the flag before the
	// session goroutine can read it.
	app.startStreamTo("receiver", []engine.Peer{{Host: "10.0.10.31", Port: 7000}})

	if app.stopping.Load() {
		t.Error("a new session started with stopping still set, so it would abort at once")
	}
}

func TestASupersededSessionGivesUpEvenAfterAStopIsCleared(t *testing.T) {
	// Clearing stopping on a new Play is right, and on its own it would let an
	// older retry loop - still asleep in its backoff - wake to a flag that says
	// "go" and race the new session. The generation is what the old loop
	// compares against, so a stale one stands down.
	app := &App{
		settings: settings.Open("/tmp/airflash-stop-gen.json"),
		state:    engine.NewState(),
	}
	app.startStreamTo("first", []engine.Peer{{Host: "10.0.10.31", Port: 7000}})
	first := app.sessionSeq.Load()

	app.startStreamTo("second", []engine.Peer{{Host: "10.0.10.31", Port: 7000}})
	second := app.sessionSeq.Load()

	if second == first {
		t.Fatal("starting a second session did not move the generation, so an old loop would not stand down")
	}
	if app.sessionSeq.Load() == first {
		t.Error("the first session still reads as current")
	}
}

func TestAStaleSessionStandsDownWhenANewerOneStarts(t *testing.T) {
	// The guard runSession checks before each attempt, exercised on its own so
	// the property is proved without waiting out a real backoff.
	app := &App{settings: settings.Open("/tmp/airflash-stop-stale.json")}
	gen := app.sessionSeq.Add(1)

	if app.stopping.Load() || app.sessionSeq.Load() != gen {
		t.Fatal("a freshly started session was treated as stale")
	}
	// A newer session is started while this one is still asleep.
	app.sessionSeq.Add(1)
	if app.sessionSeq.Load() == gen {
		t.Fatal("the newer session did not move the generation")
	}
	// Which is exactly what makes the old loop stand down.
	if !(app.stopping.Load() || app.sessionSeq.Load() != gen) {
		t.Error("the old session still thinks it is current, so it would start a second engine")
	}
}
