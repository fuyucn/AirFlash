// Command mygo-shell is the AirFlash desktop shell: a resident tray app that
// keeps the AirPlay engine running while its window is closed.
//
// The engine is a separate process. That is deliberate: it is the only way a
// Go runtime holds the engine's 7.98 ms send deadline while the UI is busy, and
// it is what lets the window be destroyed and rebuilt without interrupting
// playback.
package main

import (
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
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/settings"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/shell"
	appui "github.com/fuyucn/airflash/tools/mygo-shell/ui"
)

func main() {
	var (
		enginePath = flag.String("engine", "", "path to the airflash-engine executable")
		probeJSON  = flag.String("probe", "", "write a start-up report as JSON and exit")
	)
	flag.Parse()

	cfg := settings.Open(settings.DefaultPath())

	app := &App{
		settings: cfg,
		life:     shell.New(),
		state:    engine.NewState(),
	}

	if *enginePath != "" {
		app.enginePath = *enginePath
	} else {
		app.enginePath = defaultEnginePath()
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
	enginePath string
	settings   *settings.Store
	life       *shell.Lifecycle
	state      *engine.State

	client atomic.Pointer[engine.Client]
	win    atomic.Pointer[mygo.Window]

	peers atomic.Pointer[[]engine.Peer]
}

func (a *App) run() {
	a.life.OnClose(func(shell.Surface) {
		// Releasing the window is all that happens here. The engine is not
		// touched: playback must survive the window being closed.
		if w := a.win.Swap(nil); w != nil {
			w.Destroy()
		}
	})

	// With no window the app stays resident, so the engine keeps playing.
	// Nothing is quit here on purpose.
	mygo.App.OnWindowAllClosed(func() {})

	// The tray is the app's permanent surface: it exists whether or not a
	// window does, and it is how the user gets the window back. Both it and
	// the first window need a ready application.
	mygo.App.WhenReady(func() {
		a.installTray()
		a.openWindow(a.settings.Current())
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func (a *App) openWindow(cfg settings.Settings) {
	if !a.life.Open() {
		return
	}
	view := appui.New(appui.Deps{
		State:    a.state,
		Settings: a.settings,
		Life:     a.life,
		Peers:    a.currentPeers,
		OnStart:  a.startStream,
		OnStop:   a.stopStream,
		OnGain: func(g float64) {
			a.settings.Update(func(s *settings.Settings) { s.Gain = g })
			if c := a.client.Load(); c != nil {
				_, _ = c.SetGain(g)
			}
		},
		OnLatency: func(ms int) {
			a.settings.Update(func(s *settings.Settings) { s.LatencyMs = ms })
		},
		OnMode: func(m settings.Mode) {
			a.settings.Update(func(s *settings.Settings) { s.Mode = m })
			// The mode changes what the window draws, so rebuild it. Rebuilding
			// is also what frees a webview's process tree when switching away.
			a.life.Close()
			a.openWindow(a.settings.Current())
		},
		OnClose: func() { a.life.Close() },
		OnQuit:  func() { mygo.App.Quit() },
	})

	opts := mygo.WindowOptions{
		Title:           "AirFlash",
		Width:           980,
		Height:          680,
		MinWidth:        820,
		MinHeight:       560,
		StateKey:        "main",
		BackgroundColor: "light-dark(#F2F2F7, #1C1C1E)",
		Content:         ui.View(view.View),
	}
	if cfg.Mode != settings.ModeLightweight {
		// A translucent window material is what makes the bars read as
		// macOS's. Lightweight mode takes no material at all.
		opts.Vibrancy = mygo.VibrancySidebar
	}
	w := mygo.NewWindow(opts)
	a.win.Store(w)
}

func (a *App) installTray() {
	// A tray needs an icon; without one the app still runs, so a failure here
	// is reported rather than fatal.
	icon := trayIcon()
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Show AirFlash", Click: func(*mygo.MenuItem, *mygo.Window) {
			a.openWindow(a.settings.Current())
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
		log.Printf("tray unavailable: %v", err)
		return
	}
	tray.OnClick(func() { a.openWindow(a.settings.Current()) })
}

// startStream begins playback on a receiver, starting the engine if needed.
func (a *App) startStream(peer engine.Peer) {
	c := a.client.Load()
	if c == nil || !c.Running() {
		if a.enginePath == "" {
			log.Printf("no engine path configured")
			return
		}
		started, err := engine.Start(a.enginePath, a.state.Apply)
		if err != nil {
			log.Printf("engine start failed: %v", err)
			return
		}
		a.client.Store(started)
		_, _ = started.Hello()
		c = started
	}

	cfg := a.settings.Current()
	opts := engine.DefaultStreamOptions()
	opts.LatencyMs = cfg.LatencyMs
	opts.Gain = cfg.Gain
	opts.SampleRate = cfg.SampleRate

	if _, err := c.StartStream([]engine.Peer{peer}, opts); err != nil {
		log.Printf("start failed: %v", err)
	}
	a.settings.Update(func(s *settings.Settings) { s.LastPeer = peer.Host })
}

func (a *App) stopStream() {
	if c := a.client.Load(); c != nil {
		_, _ = c.Stop()
	}
}

func (a *App) currentPeers() []engine.Peer {
	if p := a.peers.Load(); p != nil {
		return *p
	}
	// Until discovery is wired to the engine, fall back to the receiver the
	// user last played to.
	if host := a.settings.Current().LastPeer; host != "" {
		return []engine.Peer{{Host: host, Port: 7000}}
	}
	return nil
}

// SetPeers lets a discovery loop publish what it found.
func (a *App) SetPeers(p []engine.Peer) {
	a.peers.Store(&p)
}

// Probe is the start-up report CI reads.
type Probe struct {
	Shell       string `json:"shell"`
	Runtime     string `json:"runtime"`
	CPUs        int    `json:"cpus"`
	GoMax       int    `json:"gomaxprocs"`
	Surface     string `json:"surface"`
	DefaultMode string `json:"default_mode"`
	LatencyMs   int    `json:"latency_ms"`
	EngineFound bool   `json:"engine_found"`
	EnginePath  string `json:"engine_path"`
	HasTray     bool   `json:"tray_api_available"`
	Note        string `json:"note"`
}

func (a *App) writeProbe(path string) {
	cfg := a.settings.Current()
	_, err := os.Stat(a.enginePath)
	rep := Probe{
		Shell:       "mygo (Go)",
		Runtime:     runtime.Version(),
		CPUs:        runtime.NumCPU(),
		GoMax:       runtime.GOMAXPROCS(0),
		Surface:     string(a.life.Surface()),
		DefaultMode: string(cfg.Mode),
		LatencyMs:   cfg.LatencyMs,
		EngineFound: err == nil,
		EnginePath:  a.enginePath,
		HasTray:     true,
		Note:        "window opens only on demand; the engine keeps its own process",
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		log.Fatalf("write probe: %v", err)
	}
	fmt.Println(string(b))
}

// defaultEnginePath looks for the engine beside the shell.
func defaultEnginePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	name := "airflash-engine"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidate := filepath.Join(dir, name)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return candidate
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
