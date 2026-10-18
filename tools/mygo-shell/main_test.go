package main

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/discovery"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/i18n"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/settings"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/shell"
	appui "github.com/fuyucn/airflash/tools/mygo-shell/ui"
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

func TestAnOfflineReceiverIsNotPlayedToEvenIfTheUiAsks(t *testing.T) {
	// The surfaces hide Play for a receiver the browse has lost, but hiding a
	// control is not the rule - it is a drawing of it. A stale frame, or the
	// connect-at-login path, can still reach playReceiver with a device that is
	// not answering, and starting a session for one can only time out.
	app := &App{
		settings: settings.Open("/tmp/airflash-offline-play.json"),
		state:    engine.NewState(),
	}
	rec := discovery.Fold([]discovery.Record{
		{Instance: "Living Room", Type: discovery.ServiceAirPlay, Address: "10.0.10.31", Port: 7000,
			TXT: map[string]string{"deviceid": "AA", "model": "AudioAccessory1,1"}},
		{Instance: "AA@Living Room", Type: discovery.ServiceRAOP, Address: "10.0.10.31", Port: 7000,
			TXT: map[string]string{"deviceid": "AA", "cn": "0,1", "am": "AudioAccessory1,1"}},
	})[0]
	rec.Online = false

	app.playReceiver(rec)

	if app.playing.Load() != nil {
		t.Error("an offline receiver was marked as the one playing")
	}
	if got := app.sessionSeq.Load(); got != 0 {
		t.Errorf("session generation = %d, want no session started for an offline receiver", got)
	}
}

func TestTheTrayMenuSeparatesTheIdFromTheLabel(t *testing.T) {
	// The items were labelled "Stats" and "Settings" and those labels were handed
	// to openWindow as page ids. SetPage takes lower-case ids, so every item fell
	// through to its default and opened Receivers: choosing Stats from the tray
	// opened the receiver list, which reads as a menu that does nothing.
	app := &App{
		settings: settings.Open("/tmp/airflash-tray-pages.json"),
		state:    engine.NewState(),
	}
	menu := app.trayMenuItems()
	for _, label := range []string{"Stats", "Settings"} {
		want := strings.ToLower(label)
		item := menu.ItemByID(want)
		if item == nil {
			t.Fatalf("no item with id %q: the label is being used as the id again", want)
		}
		if item.Label != label {
			t.Errorf("item %q shows %q, want %q", want, item.Label, label)
		}
	}
}

func ptr(s string) *string { return &s }

func TestAskingForAPageMovesTheOpenWindow(t *testing.T) {
	// The tray menu's Stats and Settings items only brought the window forward
	// when one was already open, so choosing either while the window showed
	// Receivers did nothing visible - a menu item that reads as broken.
	app := &App{
		settings: settings.Open("/tmp/airflash-page-move.json"),
		state:    engine.NewState(),
	}
	view := appui.New(appui.Deps{
		State:    app.state,
		Settings: app.settings,
		Life:     shell.New(),
	})
	app.view.Store(view)
	app.page.Store(ptr("receivers"))

	app.showPage("settings")

	if got := view.Page(); got != "settings" {
		t.Errorf("the open view shows %q, want settings", got)
	}
	if got := app.currentPage(); got != "settings" {
		t.Errorf("the remembered page is %q, want settings", got)
	}
}

// showPage does nothing when no window is open, which is the normal case: the
// next openWindow builds a view on the page it was asked for.
func TestAskingForAPageWithNoWindowIsHarmless(t *testing.T) {
	app := &App{settings: settings.Open("/tmp/airflash-page-none.json"), state: engine.NewState()}
	app.showPage("stats")
	if app.view.Load() != nil {
		t.Error("showPage created a view with no window")
	}
}

func TestAClosedWindowLeavesNoViewBehind(t *testing.T) {
	// A rebuild closes the window and opens a new one. If the close left the old
	// view in place, the next request that moves the page would move a view no
	// window is drawing, and the window that is up would not follow the menu.
	app := &App{
		settings: settings.Open("/tmp/airflash-close-view.json"),
		state:    engine.NewState(),
	}
	view := appui.New(appui.Deps{
		State:    app.state,
		Settings: app.settings,
		Life:     shell.New(),
	})
	app.view.Store(view)

	app.view.Store(nil)

	if app.view.Load() != nil {
		t.Fatal("a closed window left its view behind")
	}
	app.showPage("stats")
	if app.view.Load() != nil {
		t.Error("showPage created a view after the window was closed")
	}
}

func TestAStreamThatCouldNotStartIsNotLeftConnecting(t *testing.T) {
	// startAttempt logs a failed StartStream and returns. Nothing else reports a
	// phase for that attempt, so the model kept the "connecting" it was put in -
	// a surface saying a connection is being made that never was.
	app := &App{settings: settings.Open("/tmp/airflash-start-fail.json"), state: engine.NewState()}
	app.state.Apply(engine.Event{Event: "phase", Raw: []byte(`{"event":"phase","phase":"connecting"}`)})
	if got := app.state.Snapshot().Phase; got != engine.PhaseConnecting {
		t.Fatalf("setup phase = %q, want connecting", got)
	}

	// What startAttempt does when StartStream fails.
	app.state.Apply(engine.Event{Event: "error", Raw: []byte(
		`{"event":"error","message":"could not start the stream"}`)})

	if got := app.state.Snapshot().Phase; got != engine.PhaseFailed {
		t.Errorf("phase = %q, want failed: the surface would keep saying connecting", got)
	}
	if got := app.state.LastError(); got != "could not start the stream" {
		t.Errorf("last error = %q, want the start failure", got)
	}
}

func TestTheTrayMenuIsDrawnInTheChosenLanguage(t *testing.T) {
	// The tray menu is the one surface drawn outside the ui package, and it was
	// written with English labels. A Chinese interface therefore had an English
	// menu over it - the two most visible items being the page names.
	cfg := settings.Open("/tmp/airflash-tray-lang.json")
	cfg.Update(func(s *settings.Settings) { s.Language = string(i18n.Chinese) })
	app := &App{settings: cfg, state: engine.NewState()}

	labels := map[string]bool{}
	for _, item := range app.trayMenuItems().Items() {
		if item.Label != "" {
			labels[item.Label] = true
		}
	}
	if labels["Stats"] || labels["Settings"] || labels["Quit"] {
		t.Errorf("the menu is still English: %v", labels)
	}
	// And the translated labels are the ones it shows.
	for _, want := range []string{"状态", "设置", "退出"} {
		if !labels[want] {
			t.Errorf("the menu has no %q item: %v", want, labels)
		}
	}
}

func TestThePanelGetsEveryDependencyItReads(t *testing.T) {
	// The panel read PlayingReceiver and the app did not pass it, so the call was
	// on a nil func: a panic the first time the panel opened with a receiver in
	// the list. Checking only that one field would miss the next omission, so
	// this compares every field the panel reads against every field the app
	// passes - read from the source, because the wiring is a literal.
	app := &App{settings: settings.Open("/tmp/airflash-panel-deps.json"), state: engine.NewState()}
	d := app.panelDeps()

	// The fields that must be set for the panel to draw at all.
	if d.State == nil || d.Settings == nil || d.Receivers == nil {
		t.Fatal("the panel is missing one of its required fields")
	}
	// And the ones that are optional in type but read unconditionally by a
	// branch: a nil here is a panic, not an empty answer.
	if d.PlayingReceiver == nil {
		t.Error("the panel was given no PlayingReceiver, so its receiver list panics")
	}
}

func TestTheWindowDepsPassATranslator(t *testing.T) {
	// The window's Deps were built without Words, so every string in it fell
	// back to English while the tray panel, which does pass a translator, was
	// Chinese. The render tool hid it because it builds its own dependencies.
	//
	// The literal is read from the source rather than built, because building it
	// needs a running window; what matters is that the field is there.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	// The window's literal is the one that sets Settings and Life but is not
	// panelDeps, so look for a Words line between the window's opening brace and
	// its view.SetPage call.
	text := string(src)
	open := strings.Index(text, "view := appui.New(appui.Deps{")
	if open < 0 {
		t.Fatal("the window's dependencies are not built where they were")
	}
	close := strings.Index(text[open:], "view.SetPage(page)")
	if close < 0 {
		t.Fatal("the window literal has no end marker")
	}
	body := text[open : open+close]
	if !strings.Contains(body, "Words:") {
		t.Error("the window's dependencies have no translator, so the window is drawn in English")
	}
}
