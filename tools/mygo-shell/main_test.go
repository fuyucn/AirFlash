package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
