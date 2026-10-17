package main

import (
	"testing"

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
