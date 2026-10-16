// Command mygo-shell is the AirFlash desktop shell: a resident tray app that
// keeps the AirPlay engine running while its window is closed.
//
// The engine is a separate process. That is deliberate: it is the only way a
// Go runtime holds the engine's 7.98 ms send deadline while the UI is busy, and
// it is what lets the window be destroyed and rebuilt without interrupting
// playback.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/adapt"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/autostart"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/discovery"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/enginebin"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/settings"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/shell"
	appui "github.com/fuyucn/airflash/tools/mygo-shell/ui"
)

func main() {
	var (
		enginePath = flag.String("engine", "", "path to the airflash-engine executable")
		probeJSON  = flag.String("probe", "", "write a start-up report as JSON and exit")
		// startedByLogin is set by the startup entry this app writes. It is what
		// separates a launch at login from one by hand: the first stays quiet and
		// connects to the last receiver, the second opens the panel.
		startedByLogin = flag.Bool("startup", false, "started by the machine's login, not by hand")
	)
	flag.Parse()

	cfg := settings.Open(settings.DefaultPath())

	// Keep the startup entry in step with the setting, whichever way the truth
	// moved: the registry is read rather than the settings, because a user can
	// remove the entry by hand and an earlier install can leave one this one
	// does not know about.
	if on, err := autostart.Enabled(); err != nil {
		log.Printf("autostart: could not read the startup entry: %v", err)
	} else if on != cfg.Current().StartAtLogin {
		cfg.Update(func(s *settings.Settings) { s.StartAtLogin = on })
	}

	app := &App{
		startedByLogin: *startedByLogin,
		settings:       cfg,
		life:           shell.New(),
		state:          engine.NewState(),
	}

	if *enginePath != "" {
		app.enginePath = *enginePath
	} else {
		app.enginePath = resolveEngine()
	}

	// A start-up report lets CI prove the shell builds, launches and can
	// describe itself without a display.
	if *probeJSON != "" {
		app.writeProbe(*probeJSON)
		return
	}

	app.run()
}

// App owns the shell's runtime objects.
type App struct {
	// startedByLogin says the machine launched this, not a person. A launch by
	// hand opens the panel; one at login stays in the tray and connects to the
	// last receiver if the settings ask for it.
	startedByLogin bool
	enginePath     string
	settings       *settings.Store
	life           *shell.Lifecycle
	state          *engine.State

	client atomic.Pointer[engine.Client]
	win    atomic.Pointer[mygo.Window]
	tray   *mygo.Tray
	// flyout is the open panel, or nil. It is atomic because the engine
	// goroutine repaints whatever is open, while the tray callback opens and
	// closes it on the main thread.
	flyout atomic.Pointer[mygo.Window]

	receivers atomic.Pointer[[]discovery.Receiver]
	// playing is the receiver playback is going to, or "". The engine streams
	// to hosts, so the receiver those hosts belong to is the shell's own fact.
	playing atomic.Pointer[string]

	// stopping is set when the user asks for playback to end. It is what keeps a
	// reconnect from bringing the session back after the user pressed stop:
	// the loop waits between attempts, and without this it would wake and start
	// an engine the user had just asked to be silent.
	stopping atomic.Bool

	// lastPaint is when the open surfaces were last asked to redraw. The
	// engine reports more often than a person can read, so redraws are held to
	// the rate the mode asks for.
	lastPaint atomic.Int64

	// discoveryCancel stops the mDNS browse. Discovery runs only while a window
	// is open, so a closed window costs nothing on the network either.
	discoveryCancel atomic.Pointer[context.CancelFunc]
}

func (a *App) run() {
	a.life.OnClose(func(shell.Surface) {
		// Releasing the window is all that happens here. The engine is not
		// touched: playback must survive the window being closed.
		if w := a.win.Swap(nil); w != nil {
			w.Destroy()
		}
		// Discovery serves the window, so it stops with it.
		a.stopDiscovery()
	})

	// With no window the app stays resident, so the engine keeps playing.
	// Nothing is quit here on purpose.
	mygo.App.OnWindowAllClosed(func() {})

	// The tray is the app's permanent surface, and the panel it opens is the
	// app's main surface: the user opens it to choose what to play to. The
	// window is for settings and for the full reading, so nothing opens one at
	// launch.
	mygo.App.WhenReady(func() {
		a.installTray()
		a.startDiscovery()
		// A launch at login is the machine's, not a person's: nothing is shown,
		// and the last receiver is connected to if that is what was asked for. A
		// launch by hand stays as it was, and the user opens the panel.
		if a.startedByLogin && a.settings.Current().AutoConnect {
			a.connectToLast()
		}
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// onEngineEvent folds one engine event into the model and repaints whatever is
// open.
//
// Without the repaint the surfaces would show the state as it was when they
// opened: nothing else redraws them, because the only other thing that arrives
// asynchronously is discovery, and that only runs while a window is open. A
// reading that never moves is worse than no reading at all.
func (a *App) onEngineEvent(ev engine.Event) {
	a.state.Apply(ev)

	// The engine reports several times a second; the redraw rate is what the
	// mode chose, so a burst of events costs one frame rather than one each.
	now := time.Now().UnixMilli()
	if !a.dueToPaint(now) {
		return
	}

	if w := a.win.Load(); w != nil {
		w.Invalidate()
	}
	if f := a.flyout.Load(); f != nil {
		f.Invalidate()
	}
}

// dueToPaint reports whether enough time has passed for another redraw, and
// records this one if so.
//
// The engine reports four times a second in standard mode and once in
// lightweight; the redraw rate is what the mode asks for. Without this every
// report would paint, and in lightweight mode - whose whole point is to stop
// the shell spending cycles on itself - the UI would redraw four times as often
// as the setting says.
func (a *App) dueToPaint(nowMs int64) bool {
	interval := int64(a.settings.Current().RedrawIntervalMs())
	last := a.lastPaint.Load()
	if last != 0 && nowMs-last < interval {
		return false
	}
	a.lastPaint.Store(nowMs)
	return true
}

func (a *App) openWindow(page string) {
	if w := a.win.Load(); w != nil {
		w.Show()
		w.Focus()
		return
	}
	if !a.life.Open() {
		return
	}
	view := appui.New(appui.Deps{
		State:           a.state,
		Settings:        a.settings,
		Life:            a.life,
		Receivers:       a.Receivers,
		PlayingReceiver: a.PlayingReceiver,
		OnPlay:          a.playReceiver,
		OnStop:          a.stopStream,
		OnGain: func(g float64) {
			a.settings.Update(func(s *settings.Settings) { s.Gain = g })
			if c := a.client.Load(); c != nil {
				_, _ = c.SetGain(g)
			}
		},
		OnLatency: func(ms int) {
			a.settings.Update(func(s *settings.Settings) { s.LatencyMs = ms })
		},
		OnAuto: func(on bool) {
			a.settings.Update(func(s *settings.Settings) { s.LatencyAuto = on })
		},
		AdaptedMs: a.adaptedTarget,
		OnMode: func(m settings.Mode) {
			a.settings.Update(func(s *settings.Settings) { s.Mode = m })
			// The mode changes what the window draws, so rebuild it. Rebuilding
			// is also what frees a webview's process tree when switching away.
			a.life.Close()
			a.openWindow(page)
		},
		OnStartAtLogin: func(on bool) {
			a.settings.Update(func(s *settings.Settings) { s.StartAtLogin = on })
			// The setting is a request to change the registry, and the registry
			// is the truth: it is written immediately, so the two cannot drift.
			if err := autostart.Set(on); err != nil {
				log.Printf("autostart: %v", err)
			}
		},
		OnReconnect: func(on bool) {
			a.settings.Update(func(s *settings.Settings) { s.Reconnect = on })
		},
		OnReconnectAttempts: func(n int) {
			a.settings.Update(func(s *settings.Settings) { s.ReconnectAttempts = n })
		},
		OnClose: func() { a.life.Close() },
		OnQuit:  func() { mygo.App.Quit() },
	})
	view.SetPage(page)

	opts := mygo.WindowOptions{
		Title:           "AirFlash",
		Width:           880,
		Height:          640,
		MinWidth:        720,
		MinHeight:       520,
		StateKey:        "window",
		BackgroundColor: "light-dark(#FFFFFF, #0C0C0E)",
		Content:         ui.View(view.View),
	}
	w := mygo.NewWindow(opts)
	a.win.Store(w)
}

func (a *App) installTray() {
	// A tray needs an icon; without one the app still runs, so a failure here
	// is reported rather than fatal.
	icon := trayIcon()
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Stats", Click: func(*mygo.MenuItem, *mygo.Window) {
			a.openWindow("stats")
		}},
		{Label: "Settings", Click: func(*mygo.MenuItem, *mygo.Window) {
			a.openWindow("settings")
		}},
		{Type: mygo.MenuItemSeparator},
		{Label: "Quit", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	})

	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:    icon,
		ToolTip: "AirFlash",
		Menu:    menu,
	})
	if err != nil {
		log.Printf("tray unavailable, opening the window instead: %v", err)
		a.openWindow("stats")
		return
	}
	a.tray = tray
	// On Linux a tray icon reports neither clicks nor its own position, so a
	// menu item is the way to the panel and the panel opens in the middle of
	// the display. On the other platforms a click is enough.
	tray.OnClick(a.togglePanel)
}

// adaptedTarget is the latency the shell worked out from previous sessions.
// It is only ever a suggestion: the user's setting is the floor.
func (a *App) adaptedTarget() int {
	cfg := a.settings.Current()
	if !cfg.Adapt || len(cfg.Sessions) == 0 {
		return cfg.LatencyMs
	}
	target, _ := adapt.Recommend(cfg.LatencyMs, observations(cfg.Sessions))
	return target
}

// observations converts the stored sessions for the recommender.
func observations(sessions []settings.Session) []adapt.Observation {
	out := make([]adapt.Observation, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, adapt.Observation{
			TargetMs:       s.TargetMs,
			PeakRTTMs:      s.PeakRTTMs,
			MedianRTTMs:    s.MedianRTTMs,
			SenderStalls:   s.SenderStalls,
			SkippedPackets: s.SkippedPackets,
			ExpiredResends: s.ExpiredResends,
			At:             s.At,
		})
	}
	return out
}

// recordSession stores what a finished session turned out to be, so the next
// one can learn from it. It runs when a stream stops, never during one.
func (a *App) recordSession() {
	peak, median := a.state.SessionRTT()
	m := a.state.Snapshot()
	cfg := a.settings.Current()

	// A session that never measured anything has nothing to teach.
	if peak == 0 && m.SenderLateRecoveries == 0 && m.SkippedPackets == 0 &&
		m.RetransmitExpired == 0 {
		a.state.ResetSession()
		return
	}

	a.settings.Update(func(s *settings.Settings) {
		s.Sessions = append(s.Sessions, settings.Session{
			TargetMs:       cfg.EffectiveLatencyMs(a.adaptedTarget()),
			PeakRTTMs:      peak,
			MedianRTTMs:    median,
			SenderStalls:   m.SenderLateRecoveries,
			SkippedPackets: m.SkippedPackets,
			ExpiredResends: m.RetransmitExpired,
			At:             time.Now(),
		})
	})
	a.state.ResetSession()
}

// togglePanel opens the tray panel, or closes it if it is already open.
//
// The panel is the app's main surface: a narrow flyout beside the tray icon,
// not a window. It is destroyed rather than hidden when it closes, so it holds
// nothing while the user is not looking at it.
func (a *App) togglePanel() {
	if w := a.flyout.Load(); w != nil {
		w.Close()
		a.flyout.Store(nil)
		return
	}
	if a.tray == nil {
		// Without a tray there is nothing to anchor to, so the window stands in.
		a.openWindow("stats")
		return
	}

	panel := a.panelView()
	w := mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:      a.tray,
		Placement: mygo.PlacementBottom,
		Gap:       6,
		Width:     appui.PanelWidth(),
		Height:    panel.Height(),
		Focusable: true,
		Shadow:    true,
		Content:   ui.View(panel.View),
	})
	// The panel is destroyed when it closes, so it holds nothing while the user
	// is not looking at it.
	w.OnClosed(func() { a.flyout.Store(nil) })
	a.flyout.Store(w)
}

// panelView builds the panel with the app's callbacks.
func (a *App) panelView() *appui.Panel {
	return appui.NewPanel(appui.Deps{
		State:     a.state,
		Settings:  a.settings,
		Life:      a.life,
		Receivers: a.Receivers,
		OnPlay:    a.playReceiver,
		OnStop:    a.stopStream,
		OnGain: func(g float64) {
			a.settings.Update(func(s *settings.Settings) { s.Gain = g })
			if c := a.client.Load(); c != nil {
				_, _ = c.SetGain(g)
			}
		},
		OnDeviceVolume: a.setDeviceVolume,
		OnOpenWindow:   a.openWindow,
		OnQuit:         func() { mygo.App.Quit() },
	})
}

// setDeviceVolume asks the engine to set a receiver's own volume.
func (a *App) setDeviceVolume(rec discovery.Receiver, volume float64) {
	c := a.client.Load()
	if c == nil || !c.Running() {
		return
	}
	if _, err := c.SetDeviceVolume(volume); err != nil {
		log.Printf("set device volume: %v", err)
	}
}

// startDiscovery browses for receivers while a window is open.
func (a *App) startDiscovery() {
	if a.discoveryCancel.Load() != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.discoveryCancel.Store(&cancel)

	go func() {
		err := discovery.Browse(ctx, func(found []discovery.Receiver) {
			a.receivers.Store(&found)

			if w := a.win.Load(); w != nil {
				w.Invalidate()
			}
		}, func(err error) {
			log.Printf("discovery: %v", err)
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("discovery stopped: %v", err)
		}
	}()
}

// stopDiscovery ends the browse and releases the cancel.
func (a *App) stopDiscovery() {
	if c := a.discoveryCancel.Swap(nil); c != nil {
		(*c)()
	}
}

// Receivers returns what discovery has found.
func (a *App) Receivers() []discovery.Receiver {
	if r := a.receivers.Load(); r != nil {
		return *r
	}
	return nil
}

// playReceiver starts playback on what discovery found. A stereo pair sends
// to both of its halves; a single device sends to itself.
func (a *App) playReceiver(r discovery.Receiver) {
	if !r.Complete() {
		return
	}
	var peers []engine.Peer
	for _, m := range r.Peers() {
		peers = append(peers, engine.Peer{Host: m.Address, Port: m.Port})
	}
	if len(peers) == 0 {
		return
	}
	// The receiver's own id, which is what PlayingReceiver looks up. The engine
	// is told hosts, so without this the shell would only know an address, and
	// an address does not say which receiver in the list it belongs to.
	a.playing.Store(&r.ID)
	a.startStreamTo(peers)
}

// PlayingReceiver returns the receiver playback is going to, or nil.
//
// The engine is told hosts, not a receiver, so this is the shell's own record
// of the choice the user made. It is what the window's action operates on and
// what the volume slider is shown for.
func (a *App) PlayingReceiver() *discovery.Receiver {
	if id := a.playing.Load(); id != nil {
		for _, r := range a.Receivers() {
			if r.ID == *id {
				// Go 1.22 gives each loop iteration its own variable, so the
				// copy outlives the loop that made it.
				found := r
				return &found
			}
		}
	}
	return nil
}

// startStreamTo starts the engine if needed and begins playback to the peers.
// startStreamTo begins a session on a receiver, and keeps it alive across a
// reconnect if the user asked for that.
//
// The session lives in its own goroutine, so the surfaces stay responsive while
// the engine is being brought up, and so the retry loop can wait without holding
// anything the UI needs.
func (a *App) startStreamTo(peers []engine.Peer) {
	if len(peers) == 0 {
		return
	}
	go a.runSession(session{peers: peers})
}

// connectToLast starts a session on the receiver the user last played to.
//
// The address was stored when that session started, which is what a login launch
// has to work from: discovery has not run yet, so the receiver's name is not
// known here, only where it was. The last one is used rather than anything found
// by scanning, because a scan at login can find a different device and the user
// asked for the one they were using.
func (a *App) connectToLast() {
	host := a.settings.Current().LastPeer
	if host == "" {
		return
	}
	// The receiver's id is what the panel shows the session against, and the
	// stored value is a host. Discovery has not run yet, so the receiver cannot
	// be looked up here; a later browse repairs it, and until then the row is
	// chosen by its address rather than by name.
	for _, r := range a.Receivers() {
		if r.Address == host {
			id := r.ID
			a.playing.Store(&id)
			break
		}
	}
	a.startStreamTo([]engine.Peer{{Host: host, Port: 7000}})
}

func (a *App) stopStream() {
	// Set first: the reconnect loop waits between attempts, and stopping has to
	// be visible before the engine is asked to stop so that a loop waking up
	// sees it.
	a.stopping.Store(true)
	a.playing.Store(nil)
	if c := a.client.Load(); c != nil {
		_, _ = c.Stop()
	}
	// The session is over, so what it showed about the network is now known.
	// runSession also records one when it ends by itself, so this is the path
	// for a session the user ended.
	a.state.Apply(engine.Event{Event: "stopped", Raw: []byte(`{"event":"stopped"}`)})
}

// Probe is the start-up report CI reads.
type Probe struct {
	Shell         string `json:"shell"`
	Runtime       string `json:"runtime"`
	CPUs          int    `json:"cpus"`
	GoMax         int    `json:"gomaxprocs"`
	Surface       string `json:"surface"`
	DefaultMode   string `json:"default_mode"`
	LatencyMs     int    `json:"latency_ms"`
	EngineFound   bool   `json:"engine_found"`
	EnginePath    string `json:"engine_path"`
	EngineBundled bool   `json:"engine_bundled"`
	EngineBytes   int    `json:"engine_bytes"`
	HasTray       bool   `json:"tray_api_available"`
	Note          string `json:"note"`
}

func (a *App) writeProbe(path string) {
	cfg := a.settings.Current()
	_, err := os.Stat(a.enginePath)
	rep := Probe{
		Shell:         "mygo (Go)",
		Runtime:       runtime.Version(),
		CPUs:          runtime.NumCPU(),
		GoMax:         runtime.GOMAXPROCS(0),
		Surface:       string(a.life.Surface()),
		DefaultMode:   string(cfg.Mode),
		LatencyMs:     cfg.LatencyMs,
		EngineFound:   err == nil,
		EnginePath:    a.enginePath,
		EngineBundled: enginebin.Bundled(),
		EngineBytes:   enginebin.Size(),
		HasTray:       true,
		Note:          "window opens only on demand; the engine keeps its own process",
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		log.Fatalf("write probe: %v", err)
	}
	fmt.Println(string(b))
}

// resolveEngine finds the engine, in the order that respects what the user
// meant: a file placed beside the shell first, so a developer can swap one in,
// then the copy embedded in this build.
func resolveEngine() string {
	if beside := besideEnginePath(); beside != "" {
		if _, err := os.Stat(beside); err == nil {
			return beside
		}
	}
	if enginebin.Bundled() {
		path, err := enginebin.Ensure(enginebin.CacheDir())
		if err != nil {
			log.Printf("could not unpack the bundled engine: %v", err)
			return ""
		}
		return path
	}
	// Nothing to run, but returning the expected path lets the start-up report
	// say where an engine was looked for.
	return besideEnginePath()
}

// besideEnginePath is where an engine would sit if one were placed next to the
// shell, which is how the portable layout works.
func besideEnginePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), enginebin.Name())
}

// trayIcon returns a small PNG, or nil when none is embedded. A missing icon
// is not fatal: the tray is reported and skipped.
func trayIcon() []byte {
	if len(embeddedIcon) == 0 {
		return nil
	}
	return embeddedIcon
}

// embeddedIcon is filled in by an asset build, if one is present.
var embeddedIcon []byte

var _ = time.Now
