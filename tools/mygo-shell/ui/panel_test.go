package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/discovery"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
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
