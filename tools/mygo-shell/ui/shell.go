package ui

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/discovery"
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
	Receivers func() []discovery.Receiver
	OnStart   func(engine.Peer)
	OnPlay    func(discovery.Receiver)
	OnStop    func()
	OnGain    func(float64)
	OnLatency func(int)
	OnAuto    func(bool)
	AdaptedMs func() int
	OnMode    func(settings.Mode)
	OnClose   func()
	OnQuit    func()
}

// page identifies the content shown beside the sidebar.
type page int

const (
	pageStats page = iota
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

// SetPage selects which page the window shows. It exists so a caller can open
// the window on a particular page, and so a render check can look at one
// without clicking through the sidebar.
func (s *Shell) SetPage(name string) {
	switch name {
	case "receivers":
		s.page = pageReceivers
	case "settings":
		s.page = pageSettings
	default:
		s.page = pageStats
	}
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
			ui.Text(c, "AirFlash").FontSize(sizeTitle).Bold().TextColor(p.Ink)
		})

		s.navItem(c, p, "Stats", pageStats)
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
				ui.Text(c, label).FontSize(sizeSmall).TextColor(p.InkMuted)
			})
			if cfg.Mode == settings.ModeLightweight {
				ui.Text(c, "lightweight").FontSize(sizeSmall).TextColor(p.InkMuted)
			}
		})
	})
}

func (s *Shell) navItem(c *ui.Context, p palette, label string, target page) {
	selected := s.page == target
	// Selection is shown by a step in tone, not by the accent: the accent is
	// reserved for the action and the live reading, so it keeps its meaning.
	row := ui.Row(c).FillWidth().Padding(8, 10).Radius(7).Gap(8)
	if selected {
		row.Background(p.SurfaceHi)
	}
	row.Children(func() {
		col := p.InkMuted
		if selected {
			col = p.Ink
		}
		ui.Text(c, label).FontSize(sizeSmall).TextColor(col)
		if selected {
			ui.Spacer(c).Grow(1)
			ui.Box(c).Width(3).Height(14).Radius(2).Background(p.Accent)
		}
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
		title := "Stats"
		switch s.page {
		case pageReceivers:
			title = "Receivers"
		case pageSettings:
			title = "Settings"
		}
		ui.Text(c, title).FontSize(sizeTitle).Bold().TextColor(p.Ink)

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
		case pageStats:
			s.statsPage(c, p, cfg, st)
		case pageReceivers:
			s.receiversPage(c, p, cfg)
		case pageSettings:
			s.settingsPage(c, p, cfg)
		}
	})
}

// nowPage is the one thing the user opened the window to see: is audio
// flowing, how far behind is it, and one control to start or stop.
func (s *Shell) statsPage(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	ui.Column(c).FillWidth().Gap(18).Children(func() {
		s.measurement(c, p, cfg, st)
		if cfg.ShowTrend {
			s.trend(c, p, cfg, st)
		}
		s.controls(c, p, cfg, st)
		s.diagnostics(c, p, cfg, st)
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

	card := ui.Column(c).FillWidth().Padding(20).Gap(16).Radius(14).Background(p.Surface)
	card.Children(func() {
		ui.Row(c).Gap(44).Children(func() {
			// The target is what the user chose.
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "Target").FontSize(sizeSmall).TextColor(p.InkMuted)
				ui.Text(c, fmt.Sprintf("%.0f", target)).FontSize(sizeFigure).Bold().TextColor(p.Ink)
				ui.Text(c, "milliseconds").FontSize(sizeSmall).TextColor(p.InkMuted)
			})

			// The receiver's buffer is the biggest part of what is heard.
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "Receiver buffer").FontSize(sizeSmall).TextColor(p.InkMuted)
				if st.HasReceiverBuffer {
					ui.Text(c, fmt.Sprintf("%.0f", st.ReceiverBufferMs)).FontSize(sizeFigure).Bold().
						TextColor(p.Ink)
					label := "milliseconds"
					if st.ReceiverBufferEstimated {
						label = "milliseconds, estimated"
					}
					ui.Text(c, label).FontSize(sizeSmall).TextColor(p.InkMuted)
				} else {
					ui.Text(c, "—").FontSize(sizeFigure).Bold().TextColor(p.InkMuted)
					ui.Text(c, "waiting for playback").FontSize(sizeSmall).TextColor(p.InkMuted)
				}
			})

			// The network's round trip: what moves when the ping moves.
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "Network round trip").FontSize(sizeSmall).TextColor(p.InkMuted)
				if st.HasNetworkRTT {
					ui.Text(c, fmt.Sprintf("%.0f", st.NetworkRTTMs)).FontSize(sizeFigure).Bold().
						TextColor(rttColor(p, st.NetworkRTTMs))
					ui.Text(c, "milliseconds").FontSize(sizeSmall).TextColor(p.InkMuted)
				} else {
					ui.Text(c, "—").FontSize(sizeFigure).Bold().TextColor(p.InkMuted)
					ui.Text(c, "not measured yet").FontSize(sizeSmall).TextColor(p.InkMuted)
				}
			})
		})

		// What the user actually hears, as far as the parts can say.
		if st.HasTotalLatency {
			band := bandFor(p, target, st.TotalLatencyMs, true)
			ui.Row(c).Gap(8).Children(func() {
				ui.Box(c).Width(8).Height(8).Radius(4).Background(band.Color)
				ui.Text(c, fmt.Sprintf("About %.0f ms from capture to playback · %s",
					st.TotalLatencyMs, band.Label)).FontSize(sizeSmall).TextColor(p.InkMuted)
			})
		} else {
			ui.Text(c, "Start playback and the measured delay appears here.").
				FontSize(sizeSmall).TextColor(p.InkMuted)
		}
	})
}

// rttColor flags a round trip high enough to threaten the jitter budget. It is
// a report, not a grade: a slow network is a fact, not a mistake.
func rttColor(p palette, rtt float64) ui.Color {
	switch {
	case rtt <= 20:
		return p.Success
	case rtt <= 60:
		return p.Warning
	default:
		return p.Danger
	}
}

// trend draws the measured latency over time, with the target as a reference
// line so a drift away from it is visible without reading numbers.
// trend draws the session's latency over time, against the target, so a drift
// away from it is visible without reading numbers.
func (s *Shell) trend(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	if !st.HasTotalLatency {
		card := ui.Column(c).FillWidth().Padding(20).Gap(4).Radius(14).Background(p.Surface)
		card.Children(func() {
			ui.Text(c, "Latency over time").FontSize(sizeSmall).TextColor(p.Ink)
			ui.Text(c, "The measured latency appears here once playback starts, "+
				"against the target you set.").FontSize(sizeSmall).TextColor(p.InkMuted)
		})
		return
	}

	target := float64(cfg.EffectiveLatencyMs(s.adapted()))
	hist := st.History

	card := ui.Column(c).FillWidth().Padding(20).Gap(10).Radius(14).Background(p.Surface)
	card.Children(func() {
		// The headline: the latest reading, set large.
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "Latency over time").FontSize(sizeSmall).TextColor(p.InkMuted)
			ui.Spacer(c).Grow(1)
			ui.Text(c, fmt.Sprintf("%.0f ms now", hist[len(hist)-1])).
				FontSize(sizeSmall).Bold().TextColor(p.Ink)
		})

		plot := ui.Box(c).FillWidth().Height(140).Radius(8).Background(p.SurfaceHi)
		plot.Draw(func(pt *ui.Painter, r ui.Rect) {
			drawLatencyPlot(pt, r, p, hist, target)
		})

		// A scale the reader can use, and what the reference line means.
		lo, hi := plotRange(hist, target)
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, fmt.Sprintf("%.0f ms", lo)).FontSize(sizeSmall).TextColor(p.InkMuted)
			ui.Spacer(c).Grow(1)
			ui.Text(c, fmt.Sprintf("target %d ms", int(target))).FontSize(sizeSmall).TextColor(p.InkMuted)
			ui.Spacer(c).Grow(1)
			ui.Text(c, fmt.Sprintf("%.0f ms", hi)).FontSize(sizeSmall).TextColor(p.InkMuted)
		})
	})
}

// plotRange is the vertical range a latency plot is drawn over.
//
// It spans what was actually measured rather than a fixed band around the
// target, so the shape of the series fills the plot instead of sitting in a
// thin strip. The target is always included, so the reference line is never
// off-screen, and a small margin keeps the extremes off the edges.
func plotRange(hist []float64, target float64) (lo, hi float64) {
	lo, hi = target, target
	for _, v := range hist {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}

	// A quarter of the span, or 10 ms when the series is nearly flat, so a
	// steady line still has a readable band around it.
	margin := (hi - lo) / 4
	if margin < 10 {
		margin = 10
	}
	lo -= margin
	hi += margin

	if hi <= lo {
		hi = lo + 1
	}
	return lo, hi
}

// drawLatencyPlot draws the latency series and the target reference.
//
// The reference is drawn in the text colour rather than a hairline: it is the
// line the reader compares everything against, so it has to be visible. Marks
// are only drawn for readings that went over, which is the audible gap.
func drawLatencyPlot(pt *ui.Painter, r ui.Rect, p palette, hist []float64, target float64) {
	lo, hi := plotRange(hist, target)
	yOf := func(v float64) float32 {
		f := (v - lo) / (hi - lo)
		return r.Y + r.H - float32(f)*r.H
	}

	// The target reference, visible against the plot's own background.
	ty := yOf(target)
	pt.Line(r.X, ty, r.X+r.W, ty, 1, p.InkMuted)

	if len(hist) < 2 {
		return
	}
	step := r.W / float32(len(hist)-1)

	// Readings that went over the target, marked behind the line so the shape
	// of the series still reads.
	for i, v := range hist {
		if v <= target {
			continue
		}
		x := r.X + float32(i)*step
		pt.Line(x, yOf(v), x, ty, 1, p.Danger)
	}

	// The series, drawn as segments so a spike keeps its shape.
	prevX, prevY := r.X, yOf(hist[0])
	for i := 1; i < len(hist); i++ {
		x, y := r.X+float32(i)*step, yOf(hist[i])
		pt.Line(prevX, prevY, x, y, 1.5, p.Accent)
		prevX, prevY = x, y
	}
}

// adapted is what the shell worked out from previous sessions.
func (s *Shell) adapted() int {
	if s.deps.AdaptedMs == nil {
		return 0
	}
	return s.deps.AdaptedMs()
}

// controls are the two things that actually change playback.
func (s *Shell) controls(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	card := ui.Column(c).FillWidth().Padding(22).Gap(18).Radius(14).Background(p.Surface)
	card.Children(func() {
		// Gain.
		gain := cfg.Gain
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, fmt.Sprintf("Volume  %.0f%%", gain*100)).FontSize(sizeSmall).TextColor(p.Ink)
			ui.Slider(c, &gain, 0, 1).FillWidth().OnChange(func() {
				if s.deps.OnGain != nil {
					s.deps.OnGain(gain)
				}
			})
		})

		// Latency. Auto picks from what the network actually did; the choices
		// beside it are the floor Auto will not go below.
		s.latencyControl(c, p, cfg, st)

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

// latencyControl offers Auto and, beside it, the targets a user can pin.
//
// Auto is the default because the number is not something a user can know: it
// depends on the network at the time. The setting beside it is the floor Auto
// will not go below, which is the one thing only the user can say.
func (s *Shell) latencyControl(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	choices := settings.LatencyChoices()

	// The segmented control holds an index: 0 is Auto, then one per choice.
	labels := make([]string, 0, len(choices)+1)
	labels = append(labels, "Auto")
	for _, ms := range choices {
		labels = append(labels, fmt.Sprintf("%d", ms))
	}

	index := 0
	if !cfg.LatencyAuto {
		// A pinned setting that is not one of the presets shows as Auto until
		// the user picks again; the effective line below still states the truth.
		for i, ms := range choices {
			if ms == cfg.LatencyMs {
				index = i + 1
				break
			}
		}
	}

	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, "Latency").FontSize(sizeSmall).TextColor(p.Ink)
			ui.Text(c, "milliseconds").FontSize(sizeSmall).TextColor(p.InkMuted)
		})

		seg := ui.Segmented(c, &index, labels...)
		if seg.Changed() {
			if index == 0 {
				if s.deps.OnAuto != nil {
					s.deps.OnAuto(true)
				}
			} else if index-1 < len(choices) {
				if s.deps.OnAuto != nil {
					s.deps.OnAuto(false)
				}
				if s.deps.OnLatency != nil {
					s.deps.OnLatency(choices[index-1])
				}
			}
		}

		// What the engine will actually be asked for, so the control is never
		// a mystery.
		effective := cfg.EffectiveLatencyMs(s.adapted())
		line := fmt.Sprintf("The engine will be asked for %d ms", effective)
		if cfg.LatencyAuto {
			if effective > cfg.LatencyMs {
				line += fmt.Sprintf(" · Auto raised it from your floor of %d ms", cfg.LatencyMs)
			} else {
				line += " · Auto, at your floor"
			}
		}
		ui.Text(c, line).FontSize(sizeSmall).TextColor(p.InkMuted)

		if st.Phase == engine.PhaseStreaming {
			ui.Text(c, "A new target applies to the next start.").FontSize(sizeSmall).TextColor(p.InkMuted)
		}
	})
}

// diagnostics shows the counters that explain a stutter. They are only
// meaningful while something is playing, and in lightweight mode they are left
// out: the point of that mode is to stop the shell spending cycles on itself.
func (s *Shell) diagnostics(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	if st.Phase != engine.PhaseStreaming || cfg.Mode == settings.ModeLightweight {
		return
	}

	// A sender stall is the stutter itself: it fell behind and threw away
	// audio. Packet loss is what makes the receiver ask for it again.
	losses := st.RetransmitMissing + st.RetransmitExpired
	unhealthy := st.SenderLateRecoveries > 0 || st.SkippedPackets > 0 ||
		losses > 0 || st.RetransmitQueueDrops > 0 || st.MediaSendDropped > 0

	card := ui.Column(c).FillWidth().Padding(20).Gap(12).Radius(14).Background(p.Surface)
	card.Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "Diagnostics").FontSize(sizeSmall).TextColor(p.Ink)
			if unhealthy {
				ui.Text(c, "· something is being lost").FontSize(sizeSmall).TextColor(p.Warning)
			} else {
				ui.Text(c, "· nothing lost so far").FontSize(sizeSmall).TextColor(p.Success)
			}
		})

		// Two columns of counters, because these are read by comparing them.
		ui.Row(c).Gap(40).Children(func() {
			ui.Column(c).Gap(6).Children(func() {
				s.counter(c, p, "Sender fell behind", st.SenderLateRecoveries,
					"the sender discarded expired audio")
				s.counter(c, p, "Audio slots skipped", st.SkippedPackets,
					"each one is a gap you can hear")
				s.counter(c, p, "Packets dropped on send", st.MediaSendDropped, "")
			})
			ui.Column(c).Gap(6).Children(func() {
				s.counter(c, p, "Retransmit requests", st.RetransmitRequests,
					"the receiver asked for packets again")
				s.counter(c, p, "Lost past recovery", losses,
					"too late to resend, so the receiver played over the gap")
				s.counter(c, p, "Queue overflow", st.RetransmitQueueDrops, "")
			})
		})

		if st.FeedbackDelayed {
			ui.Text(c, "The receiver's feedback is late, which is what a rising ping looks like.").
				FontSize(sizeSmall).TextColor(p.Warning)
		}
	})
}

// counter draws one labelled number.
func (s *Shell) counter(c *ui.Context, p palette, label string, value uint64, hint string) {
	col := p.InkMuted
	if value > 0 {
		col = p.Warning
	}
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, label).FontSize(sizeSmall).TextColor(p.InkMuted)
		ui.Text(c, fmt.Sprintf("%d", value)).FontSize(sizeSmall).Bold().TextColor(col)
	})
	if hint != "" && value > 0 {
		ui.Text(c, hint).FontSize(sizeSmall).TextColor(p.InkMuted)
	}
}

func (s *Shell) receiversPage(c *ui.Context, p palette, cfg settings.Settings) {
	var found []discovery.Receiver
	if s.deps.Receivers != nil {
		found = s.deps.Receivers()
	}

	card := ui.Column(c).FillWidth().Padding(22).Gap(14).Radius(14).Background(p.Surface)
	card.Children(func() {
		if len(found) == 0 {
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "Looking for receivers…").FontSize(sizeSmall).TextColor(p.Ink)
				ui.Text(c, "The PC and the HomePod have to be on the same network.").
					FontSize(sizeSmall).TextColor(p.InkMuted)
			})
			return
		}

		for _, r := range found {
			rec := r
			row := ui.Row(c).FillWidth().Padding(12).Radius(9).Gap(12)
			row.Background(p.SurfaceHi)
			row.Children(func() {
				ui.Column(c).Gap(3).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, rec.Name).FontSize(sizeSmall).TextColor(p.Ink)
						if rec.IsGroup() && !rec.Complete() {
							ui.Text(c, "incomplete").FontSize(sizeSmall).TextColor(p.Warning)
						}
					})
					detail := rec.Address
					if rec.Model != "" {
						detail = rec.Model + " · " + rec.Address
					}
					if rec.IsGroup() {
						detail = fmt.Sprintf("stereo · %d/2 · %s", len(rec.Members), detail)
					}
					ui.Text(c, detail).FontSize(sizeSmall).TextColor(p.InkMuted)
				})

				ui.Spacer(c).Grow(1)

				// A pair with one half cannot be played to, so it offers no
				// action rather than one that would sound wrong.
				if rec.Complete() {
					if ui.PrimaryButton(c, "Play").Clicked() && s.deps.OnPlay != nil {
						s.deps.OnPlay(rec)
					}
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
			ui.Text(c, "Lightweight mode").FontSize(sizeSmall).TextColor(p.Ink)
			ui.Text(c, "Drops the glass and the animation, redraws only when a reading changes, "+
				"and samples the engine once a second. The audio path is untouched: the engine "+
				"keeps its own process either way.").FontSize(sizeSmall).TextColor(p.InkMuted)
		})

		ui.Divider(c)

		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, "Closing the window").FontSize(sizeSmall).TextColor(p.Ink)
			ui.Text(c, "Closing releases the window and everything it holds. Playback continues: "+
				"the engine is a separate process, and the app stays in the tray.").
				FontSize(sizeSmall).TextColor(p.InkMuted)
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
