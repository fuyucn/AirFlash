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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/adapt"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/autostart"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/discovery"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/enginebin"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/equalizer"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/i18n"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/localsound"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/settings"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/shell"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/trayicon"
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
		quitting:       make(chan struct{}),
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

	// restoreMu serialises the local-speaker restore. It runs off the UI thread,
	// because it is a COM round trip to the audio service and a slow one would
	// stall the click that asked to stop. The mutex is what keeps two stops from
	// interleaving their restore: the endpoint state one of them reads would be
	// the other's half-finished work.
	restoreMu sync.Mutex

	// lastPaint is when the open surfaces were last asked to redraw. The
	// engine reports more often than a person can read, so redraws are held to
	// the rate the mode asks for.
	lastPaint atomic.Int64

	// quitting is closed when the app is going away, which is what the retry
	// loop waits on so it does not sleep out its backoff - up to eight seconds -
	// after the shell has already left, holding the engine it would start again.
	// quitting is closed once, when the app is going away. It is closed rather
	// than a flag read because the retry loop waits on it, and a channel is what
	// wakes a select.
	quitting chan struct{}

	// discoveryCancel stops the mDNS browse. Discovery runs only while a window
	// is open, so a closed window costs nothing on the network either.
	discoveryCancel atomic.Pointer[context.CancelFunc]

	// page is which window page is open. It is the app's rather than the
	// window's because the window is destroyed and rebuilt freely, and a rebuild
	// that lost the page would drop the user back on Stats.
	page atomic.Pointer[string]
}

// currentPage is which page the window shows, for a rebuild to keep.
func (a *App) currentPage() string {
	if p := a.page.Load(); p != nil {
		return *p
	}
	return "receivers"
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

	// The engine is a process this app started, so leaving is when it ends.
	// Its stdin closing is what asks it to: the engine reads commands until it
	// sees EOF and then exits. Without this the shell goes away and the engine
	// stays behind, holding the audio device and looking like a second copy of
	// the app in the task list.
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		a.stopEngine()
		close(a.quitting)
	})

	// The tray is the app's permanent surface, and the panel it opens is the
	// app's main surface: the user opens it to choose what to play to. The
	// window is for settings and for the full reading, so nothing opens one at
	// launch.
	mygo.App.WhenReady(func() {
		a.installTray()
		a.startDiscovery()

		// A launch at login is the machine's, not a person's: it stays quiet in
		// the tray whatever the setting says, and connects to the last receiver
		// if that is what was asked for.
		if a.startedByLogin {
			if a.settings.Current().AutoConnect {
				a.connectToLast()
			}
			return
		}

		// A launch by hand. Whether anything is shown is the user's choice: the
		// tray is the app's surface by default, so a hand launch opens the panel
		// rather than sitting invisible with no way to know it is running. A
		// user who wants the window instead has the setting.
		switch {
		case a.settings.Current().OpenAtLaunch:
			a.openWindow("receivers")
		default:
			a.togglePanel()
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
	before := a.state.Snapshot().Phase
	a.state.Apply(ev)
	after := a.state.Snapshot().Phase

	// The moment playback begins is when the PC's own speakers go quiet, if that
	// is what the setting asks. Doing it on the transition rather than on every
	// event matters: the engine reports four times a second, and the mute is one
	// COM call that remembers the endpoint's state the first time only.
	if before != engine.PhaseStreaming && after == engine.PhaseStreaming {
		a.applyMute(a.settings.Current().MuteWhileStreaming)
	}
	if before != engine.PhaseStopped && after == engine.PhaseStopped {
		// A stream that ended without the user pressing stop - the engine
		// stopping, or a receiver dropping it - still has to give the speakers
		// back.
		if err := localsound.Restore(); err != nil {
			log.Printf("restore the PC speakers: %v", err)
		}
	}

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
	// A window the platform has already destroyed must not be shown again:
	// Show on a dead window does nothing at all, which is what leaves the
	// tray unable to bring the window back after its close button closed it.
	// The close hook clears a.win, so this is the second line of defence for
	// a close that arrives from somewhere else.
	if w := a.win.Load(); w != nil && !w.IsDestroyed() {
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
		OnSampleRate: func(rate int) {
			// A new rate applies to the next stream: the engine is asked for the
			// rate when a session starts, so the one running keeps the rate it
			// was started at rather than being re-asked mid-stream.
			a.settings.Update(func(s *settings.Settings) { s.SampleRate = rate })
		},
		OnReceiverOption: func(id, field string, value any) {
			a.settings.Update(func(s *settings.Settings) {
				o := s.Options(id)
				switch field {
				case "latency_ms":
					// nil clears the field, which is how a receiver goes back to
					// the global setting; a value sets its own.
					if value == nil {
						o.LatencyMs = nil
					} else if n, ok := value.(int); ok {
						o.LatencyMs = &n
					}
				case "volume":
					if n, ok := value.(int); ok {
						o.Volume = &n
					}
				case "auto_connect":
					if b, ok := value.(bool); ok {
						o.AutoConnect = &b
					}
				case "hidden":
					if b, ok := value.(bool); ok {
						o.Hidden = &b
					}
				}
				s.SetOptions(id, o)
			})
		},
		OnManualReceiver: func(add bool, index int, rec settings.ManualReceiver, onProblem func(string)) {
			if !add {
				a.settings.Update(func(s *settings.Settings) { s.DropManualReceiver(index) })
				a.repaint()
				return
			}
			// The id is derived from the address, which is what makes an entry
			// and a browse record of the same device the same receiver as far as
			// settings are concerned.
			rec.Id = discovery.Normalize(rec.Host)
			problem := ""
			a.settings.Update(func(s *settings.Settings) {
				if err := s.AddManualReceiver(rec); err != nil {
					problem = err.Error()
					return
				}
			})
			if problem == "" {
				a.repaint()
				return
			}
			// The form stays open on the fields that were typed, so a refusal is
			// not the whole thing being lost.
			if onProblem != nil {
				onProblem(problem)
			}
		},
		OnAlias: func(id, name string) {
			a.settings.Update(func(s *settings.Settings) { s.SetAlias(id, name) })
			a.repaint()
		},
		OnPairPin: a.pairPin,
		OnPair: func(r discovery.Receiver) {
			a.pair(r)
		},
		// The equalizer's four changes all reach the engine the same way: a live
		// stream takes it at once, and one that has not started inherits it from
		// settings when it does.
		OnEqualizer: a.sendEqualizer,
		OnEqualizerEnabled: func(on bool) {
			a.settings.Update(func(s *settings.Settings) { s.Equalizer.Enabled = on })
			a.sendEqualizer()
		},
		OnEqualizerPreset: func(name string) {
			a.settings.Update(func(s *settings.Settings) {
				for _, p := range equalizer.Presets {
					if p.Name == name {
						s.Equalizer.Apply(p)
					}
				}
			})
			a.sendEqualizer()
		},
		OnEqualizerBand: func(index int, db float64) {
			a.settings.Update(func(s *settings.Settings) { s.Equalizer.SetBand(index, db) })
			a.sendEqualizer()
		},
		OnEqualizerPreamp: func(db float64) {
			a.settings.Update(func(s *settings.Settings) { s.Equalizer.PreampDB = clampDB(db) })
			a.sendEqualizer()
		},
		OnLanguage: func(lang string) {
			a.settings.Update(func(s *settings.Settings) { s.Language = lang })
			// The words the window is drawn in, so the window is rebuilt. A
			// partial redraw would leave half the page in the old language.
			a.life.Close()
			a.openWindow(a.currentPage())
		},
		OnDiscoveryInterface: func(id string) {
			a.settings.Update(func(s *settings.Settings) { s.DiscoveryInterfaceID = id })
			// The browse runs on the adapter it was started with, so the only
			// way to move it is to run it again. A window is open while this is
			// being changed, so the new browse starts at once and the list
			// refills.
			a.stopDiscovery()
			a.startDiscovery()
		},
		OnCaptureEndpoint: func(id string) {
			a.settings.Update(func(s *settings.Settings) { s.CaptureEndpoint = id })
		},
		OnOpenAtLaunch: func(on bool) {
			a.settings.Update(func(s *settings.Settings) { s.OpenAtLaunch = on })
		},
		OnMuteWhileStreaming: func(on bool) {
			a.settings.Update(func(s *settings.Settings) { s.MuteWhileStreaming = on })
			// The setting is what a session applies when it starts, so it takes
			// effect now rather than on the next one.
			a.applyMute(on)
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
	a.page.Store(&page)

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
	// The window's own icon, in its title bar and on its taskbar button. A window
	// without one shows the host application's default, which on a Go program is
	// the generic executable mark - so an app with a tray logo and no window logo
	// looks like two different programs.
	if err := w.SetIcon(trayIcon()); err != nil {
		log.Printf("window icon: %v", err)
	}
	// The window's own close button. Without this the window is destroyed by
	// the platform with nothing here to hear about it, so a.win keeps pointing
	// at a window that is gone: the next openWindow takes the Show branch on a
	// dead window and shows nothing, which is the tray being unable to bring
	// the window back after it was closed.
	w.OnClose(func(e *mygo.CloseEvent) {
		a.life.Close()
		// Nothing is quit here: a tray app stays resident with no window, and
		// the engine keeps playing.
	})
	a.win.Store(w)
}

func (a *App) installTray() {
	// A tray needs an icon; without one the app still runs, so a failure here
	// is reported rather than fatal.
	icon := trayIcon()

	// No menu on the icon. A tray menu takes the left click over, so with one the
	// panel never opens from a click and the menu is the only way in. A panel is
	// the app's surface, so the click goes to it and the menu is moved to the
	// right click, which is where a context menu belongs.
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:    icon,
		ToolTip: "AirFlash",
	})
	if err != nil {
		log.Printf("tray unavailable, opening the window instead: %v", err)
		a.openWindow("stats")
		return
	}
	a.tray = tray
	if runtime.GOOS == "linux" {
		// A tray icon there is an AppIndicator, which reports no clicks at
		// all - not a left one and not a right one - and shows a menu on the
		// only gesture it does see. So the menu stays on the icon for good,
		// and it is the menu that says how to reach the panel: there is no
		// left click to hand that to.
		tray.SetMenu(a.trayMenuItems())
		return
	}
	tray.OnClick(a.togglePanel)
	tray.OnRightClick(a.trayMenu)
}

// trayMenuHost is what a tray menu is shown through: the calls that make a
// menu appear. It is an interface rather than *mygo.Tray so a test can watch
// the order they are made in, which is the whole of the behaviour.
type trayMenuHost interface {
	SetMenu(m *mygo.Menu)
	PopUpMenu()
}

// trayMenu is the icon's right-click menu: the way to the window and the way
// out. The left click goes to the panel, so this is what the menu is for.
func (a *App) trayMenu() {
	a.showTrayMenu(a.tray)
}

// showTrayMenu sets a menu on the icon, shows it, and takes it off again.
func (a *App) showTrayMenu(tray trayMenuHost) {
	// The taking off is the part that matters. SetMenu stores the menu on the
	// icon and the Windows backend answers a right click by showing whatever
	// menu the icon holds - after it has reported the click - so a menu left
	// on the icon is shown twice: once here, and once by the backend as this
	// callback returns. The second one appears the moment the user chooses
	// from the first, which reads as the menu coming back rather than the
	// choice being taken.
	//
	// PopUpMenu has no arguments because it presents the menu the tray already
	// holds, so the menu has to be set for as long as it is being shown.
	tray.SetMenu(a.trayMenuItems())
	defer tray.SetMenu(nil)
	tray.PopUpMenu()
}

// trayMenuItems is the right-click menu: the way to the window and the way out.
func (a *App) trayMenuItems() *mygo.Menu {
	items := []*mygo.MenuItem{}
	if runtime.GOOS == "linux" {
		// A tray icon on Linux reports no click at all, so the left click that
		// opens the panel elsewhere is an item here.
		items = append(items, &mygo.MenuItem{
			Label: "Open AirFlash",
			Click: func(*mygo.MenuItem, *mygo.Window) { a.togglePanel() },
		}, mygo.Separator())
	}

	window := func(page string) *mygo.MenuItem {
		return &mygo.MenuItem{Label: page, Click: func(*mygo.MenuItem, *mygo.Window) {
			a.openWindow(page)
		}}
	}
	return mygo.NewMenu(append(items,
		window("Stats"), window("Settings"),
		mygo.Separator(),
		&mygo.MenuItem{Label: "Quit", Click: func(*mygo.MenuItem, *mygo.Window) {
			mygo.App.Quit()
		}},
	))
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
	// A flyout closes when it loses focus, which is what every other popover on
	// every other platform does. Without this the panel stays over whatever the
	// user clicked next to.
	w.OnBlur(func() {
		w.Close()
		a.flyout.Store(nil)
	})
	a.flyout.Store(w)
}

// panelView builds the panel with the app's callbacks.
func (a *App) panelView() *appui.Panel {
	return appui.NewPanel(appui.Deps{
		Words:     i18n.For(a.settings.Current().Lang()),
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
		OnPairPin:      a.pairPin,
		OnOpenWindow:   a.openWindow,
		OnQuit:         func() { mygo.App.Quit() },
	})
}

// applyMute silences or restores the PC's own speakers, as the setting asks.
//
// This is the desktop host's "mute while streaming". It is applied when a
// session starts and when the setting changes during one, and undone when the
// session ends: the engine being a separate process does not help here, because
// the endpoint that has to be silenced is the shell's own machine's.
//
// The default render endpoint is the one that is silenced. A user with several
// speakers who wants one left alone would need a per-endpoint choice, and the
// desktop host has the same behaviour - it silences the endpoint audio is
// playing through.
func (a *App) applyMute(on bool) {
	if !on {
		if err := localsound.Restore(); err != nil {
			log.Printf("restore the PC speakers: %v", err)
		}
		return
	}
	// With no endpoint named, the default render device is the one to silence,
	// which is what an empty name means to the audio service. Nothing is
	// enumerated here: a stream that is running has already decided, and
	// re-reading the endpoints would be work that changes nothing.
	if err := localsound.Mute(defaultRenderEndpoint()); err != nil {
		log.Printf("mute the PC speakers: %v", err)
	}
}

// defaultRenderEndpoint is the endpoint audio is playing through, or "" when
// there is none to name.
//
// It is the endpoint the engine captures from, which is the render device by
// definition: the engine's loopback capture reads what the console is sending to
// the speakers, so that is the endpoint that has to go quiet.
func defaultRenderEndpoint() string {
	for _, e := range localsound.Endpoints() {
		if !e.Capture {
			return e.ID
		}
	}
	return ""
}

// pair starts pairing with a receiver, which is what a HomePod asks for the
// first time this PC talks to it.
//
// The engine runs pairing as its own session rather than a phase of a stream, so
// the engine is started here if it is not already running and the pairing is
// asked for on its own. The prompt that follows is drawn by the panel from the
// engine's pin_required event.
func (a *App) pair(r discovery.Receiver) {
	if !r.Complete() {
		return
	}
	peers := []engine.Peer{}
	for _, m := range r.Peers() {
		peers = append(peers, engine.Peer{Host: m.Host, Port: m.Port})
	}
	if len(peers) == 0 {
		return
	}
	c := a.client.Load()
	if c == nil || !c.Running() {
		client, err := engine.Start(a.enginePath, a.onEngineEvent)
		if err != nil {
			log.Printf("engine start failed: %v", err)
			return
		}
		a.client.Store(client)
		_, _ = client.Hello()
		c = client
	}
	// A stereo pair is paired to once, through one half: the credentials are the
	// receiver's and a pair shares them.
	if _, err := c.Pair(peers[0]); err != nil {
		log.Printf("pair: %v", err)
	}
}

// restoreSpeakers gives the local speakers back after a stream, off the UI
// thread.
//
// It is a COM round trip to the audio service. Running it inline meant a slow
// service stalled the click that asked to stop, so it goes to a goroutine; the
// mutex keeps two stops from interleaving, and a failure is logged because a
// failed restore leaves the speakers silent with nothing else to say why.
func (a *App) restoreSpeakers() {
	go func() {
		a.restoreMu.Lock()
		defer a.restoreMu.Unlock()
		if err := localsound.Restore(); err != nil {
			log.Printf("restore the PC speakers: %v", err)
		}
	}()
}

// pairPin sends the PIN the receiver asked for, which completes pairing.
//
// Both surfaces ask for it - the panel's prompt and the window's - and both send
// it the same way, so it is one method rather than two copies.
func (a *App) pairPin(pin string) {
	c := a.client.Load()
	if c == nil || !c.Running() {
		return
	}
	if _, err := c.PairPin(pin); err != nil {
		log.Printf("pair pin: %v", err)
	}
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
	ctx, _, ok := claimBrowse(&a.discoveryCancel)
	if !ok {
		return
	}

	go func() {
		// The interface to browse on, or none named, which is the system's own
		// choice and right for a machine with one adapter.
		onInterface := 0
		if n, err := strconv.Atoi(a.settings.Current().DiscoveryInterfaceID); err == nil && n > 0 {
			onInterface = n
		}

		err := discovery.Browse(ctx, onInterface, func(found []discovery.Receiver) {
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

// claimBrowse takes the one browse slot, or reports that it was already taken.
//
// A load-then-store is a check-then-act: two callers can both see nil and both
// start a browse, and the second then overwrites the first's cancel, so the first
// can never be stopped. The compare-and-swap makes the check and the claim one
// step. The loser cancels the context it made rather than leaving it running with
// nothing holding its cancel.
func claimBrowse(slot *atomic.Pointer[context.CancelFunc]) (context.Context, context.CancelFunc, bool) {
	ctx, cancel := context.WithCancel(context.Background())
	if !slot.CompareAndSwap(nil, &cancel) {
		cancel()
		return nil, nil, false
	}
	return ctx, cancel, true
}

// stopDiscovery ends the browse and releases the cancel.
func (a *App) stopDiscovery() {
	if c := a.discoveryCancel.Swap(nil); c != nil {
		(*c)()
	}
}

// Receivers returns what discovery has found.
// Receivers is what discovery found, minus any receiver the user has hidden.
//
// A hidden receiver stays in the list the browse keeps, so it comes back when it
// is un-hidden, and the panel shows only what the user chose to see.
func (a *App) Receivers() []discovery.Receiver {
	found := a.receivers.Load()
	if found == nil {
		return nil
	}
	cfg := a.settings.Current()
	out := make([]discovery.Receiver, 0, len(*found))
	for _, r := range *found {
		if cfg.HiddenFor(r.ID) {
			continue
		}
		// A manual entry for the same device is not another receiver: the browse
		// and the user are describing one speaker, and showing both would be two
		// rows for it.
		if isManualEndpoint(cfg.ManualReceivers, r.Address, r.Port) {
			continue
		}
		out = append(out, named(cfg, r))
	}
	// The ones the browse did not find, which is the whole point of having them:
	// a receiver on another subnet answers mDNS nowhere the local machine can
	// hear.
	for _, m := range cfg.ManualReceivers {
		if cfg.HiddenFor(m.Id) {
			continue
		}
		out = append(out, manualAsReceiver(cfg, m))
	}
	return out
}

// manualAsReceiver is a manual entry as the browse would have reported it. It
// reads as present rather than absent because nothing is checking: the browse
// that would say otherwise cannot reach it, which is why it was typed in.
func manualAsReceiver(cfg settings.Settings, m settings.ManualReceiver) discovery.Receiver {
	return discovery.Receiver{
		ID:      m.Id,
		Name:    nameOrHost(m.Name, m.Host),
		Address: m.Host,
		Port:    m.Port,
		Online:  true,
	}
}

// isManualEndpoint reports whether a manual entry describes this endpoint.
func isManualEndpoint(manual []settings.ManualReceiver, host string, port int) bool {
	for _, m := range manual {
		if strings.EqualFold(m.Host, host) && m.Port == port {
			return true
		}
	}
	return false
}

// nameOrHost is a manual receiver's name, or its address when none was given.
// A row with no name is still a row the user has to recognise.
func nameOrHost(name, host string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return host
}

// named applies one receiver's alias, which is what the surfaces show instead of
// the name the device itself reports.
func named(cfg settings.Settings, r discovery.Receiver) discovery.Receiver {
	if alias := cfg.AliasFor(r.ID); alias != "" {
		r.Name = alias
	}
	return r
}

// playReceiver starts playback on what discovery found. A stereo pair sends
// to both of its halves; a single device sends to itself.
func (a *App) playReceiver(r discovery.Receiver) {
	if !r.Complete() {
		return
	}
	// Playing to the receiver that is already playing is not a second request
	// to hear it: it is a request the engine cannot honour. Its start takes over
	// whatever session is running and drops it, so a second Play on the same
	// device cancels the stream that was working and leaves two session loops
	// racing - which ends in a failure the user did nothing to earn.
	//
	// The row shows Stop for this receiver, so this is the guard behind it.
	if playing := a.playing.Load(); playing != nil && *playing == r.ID {
		return
	}
	var peers []engine.Peer
	for _, m := range r.Peers() {
		peers = append(peers, engine.Peer{Host: m.Host, Port: m.Port})
	}
	if len(peers) == 0 {
		return
	}
	// The receiver's own id, which is what PlayingReceiver looks up. The engine
	// is told hosts, so without this the shell would only know an address, and
	// an address does not say which receiver in the list it belongs to.
	a.playing.Store(&r.ID)
	a.startStreamTo(r.ID, peers)
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
func (a *App) startStreamTo(receiverID string, peers []engine.Peer) {
	if len(peers) == 0 {
		return
	}
	go a.runSession(session{peers: peers, receiverID: receiverID})
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
			a.startStreamTo(id, []engine.Peer{{Host: host, Port: 7000}})
			return
		}
	}
	// The receiver has not been found yet, so its own settings cannot be looked
	// up and the global ones are used.
	a.startStreamTo("", []engine.Peer{{Host: host, Port: 7000}})
}

// sendEqualizer tells a live engine about the current equalizer.
//
// A stream that has not started gets it from settings when it does, so nothing
// is queued for one here.
func (a *App) sendEqualizer() {
	c := a.client.Load()
	if c == nil || !c.Running() {
		return
	}
	if _, err := c.SetEqualizer(a.settings.Current().Equalizer); err != nil {
		log.Printf("set equalizer: %v", err)
	}
}

// clampDB keeps a gain inside what the engine validates, so a slider dragged
// past the edge does not fail on the engine afterwards.
func clampDB(db float64) float64 {
	if db > equalizer.LimitDB {
		return equalizer.LimitDB
	}
	if db < -equalizer.LimitDB {
		return -equalizer.LimitDB
	}
	return db
}

// stopEngine ends the engine process itself, rather than what it is playing.
//
// stopStream ends a session and leaves the engine reading commands; this closes
// its stdin, which is what makes it exit. The shell is going away in the
// ordinary case, so there is nobody left to read its events or write it
// commands.
func (a *App) stopEngine() {
	// Set first: the reconnect loop waits between attempts, and an engine that
	// is going away for good must not be brought back by a loop waking up.
	a.stopping.Store(true)
	if c := a.client.Swap(nil); c != nil {
		_, _ = c.Stop()
		_ = c.Close()
	}
}

func (a *App) stopStream() {
	// Set first: the reconnect loop waits between attempts, and stopping has to
	// be visible before the engine is asked to stop so that a loop waking up
	// sees it.
	a.stopping.Store(true)
	// The stream is going away, so what was silenced for it is given back - but
	// not on this thread. The restore is a COM round trip to the audio service,
	// and a slow one would stall the click that asked to stop; the speakers stay
	// silent until it lands either way.
	a.restoreSpeakers()
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
	return trayicon.PNG()
}
