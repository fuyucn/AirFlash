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
func TestTheStatsPageHasNoPlaybackAction(t *testing.T) {
	// Stats is for reading what the numbers are. Which receiver to play to is the
	// panel's and the Receivers page's question, and both name the device they act
	// on; a button here could only act on whatever was last played to, which reads
	// as "play to I don't know what".
	played := 0
	stopped := 0
	view := New(Deps{
		State:           streamingState(),
		Settings:        settings.Open("/tmp/airflash-stats-action.json"),
		Life:            shell.New(),
		Receivers:       func() []discovery.Receiver { return nil },
		PlayingReceiver: func() *discovery.Receiver { return nil },
		OnStop:          func() { stopped++ },
		OnPlay:          func(r discovery.Receiver) { played++ },
	})
	view.SetPage("stats")
	tester := ui.NewTester(view.View, 880, 640)
	if tester.HasText("Stop") {
		t.Error("Stats offers Stop: stopping is a decision about one receiver, which this page does not name")
	}
	// "Playing" is the phase label the engine reports and contains "Play" as a
	// prefix, so a substring search would match it. A button is a label of exactly
	// "Play", which is what is looked for here.
	for _, text := range tester.Texts() {
		if text == "Play" || text == "Stop" {
			t.Errorf("Stats shows a %q action: which receiver it acts on is not stated on the page", text)
		}
	}
	if played != 0 || stopped != 0 {
		t.Errorf("play fired %d times, stop %d times, want neither", played, stopped)
	}
	// The controls that do belong to Stats are still there.
	if !tester.HasText("LATENCY") {
		t.Error("the latency control is missing from Stats")
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
		// Settings.
		"SAMPLE RATE", "A new rate applies to the next stream.",
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
		"Quit", "Tune",
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

func TestEveryPageDrawsItsContent(t *testing.T) {
	// A page added to the navigation but not to the content switch draws a blank
	// panel, which is how the equalizer page first appeared: the sidebar listed
	// it and the window showed nothing. The sidebar is not the view.
	pages := []string{"stats", "receivers", "equalizer", "settings"}
	for _, page := range pages {
		cfg := settings.Open("/tmp/airflash-pages.json")
		cfg.Update(func(s *settings.Settings) {
			s.Language = string(i18n.Chinese)
		})
		view := New(Deps{
			Words:    i18n.For(i18n.Chinese),
			State:    streamingState(),
			Settings: cfg,
			Life:     shell.New(),
		})
		view.SetPage(page)
		tester := ui.NewTester(view.View, 880, 1180)

		// Each page has something the sidebar never shows.
		if page == "stats" && !tester.HasText("延迟曲线") {
			// The section label is drawn from the LATENCY OVER TIME key.
			t.Errorf("%s page drew no content", page)
		}
		if page == "equalizer" && !tester.HasText("均衡器") {
			t.Errorf("%s page drew no content", page)
		}
		if page == "settings" && !tester.HasText("轻量模式") {
			// The switch, which is the first thing on the page.
			t.Errorf("%s page drew no content", page)
		}
		// And no page is only the sidebar.
		if page != "stats" && len(tester.Texts()) <= 6 {
			t.Errorf("%s page drew %d strings, which is only the sidebar", page, len(tester.Texts()))
		}
	}
}

// pairingState is a state with a receiver waiting for a PIN.
func pairingState() *engine.State {
	st := engine.NewState()
	st.Apply(engine.Event{Event: "pin_required", Raw: []byte(
		`{"event":"pin_required","host":"10.0.10.31"}`)})
	return st
}

func TestThePanelAsksForAPinRatherThanFailing(t *testing.T) {
	// A HomePod asks for a PIN the first time this PC talks to it. The engine is
	// still running and still waiting, so calling it failed would make the shell
	// give up on a connection that is one thing short of working.
	living := receiver("AA", "Living Room", "10.0.10.31")
	panel := NewPanel(panelDeps(pairingState(), []discovery.Receiver{living}, ""))
	tester := ui.NewTester(panel.View, panelWidth, 600)

	if !tester.HasText("Pairing") {
		t.Error("the panel does not say a PIN is being waited for")
	}
	if tester.HasText("Failed") {
		t.Error("a pairing request is being reported as a failure")
	}
	// The prompt has to name the receiver, so the user knows which speaker to go
	// and read.
	if !tester.HasText("Enter the PIN shown on 10.0.10.31") {
		t.Error("the prompt does not say which receiver is waiting")
	}
}

func TestAPinIsOnlySentWhenItLooksLikeOne(t *testing.T) {
	// The engine accepts four to eight digits and refuses anything else after a
	// round trip that takes seconds, so the panel does not send one it would
	// reject.
	sent := ""
	panel := NewPanel(panelDeps(pairingState(), nil, ""))
	panel.deps.OnPairPin = func(pin string) { sent = pin }
	tester := ui.NewTester(panel.View, panelWidth, 600)

	for _, bad := range []string{"", "12", "abc", "12 34", "12345-"} {
		sent = ""
		// The field's own value is reached through the view, so a bad one is
		// typed in and the button pressed.
		panel.pin = bad
		if r, ok := tester.Find("Pair"); ok {
			tester.ClickAt(r.X+r.W/2, r.Y+r.H/2)
		}
		if sent != "" {
			t.Errorf("PIN %q was sent, want it refused before the round trip", bad)
		}
	}

	panel.pin = "1234"
	if r, ok := tester.Find("Pair"); ok {
		tester.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	}
	if sent != "1234" {
		t.Errorf("PIN sent = %q, want 1234", sent)
	}
}

func TestThePromptIsNotShownWhenNothingIsWaiting(t *testing.T) {
	// A prompt for a receiver that has been paired stays on screen otherwise.
	panel := NewPanel(panelDeps(streamingState(), nil, ""))
	tester := ui.NewTester(panel.View, panelWidth, 600)
	if tester.HasText("Pairing") {
		t.Error("a pairing prompt is shown while a stream is playing normally")
	}
}

func TestThePanelNeverGrowsPastItsCap(t *testing.T) {
	// A flyout is placed against the tray icon and measured before it opens. One
	// taller than the display's work area has nowhere to go, so a network with a
	// dozen speakers must not produce one: the panel scrolls instead.
	many := []discovery.Receiver{
		receiver("AA", "Living Room", "10.0.10.31"),
		receiver("BB", "Studio", "10.0.10.44"),
		receiver("CC", "Kitchen", "10.0.10.51"),
		receiver("DD", "Bedroom", "10.0.10.52"),
		receiver("EE", "Office", "10.0.10.53"),
		receiver("FF", "Attic", "10.0.10.54"),
		receiver("GG", "Garage", "10.0.10.55"),
		receiver("HH", "Basement", "10.0.10.56"),
	}
	h := NewPanel(panelDeps(engine.NewState(), many, "")).Height()
	if h > PanelMaxHeight() {
		t.Errorf("height = %d, want at most %d: the panel has to scroll, not run off the display", h, PanelMaxHeight())
	}
}

func TestThePanelStillFitsAFewReceiversWithoutScrolling(t *testing.T) {
	// The cap is for networks that do not fit. One with two speakers should be
	// exactly as tall as it draws, or the panel looks half empty.
	one := receiver("AA", "Living Room", "10.0.10.31")
	two := receiver("BB", "Studio", "10.0.10.44")
	h := NewPanel(panelDeps(engine.NewState(), []discovery.Receiver{one, two}, "")).Height()
	if h > PanelMaxHeight() {
		t.Errorf("height = %d, want at most %d", h, PanelMaxHeight())
	}
	if h < 200 {
		t.Errorf("height = %d, want enough for two rows and the footer", h)
	}
}

func TestReceiversIsTheFirstPage(t *testing.T) {
	// The window is opened to choose what to play to or to look at a device.
	// Stats is a reading one finds after, so it is not what should open first.
	view := New(Deps{
		State:    engine.NewState(),
		Settings: settings.Open("/tmp/airflash-first-page.json"),
		Life:     shell.New(),
	})
	if view.page != pageReceivers {
		t.Errorf("first page = %v, want the receivers page", view.page)
	}
}

func TestAHandLaunchOpensThePanelRatherThanNothing(t *testing.T) {
	// This is the launch rule itself, asserted on the setting it reads: a hand
	// launch is visible without being a window, and the window is opt-in. A login
	// launch ignores it and stays in the tray.
	s := settings.Default()
	if s.OpenAtLaunch {
		t.Error("the window should not open on a plain launch without the setting")
	}
	// Nothing about a login launch changes this: the tray launch is always quiet.
	startedByLogin := true
	if startedByLogin && !s.AutoConnect {
		// A login launch connects only when asked; either way it opens nothing.
		if s.OpenAtLaunch {
			t.Error("a login launch must stay quiet even with the setting on")
		}
	}
}

func TestTheWindowKeepsItsNavAtANarrowWidth(t *testing.T) {
	// The move from a sidebar to a top bar was for this: a 196 DIP sidebar
	// beside a small viewport leaves the page a sliver, and is the first thing
	// to go. A horizontal menu wraps and stays.
	view := New(Deps{
		Words:    i18n.For(i18n.English),
		State:    engine.NewState(),
		Settings: settings.Open("/tmp/airflash-narrow-nav.json"),
		Life:     shell.New(),
	})
	for _, width := range []int{720, 620, 560} {
		tester := ui.NewTester(view.View, width, 640)
		texts := tester.Texts()
		nav := 0
		for _, label := range []string{"Receivers", "Stats", "Equalizer", "Settings"} {
			for _, text := range texts {
				if text == label {
					nav++
					break
				}
			}
		}
		if nav != 4 {
			t.Errorf("at width %d the bar shows %d of 4 pages: a narrow window must not drop any", width, nav)
		}
	}
}

// devicePickerIDs stands in for the two lists the picker is built from: an
// adapter index and an endpoint id, both of which a machine can stop having.
func devicePickerIDs(chosen string, ids ...string) string {
	for _, id := range ids {
		if id == chosen {
			return id
		}
	}
	return ""
}

func TestAStoredDeviceTheMachineNoLongerHasFallsBack(t *testing.T) {
	// The stored value is what a settings file holds. Set on another machine, or
	// for a card that was unplugged, it is not in the list any more, and offering
	// it would offer a browse that fails.
	ids := []string{"", "3", "7"}
	if got := devicePickerIDs("9999", ids...); got != "" {
		t.Errorf("an absent adapter resolved to %q, want the automatic entry", got)
	}
	if got := devicePickerIDs("7", ids...); got != "7" {
		t.Errorf("a present adapter resolved to %q, want 7", got)
	}
	// The automatic entry is the stored value when nothing is set.
	if got := devicePickerIDs("", ids...); got != "" {
		t.Errorf("an unset id resolved to %q, want the automatic entry", got)
	}
}

func TestPairIsNotOfferedNextToAStreamingRow(t *testing.T) {
	// The engine's pair takes over whatever session is running and cancels it, so a
	// Pair beside a row that is streaming stops the audio in order to ask for a
	// PIN. Pairing is a receiver's own control, not something offered per row.
	living := receiver("AA", "Living Room", "10.0.10.31")
	studio := receiver("BB", "Studio", "10.0.10.44")
	paired := 0
	view := New(Deps{
		State:           streamingState(),
		Settings:        settings.Open("/tmp/airflash-pair-placement.json"),
		Life:            shell.New(),
		Receivers:       func() []discovery.Receiver { return []discovery.Receiver{living, studio} },
		PlayingReceiver: func() *discovery.Receiver { return &living },
		OnPair:          func(r discovery.Receiver) { paired++ },
	})
	view.SetPage("receivers")
	tester := ui.NewTester(view.View, 880, 1100)

	// Living Room is playing, so its row shows Stop.
	if !tester.HasText("Stop") {
		t.Error("the playing row offers no Stop")
	}
	// Studio is not playing, and its row must not offer Pair: pairing takes over
	// the running session and cancels it, so a Pair on any row while something is
	// streaming stops the audio the user is listening to.
	for _, text := range tester.Texts() {
		if text == "Pair" {
			t.Error("Pair is offered while a stream is running, which cancels it")
		}
	}
	if paired != 0 {
		t.Errorf("pair fired %d times with no intent, want none", paired)
	}
}

func TestPairIsOfferedInTheReceiversOwnControls(t *testing.T) {
	// Where it belongs: among a receiver's own controls, so it is plainly about
	// that one device and not something that fires while something else plays.
	living := receiver("AA", "Living Room", "10.0.10.31")
	paired := 0
	view := New(Deps{
		Words:           i18n.For(i18n.English),
		State:           streamingState(),
		Settings:        settings.Open("/tmp/airflash-pair-controls.json"),
		Life:            shell.New(),
		Receivers:       func() []discovery.Receiver { return []discovery.Receiver{living} },
		PlayingReceiver: func() *discovery.Receiver { return &living },
		OnPair:          func(r discovery.Receiver) { paired++ },
	})
	view.SetPage("receivers")
	view.tuning = living.ID
	tester := ui.NewTester(view.View, 880, 900)

	if !tester.HasText("Pair") {
		t.Fatal("the receiver's own controls offer no Pair")
	}
	if err := tester.Click("Pair"); err != nil {
		t.Fatal(err)
	}
	if paired != 1 {
		t.Errorf("Pair fired %d times, want once", paired)
	}
}
