package ui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/discovery"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/i18n"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/settings"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/shell"
)

// receiver builds one playable device, as discovery would fold it.
func receiver(id, name, address string) discovery.Receiver {
	return discovery.Fold([]discovery.Record{
		{Instance: name, Type: discovery.ServiceAirPlay, Address: address, Port: 7000,
			TXT: map[string]string{"deviceid": id, "model": "AudioAccessory1,1"}},
		{Instance: id + "@" + name, Type: discovery.ServiceRAOP, Address: address, Port: 7000,
			TXT: map[string]string{"deviceid": id, "cn": "0,1", "am": "AudioAccessory1,1"}},
	})[0]
}

// streamingState is a state that has started playing.
func streamingState() *engine.State {
	st := engine.NewState()
	st.Apply(engine.Event{Event: "streaming", Raw: []byte(
		`{"event":"streaming","requested_latency_ms":500,"sample_rate":44100,"clock_id":"c","members":1}`)})
	return st
}

// panelDeps builds the panel's dependencies around a fixed set of receivers.
func panelDeps(st *engine.State, recs []discovery.Receiver, playingID string) Deps {
	cfg := settings.Open("/tmp/airflash-panel-test.json")
	return Deps{
		State:     st,
		Settings:  cfg,
		Life:      shell.New(),
		Receivers: func() []discovery.Receiver { return recs },
		PlayingReceiver: func() *discovery.Receiver {
			for _, r := range recs {
				if r.ID == playingID {
					found := r
					return &found
				}
			}
			return nil
		},
	}
}

func TestOnlyThePlayingReceiverOffersStop(t *testing.T) {
	// The engine runs one session, and a session is one receiver. A Stop on
	// every row would be three controls for one session, and pressing the wrong
	// one would stop playback while the panel claimed it was starting it.
	living := receiver("AA", "Living Room", "10.0.10.31")
	studio := receiver("BB", "Studio", "10.0.10.44")

	panel := NewPanel(panelDeps(streamingState(), []discovery.Receiver{living, studio}, living.ID))
	tester := ui.NewTester(panel.View, panelWidth, 600)

	if !tester.HasText("Stop") {
		t.Fatal("the playing receiver offers no Stop")
	}
	if !tester.HasText("Play") {
		t.Error("the other receiver offers no Play: it is idle, so it can be started")
	}
	// One Stop, one Play: the row that is playing is the only one that stops.
	stops := 0
	for _, s := range tester.Texts() {
		if s == "Stop" {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("%d Stop controls, want 1: only the playing receiver can be stopped", stops)
	}
}

func TestIdleReceiversAllOfferPlay(t *testing.T) {
	// Nothing is playing, so every receiver is a way to start.
	recs := []discovery.Receiver{
		receiver("AA", "Living Room", "10.0.10.31"),
		receiver("BB", "Studio", "10.0.10.44"),
	}
	panel := NewPanel(panelDeps(engine.NewState(), recs, ""))
	tester := ui.NewTester(panel.View, panelWidth, 600)

	plays := 0
	for _, s := range tester.Texts() {
		if s == "Play" {
			plays++
		}
	}
	if plays != len(recs) {
		t.Errorf("%d Play controls, want %d: every idle receiver can be played to", plays, len(recs))
	}
	if tester.HasText("Stop") {
		t.Error("an idle panel offers Stop, but nothing is playing")
	}
}

func TestVolumeBelongsToThePlayingReceiver(t *testing.T) {
	// Device volume addresses the session, so it exists only where a session
	// does. A slider on an idle panel would move and do nothing.
	living := receiver("AA", "Living Room", "10.0.10.31")

	idle := NewPanel(panelDeps(engine.NewState(), []discovery.Receiver{living}, ""))
	if ui.NewTester(idle.View, panelWidth, 600).HasText("VOL") {
		t.Error("an idle panel shows a volume control, but there is no session to set")
	}

	playing := NewPanel(panelDeps(streamingState(), []discovery.Receiver{living}, living.ID))
	if !ui.NewTester(playing.View, panelWidth, 600).HasText("VOL") {
		t.Error("the playing receiver has no volume control")
	}
}

func TestPanelHeightGrowsWithReceivers(t *testing.T) {
	// The panel is a flyout, and a flyout needs its size before it opens. If
	// the height did not follow the content, the last receiver would be cut off.
	one := receiver("AA", "Living Room", "10.0.10.31")
	two := receiver("BB", "Studio", "10.0.10.44")

	short := NewPanel(panelDeps(engine.NewState(), []discovery.Receiver{one}, "")).Height()
	tall := NewPanel(panelDeps(engine.NewState(), []discovery.Receiver{one, two}, "")).Height()

	if tall <= short {
		t.Errorf("height with two receivers (%d) is not greater than with one (%d)", tall, short)
	}
}

func TestPanelHeightContainsEverythingItDraws(t *testing.T) {
	// The measured height has to hold the lowest thing the panel draws, or the
	// footer is clipped: the one control that is always needed.
	recs := []discovery.Receiver{
		receiver("AA", "Living Room", "10.0.10.31"),
		receiver("BB", "Studio", "10.0.10.44"),
	}
	panel := NewPanel(panelDeps(engine.NewState(), recs, ""))
	height := panel.Height()

	tester := ui.NewTester(panel.View, panelWidth, height)
	for _, s := range []string{"Stats", "Settings", "Quit"} {
		r, ok := tester.Find(s)
		if !ok {
			t.Errorf("%q is not drawn", s)
			continue
		}
		if bottom := r.Y + r.H; bottom > float32(height) {
			t.Errorf("%q ends at %.1f, past the panel's height of %d", s, bottom, height)
		}
	}
}

// offlineReceiver is a receiver that has stopped answering mDNS.
func offlineReceiver() discovery.Receiver {
	r := receiver("AA", "Living Room", "10.0.10.31")
	r.Online = false
	return r
}

func TestAnOfflineReceiverCannotBePlayedTo(t *testing.T) {
	// Offering Play on a speaker that has gone would time out. The panel says
	// it is offline instead, and offers nothing to press.
	rec := offlineReceiver()
	panel := NewPanel(panelDeps(engine.NewState(), []discovery.Receiver{rec}, ""))
	tester := ui.NewTester(panel.View, panelWidth, 600)

	if !tester.HasText("offline") {
		t.Error("an offline receiver is not marked offline")
	}
	for _, s := range tester.Texts() {
		if s == "Play" {
			t.Error("an offline receiver offers Play, which could only fail")
		}
	}
}

func TestAnOnlineReceiverOffersPlay(t *testing.T) {
	rec := receiver("AA", "Living Room", "10.0.10.31")
	panel := NewPanel(panelDeps(engine.NewState(), []discovery.Receiver{rec}, ""))
	tester := ui.NewTester(panel.View, panelWidth, 600)

	if !tester.HasText("Play") {
		t.Error("a reachable receiver offers no Play")
	}
	if tester.HasText("offline") {
		t.Error("a reachable receiver is marked offline")
	}
}

func TestAPlayingReceiverIsNotCalledOffline(t *testing.T) {
	// The receiver that is playing is connected by definition, whatever the
	// browse last said: a missed mDNS round while audio is flowing must not
	// turn the live row grey.
	rec := receiver("AA", "Living Room", "10.0.10.31")
	rec.Online = false
	panel := NewPanel(panelDeps(streamingState(), []discovery.Receiver{rec}, rec.ID))

	if !ui.NewTester(panel.View, panelWidth, 600).HasText("Stop") {
		t.Error("the playing receiver lost its Stop")
	}
}

// TestTheStatsActionActsOnThePlayingDevice is the bug the user's question
// exposed: the window's action started on Peers()[0], which is whatever
// discovery listed first. That is not necessarily what is playing, so pressing
// Stop in the window could stop a different device - or one that was never
// started at all - while the panel showed the real one still running.
func TestTheStatsActionActsOnThePlayingDevice(t *testing.T) {
	living := receiver("AA", "Living Room", "10.0.10.31")
	studio := receiver("BB", "Studio", "10.0.10.44")
	playing := living

	var stopped, played int
	var playedName string
	view := New(Deps{
		State:           streamingState(),
		Settings:        settings.Open("/tmp/airflash-stats-action.json"),
		Life:            shell.New(),
		Receivers:       func() []discovery.Receiver { return []discovery.Receiver{living, studio} },
		PlayingReceiver: func() *discovery.Receiver { return &playing },
		OnStop:          func() { stopped++ },
		OnPlay: func(r discovery.Receiver) {
			played++
			playedName = r.Name
		},
	})
	view.SetPage("stats")
	tester := ui.NewTester(view.View, 880, 640)

	if err := tester.Click("Stop"); err != nil {
		t.Fatal(err)
	}
	if stopped != 1 {
		t.Errorf("Stop was pressed %d times, want the action to fire once", stopped)
	}
	if played != 0 {
		t.Errorf("Stop also started playback (%d times), want it to only stop", played)
	}

	// Now idle: the action must start on the receiver the panel last played to,
	// not on the first one discovery happened to list.
	view2 := New(Deps{
		State:     engine.NewState(),
		Settings:  settings.Open("/tmp/airflash-stats-action.json"),
		Life:      shell.New(),
		Receivers: func() []discovery.Receiver { return []discovery.Receiver{studio, living} },
		// Discovery lists Studio first. Nothing is playing, and the shell
		// remembers Living Room as what the panel last played to: the app keeps
		// that choice after the session ends, which is what starting from the
		// window rather than from a fresh scan depends on.
		PlayingReceiver: func() *discovery.Receiver {
			lastPlayed := living
			return &lastPlayed
		},
		OnPlay: func(r discovery.Receiver) {
			played++
			playedName = r.Name
		},
	})
	view2.SetPage("stats")
	if err := ui.NewTester(view2.View, 880, 640).Click("Play"); err != nil {
		t.Fatal(err)
	}
	if played != 1 {
		t.Fatalf("Play fired %d times, want once", played)
	}
	if playedName != "Living Room" {
		t.Errorf("played %q, want the receiver the panel last used (%q), not the first listed",
			playedName, living.Name)
	}
}

func TestAReconnectSaysWhichAttemptItIsOn(t *testing.T) {
	// "connecting…" and "reconnecting 2/5" are different situations: the second
	// tells the user the app is still trying, and how many tries are left.
	st := streamingState()
	st.Apply(engine.Event{Event: "phase", Raw: []byte(`{"event":"phase","phase":"connecting"}`)})
	// No retry in progress: an ordinary connection.
	if got := phaseNote(i18n.For(i18n.English), st.Snapshot()); got != "connecting…" {
		t.Errorf("note = %q, want an ordinary connecting message", got)
	}

	// A retry under way, on the second of five.
	st2 := streamingState()
	st2.Apply(engine.Event{Event: "phase", Raw: []byte(`{"event":"phase","phase":"connecting"}`)})
	st2.SetReconnecting(2, 5)
	if got := phaseNote(i18n.For(i18n.English), st2.Snapshot()); got != "reconnecting 2/5" {
		t.Errorf("note = %q, want the attempt named: it is what says the app is still on it", got)
	}
}

func TestASilentPCIsSaidOutLoud(t *testing.T) {
	// While the PC captures nothing, the round trip stays perfect and the buffer
	// stays full, so every number on the panel says things are fine. The user
	// hears nothing, so the silence has to be stated on its own.
	living := receiver("AA", "Living Room", "10.0.10.31")

	st := streamingState()
	// Four seconds of nothing captured. The first report is the session opening
	// on a quiet PC rather than a second of new silence, so four reports are
	// three seconds of it - the same counting the engine does.
	for i := 0; i < 4; i++ {
		st.Apply(engine.Event{Event: "capture_metrics", Raw: []byte(
			`{"event":"capture_metrics","metrics":{"capture_frames":4410,
			 "last_audio_qpc_ns":5,"input_rate":44100}}`)})
	}

	panel := NewPanel(panelDeps(st, []discovery.Receiver{living}, living.ID))
	tester := ui.NewTester(panel.View, panelWidth, 600)

	found := ""
	for _, s := range tester.Texts() {
		if strings.Contains(s, "no sound") {
			found = s
		}
	}
	if found == "" {
		t.Fatal("a PC sending no sound is not reported, so every number says all is well")
	}
	if !strings.Contains(found, "3 s") {
		t.Errorf("message = %q, want it to say how long the silence has lasted", found)
	}
}

func TestAQuietPCBeforeAnythingIsMeasuredIsNot(t *testing.T) {
	// A session that has just opened has measured nothing. Calling that silence
	// would cry wolf on every fresh connection.
	living := receiver("AA", "Living Room", "10.0.10.31")
	panel := NewPanel(panelDeps(streamingState(), []discovery.Receiver{living}, living.ID))

	if ui.NewTester(panel.View, panelWidth, 600).HasText("no sound") {
		t.Error("a fresh session is reported as silent before anything was measured")
	}
}

// TestEveryInterfaceStringHasAChineseEntry is the guard against a translation
// that looks done and is not.
//
// The English keys are the literals in the source, so the list is written here
// rather than read back from the view: a view that had been half translated
// would not say so. What is asserted is that each one is a key in the table,
// because a missing key silently falls back to its English - which is exactly
// how four settings descriptions stayed English while the page looked
// translated.
func TestEveryInterfaceStringHasAChineseEntry(t *testing.T) {
	// Every string the interface shows, as it appears in the source.
	shown := []string{
		// Navigation and phases.
		"Stats", "Receivers", "Settings", "Idle", "Playing", "Stopped", "Failed",
		"Connecting", "connecting…", "no reading yet", "could not connect",
		// Receivers.
		"Looking for receivers…", "The PC and the HomePod have to be on the same network.",
		"offline", "pair incomplete", "Play", "Stop", "VOL",
		// Readings.
		"Target", "ms delay", "on target", "waiting for playback", "nothing measured yet",
		"slightly over target", "well over target",
		// The charts.
		"LATENCY OVER TIME", "GAPS", "VOLUME", "CAPTURE", "LOSSES", "LATENCY",
		"One mark per second of playback appears here. Nothing has been lost so far.",
		"The measured latency appears here once playback starts.",
		"%d of %d s lost audio", "none in the last %d s",
		// Settings.
		"Lightweight mode",
		"Drops the window material and the trend chart, and samples the engine once a second. The audio path is untouched: the engine keeps its own process either way.",
		"Start when I sign in",
		"The shell starts in the tray at sign-in and connects to the last receiver, so nothing has to be opened.",
		"Reconnect a dropped stream",
		"When the connection drops, start it again. Each retry waits twice as long as the one before, up to eight seconds, and a stream that ran for ten seconds starts the count over. A HomePod that asks for a PIN is not retried: that needs you.",
		"Closing the window",
		"Closing releases the window and everything it holds. Playback continues: the engine is a separate process, and the app stays in the tray.",
		"Quit",
		// Latency modes.
		"Custom", "Auto", "The engine will be asked for %d ms",
		"A new target applies to the next start.",
	}

	tru := i18n.For(i18n.Chinese)
	for _, key := range shown {
		if tru.T(key) == key {
			// A key that is its own translation has no entry, and would show
			// English on a Chinese interface.
			t.Errorf("%q has no Chinese entry, so it would show English", key)
		}
	}
}
