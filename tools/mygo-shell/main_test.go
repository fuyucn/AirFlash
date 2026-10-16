package main

import (
	"testing"

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
