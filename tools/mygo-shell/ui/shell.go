package ui

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/settings"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/shell"
)

// Deps is everything the view reads and calls.
type Deps struct {
	State     *engine.State
	Settings  *settings.Store
	Life      *shell.Lifecycle
	Peers     func() []engine.Peer
	OnStart   func(engine.Peer)
	OnStop    func()
	OnGain    func(float64)
	OnLatency func(int)
	OnMode    func(settings.Mode)
	OnClose   func()
	OnQuit    func()
}

// page identifies the content shown beside the sidebar.
type page int

const (
	pageNow page = iota
	pageReceivers
	pageSettings
)

// Shell is the app's view state that lives between frames.
type Shell struct {
	deps Deps
	page page
	// entered is when the window opened, for the entrance animation.
	entered time.Time
}

// New builds the shell view.
func New(d Deps) *Shell {
	return &Shell{deps: d, entered: time.Now()}
}

// View builds one frame.
func (s *Shell) View(c *ui.Context) {
	st := s.deps.State.Snapshot()
	cfg := s.deps.Settings.Current()
	p := forDark(c.Theme().Dark)

	// In lightweight mode the content is a flat plane and the window takes no
	// material at all; otherwise the bars are glass, as macOS draws them.
	light := cfg.Mode == settings.ModeLightweight

	c.Root().Background(p.Ground)

	ui.Row(c).Fill().Children(func() {
		s.sidebar(c, p, cfg)
		ui.Column(c).Grow(1).Children(func() {
			s.toolbar(c, p, cfg, st, light)
			s.content(c, p, cfg, st)
		})
	})
}

// sidebar is the navigation column. macOS puts a translucent material behind
// it; in lightweight mode it is a plain tone step instead.
func (s *Shell) sidebar(c *ui.Context, p palette, cfg settings.Settings) {
	bar := ui.Column(c).Width(228).FillHeight().Padding(16, 14).Gap(2)
	if cfg.Mode == settings.ModeLightweight {
		bar.Background(p.SurfaceHi)
	} else {
		bar.Material(glass.Glass{Style: glass.Regular})
	}
	bar.Children(func() {
		ui.Row(c).Gap(9).Padding(6, 6, 6, 14).Children(func() {
			ui.Text(c, "AirFlash").FontSize(16).Bold().TextColor(p.Ink)
		})

		s.navItem(c, p, "Now playing", pageNow)
		s.navItem(c, p, "Receivers", pageReceivers)
		s.navItem(c, p, "Settings", pageSettings)

		ui.Spacer(c).Grow(1)

		// The engine's own state, always visible: whether audio is flowing is
		// the one fact the user needs at a glance.
		st := s.deps.State.Snapshot()
		label, col := phaseLabel(p, st.Phase)
		ui.Column(c).Padding(6).Gap(3).Children(func() {
			ui.Row(c).Gap(7).Children(func() {
				ui.Box(c).Width(8).Height(8).Radius(4).Background(col)
				ui.Text(c, label).FontSize(12).TextColor(p.InkMuted)
			})
			if cfg.Mode == settings.ModeLightweight {
				ui.Text(c, "lightweight").FontSize(11).TextColor(p.InkMuted)
			}
		})
	})
}

func (s *Shell) navItem(c *ui.Context, p palette, label string, target page) {
	selected := s.page == target
	row := ui.Row(c).FillWidth().Padding(8, 10).Radius(7).Gap(8)
	if selected {
		row.Background(p.Accent)
	}
	row.Children(func() {
		col := p.Ink
		if selected {
			col = p.AccentText
		}
		ui.Text(c, label).FontSize(13).TextColor(col)
	})
	if row.Clicked() {
		s.page = target
	}
}

// toolbar is the bar above the content. It floats over what scrolls under it,
// which is exactly what macOS's scroll edge effect is for.
func (s *Shell) toolbar(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics, light bool) {
	bar := ui.Row(c).FillWidth().Height(52).Padding(18, 0).Gap(10)
	if light {
		bar.Background(p.Ground)
	} else {
		bar.Material(glass.Glass{Style: glass.Regular})
	}
	bar.Children(func() {
		title := "Now playing"
		switch s.page {
		case pageReceivers:
			title = "Receivers"
		case pageSettings:
			title = "Settings"
		}
		ui.Text(c, title).FontSize(15).Bold().TextColor(p.Ink)

		ui.Spacer(c).Grow(1)

		if st.Phase == engine.PhaseStreaming {
			if ui.Button(c, "Stop").Clicked() && s.deps.OnStop != nil {
				s.deps.OnStop()
			}
		}
	})
}

func (s *Shell) content(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	body := ui.Scroll(c).Fill().Padding(24, 22)
	body.Children(func() {
		switch s.page {
		case pageNow:
			s.nowPage(c, p, cfg, st)
		case pageReceivers:
			s.receiversPage(c, p, cfg)
		case pageSettings:
			s.settingsPage(c, p, cfg)
		}
	})
}

// nowPage is the one thing the user opened the window to see: is audio
// flowing, how far behind is it, and one control to start or stop.
func (s *Shell) nowPage(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	ui.Column(c).FillWidth().Gap(18).Children(func() {
		s.measurement(c, p, cfg, st)
		if cfg.ShowTrend {
			s.trend(c, p, cfg, st)
		}
		s.controls(c, p, cfg, st)
	})
}

// targetMs is the latency the user asked for. Before a stream starts the
// engine has reported nothing, so the configured value is the honest answer.
func targetMs(cfg settings.Settings, st engine.Metrics) float64 {
	if st.RequestedMs > 0 {
		return float64(st.RequestedMs)
	}
	return float64(cfg.LatencyMs)
}

// measurement is the requested-versus-arrived pair, set large. Two numbers and
// one word carry the whole screen.
func (s *Shell) measurement(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	target := targetMs(cfg, st)
	band := bandFor(p, target, st.MeasuredMs, st.HasMeasured)

	card := ui.Column(c).FillWidth().Padding(20).Gap(14).Radius(14).Background(p.Surface)
	card.Children(func() {
		ui.Row(c).Gap(48).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "Target").FontSize(12).TextColor(p.InkMuted)
				ui.Text(c, fmt.Sprintf("%.0f", target)).FontSize(44).Bold().TextColor(p.Ink)
				ui.Text(c, "milliseconds").FontSize(12).TextColor(p.InkMuted)
			})
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "Measured").FontSize(12).TextColor(p.InkMuted)
				if st.HasMeasured {
					ui.Text(c, fmt.Sprintf("%.0f", st.MeasuredMs)).FontSize(44).Bold().TextColor(band.Color)
				} else {
					ui.Text(c, "—").FontSize(44).Bold().TextColor(p.InkMuted)
				}
				ui.Text(c, "milliseconds").FontSize(12).TextColor(p.InkMuted)
			})
		})

		ui.Row(c).Gap(8).Children(func() {
			ui.Box(c).Width(8).Height(8).Radius(4).Background(band.Color)
			ui.Text(c, band.Label).FontSize(13).TextColor(p.InkMuted)
		})
	})
}

// trend draws the measured latency over time, with the target as a reference
// line so a drift away from it is visible without reading numbers.
func (s *Shell) trend(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	card := ui.Column(c).FillWidth().Padding(20).Gap(10).Radius(14).Background(p.Surface)
	card.Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "Latency over time").FontSize(13).TextColor(p.InkMuted)
			if !st.HasMeasured {
				ui.Text(c, "· no readings yet, start playback").FontSize(12).TextColor(p.InkMuted)
			}
		})

		plot := ui.Box(c).FillWidth().Height(104).Radius(8).Background(p.SurfaceHi)
		plot.Draw(func(pt *ui.Painter, r ui.Rect) {
			hist := st.History
			target := targetMs(cfg, st)

			// The scale always includes the target, so the line never leaves
			// the frame and the target's position stays comparable between
			// frames.
			lo, hi := target*0.8, target*1.2
			for _, v := range hist {
				if v < lo {
					lo = v
				}
				if v > hi {
					hi = v
				}
			}
			if hi-lo < 1 {
				hi = lo + 1
			}
			yOf := func(v float64) float32 {
				f := (v - lo) / (hi - lo)
				return r.Y + r.H - float32(f)*r.H
			}

			// The target reference.
			ty := yOf(target)
			pt.Line(r.X, ty, r.X+r.W, ty, 1, p.Hairline)

			if len(hist) < 2 {
				return
			}
			step := r.W / float32(len(hist)-1)
			prevX, prevY := r.X, yOf(hist[0])
			for i := 1; i < len(hist); i++ {
				x, y := r.X+float32(i)*step, yOf(hist[i])
				pt.Line(prevX, prevY, x, y, 1.5, p.Accent)
				prevX, prevY = x, y
			}
		})
	})
}

// controls are the two things that actually change playback.
func (s *Shell) controls(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	card := ui.Column(c).FillWidth().Padding(22).Gap(18).Radius(14).Background(p.Surface)
	card.Children(func() {
		// Gain.
		gain := cfg.Gain
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, fmt.Sprintf("Volume  %.0f%%", gain*100)).FontSize(13).TextColor(p.Ink)
			ui.Slider(c, &gain, 0, 1).FillWidth().OnChange(func() {
				if s.deps.OnGain != nil {
					s.deps.OnGain(gain)
				}
			})
		})

		// Latency target. Changing it mid-stream is not offered: the engine
		// takes it when a stream starts.
		target := float64(cfg.LatencyMs)
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, fmt.Sprintf("Latency target  %.0f ms", target)).FontSize(13).TextColor(p.Ink)
				if st.Phase == engine.PhaseStreaming {
					ui.Text(c, "· applies to the next start").FontSize(11).TextColor(p.InkMuted)
				}
			})
			ui.Slider(c, &target, 200, 2000).FillWidth().OnChange(func() {
				if s.deps.OnLatency != nil {
					s.deps.OnLatency(int(math.Round(target)))
				}
			})
		})

		// The action.
		ui.Row(c).Gap(10).Children(func() {
			streaming := st.Phase == engine.PhaseStreaming
			label := "Play"
			if streaming {
				label = "Stop"
			}
			if ui.PrimaryButton(c, label).Clicked() {
				if streaming {
					if s.deps.OnStop != nil {
						s.deps.OnStop()
					}
				} else if peers := s.deps.Peers(); len(peers) > 0 && s.deps.OnStart != nil {
					s.deps.OnStart(peers[0])
				}
			}
		})
	})
}

func (s *Shell) receiversPage(c *ui.Context, p palette, cfg settings.Settings) {
	peers := s.deps.Peers()
	card := ui.Column(c).FillWidth().Padding(22).Gap(14).Radius(14).Background(p.Surface)
	card.Children(func() {
		if len(peers) == 0 {
			ui.Text(c, "No receiver yet. Connect the PC and the HomePod to the same network.").
				FontSize(13).TextColor(p.InkMuted)
			return
		}
		for _, peer := range peers {
			row := ui.Row(c).FillWidth().Padding(10).Radius(9).Gap(10)
			row.Background(p.SurfaceHi)
			row.Children(func() {
				ui.Column(c).Gap(3).Children(func() {
					ui.Text(c, peer.Host).FontSize(13).TextColor(p.Ink)
					ui.Text(c, fmt.Sprintf("port %d", peer.Port)).FontSize(11).TextColor(p.InkMuted)
				})
				ui.Spacer(c).Grow(1)
				if ui.Button(c, "Play").Clicked() && s.deps.OnStart != nil {
					s.deps.OnStart(peer)
				}
			})
		}
	})
}

// settingsPage holds the mode switch, which is the one setting that changes
// what the app costs to run.
func (s *Shell) settingsPage(c *ui.Context, p palette, cfg settings.Settings) {
	card := ui.Column(c).FillWidth().Padding(22).Gap(18).Radius(14).Background(p.Surface)
	card.Children(func() {
		light := cfg.Mode == settings.ModeLightweight
		ui.Column(c).Gap(6).Children(func() {
			ui.Switch(c, &light).OnChange(func() {
				if s.deps.OnMode == nil {
					return
				}
				if light {
					s.deps.OnMode(settings.ModeLightweight)
				} else {
					s.deps.OnMode(settings.ModeStandard)
				}
			})
			ui.Text(c, "Lightweight mode").FontSize(13).TextColor(p.Ink)
			ui.Text(c, "Drops the glass and the animation, redraws only when a reading changes, "+
				"and samples the engine once a second. The audio path is untouched: the engine "+
				"keeps its own process either way.").FontSize(12).TextColor(p.InkMuted)
		})

		ui.Divider(c)

		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, "Closing the window").FontSize(13).TextColor(p.Ink)
			ui.Text(c, "Closing releases the window and everything it holds. Playback continues: "+
				"the engine is a separate process, and the app stays in the tray.").
				FontSize(12).TextColor(p.InkMuted)
		})
	})
}

// phaseLabel turns an engine phase into a word and a colour.
func phaseLabel(p palette, phase engine.StreamPhase) (string, ui.Color) {
	switch phase {
	case engine.PhaseStreaming:
		return "Playing", p.Success
	case engine.PhaseConnecting, engine.PhaseNegotiating:
		return "Connecting", p.Warning
	case engine.PhaseFailed:
		return "Failed", p.Danger
	case engine.PhaseStopped:
		return "Stopped", p.InkMuted
	default:
		return "Idle", p.InkMuted
	}
}

// ensure the mygo import is used for the window options the app passes.
var _ = mygo.App
