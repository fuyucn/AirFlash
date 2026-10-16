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
//
// The window keeps its own title bar, so there is no second one drawn inside
// it: the platform already says what the window is and lets it be dragged. The
// sidebar and the content are the whole interface.
func (s *Shell) View(c *ui.Context) {
	st := s.deps.State.Snapshot()
	cfg := s.deps.Settings.Current()
	isDark := c.Theme().Dark
	p := forDark(isDark)

	// The framework draws its own widgets with its own theme; give it the
	// palette, so the sliders and switches match the rest of the window.
	c.SetTheme(themeFor(p, isDark))
	c.Root().Background(p.Ground)

	ui.Row(c).Fill().Children(func() {
		s.sidebar(c, p, cfg)
		ui.Column(c).Grow(1).FillHeight().Children(func() {
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

func (s *Shell) content(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	body := ui.Scroll(c).Grow(1).Padding(28, 24)
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

// statsPage is the one thing the user opened the window to see: whether audio
// is flowing, how far behind it is, and the counters that explain a stutter.
//
// It is laid out the way a system statistics window is - a headline figure,
// then label-and-value rows separated by space rather than by cards. Cards
// inside a window are a web idiom; a desktop window is already one surface.
func (s *Shell) statsPage(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	ui.Column(c).FillWidth().Gap(28).Children(func() {
		s.headline(c, p, cfg, st)
		if cfg.ShowTrend {
			s.trend(c, p, cfg, st)
		}
		s.controls(c, p, cfg, st)
		s.diagnostics(c, p, cfg, st)
	})
}

// headline is the current state in one line of large figures, with the parts
// that add up to it underneath.
func (s *Shell) headline(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	target := targetMs(cfg, st)

	ui.Column(c).FillWidth().Gap(14).Children(func() {
		// The audible delay, set large. This is what the user came to see.
		ui.Row(c).Gap(14).AlignItems(ui.End).Children(func() {
			if st.HasTotalLatency {
				ui.Text(c, fmt.Sprintf("%.0f", st.TotalLatencyMs)).FontSize(sizeHero).Bold().
					TextColor(p.Ink)
				ui.Text(c, "ms").FontSize(sizeLead).TextColor(p.InkMuted)
				band := bandFor(p, target, st.TotalLatencyMs, true)
				ui.Text(c, band.Label).FontSize(sizeBody).TextColor(band.Color)
			} else {
				ui.Text(c, "—").FontSize(sizeHero).Bold().TextColor(p.InkMuted)
				ui.Text(c, "waiting for playback").FontSize(sizeBody).TextColor(p.InkMuted)
			}
		})

		// The parts, as label-and-value rows.
		s.metricRow(c, p, "Target", fmt.Sprintf("%.0f ms", target), p.Ink)
		if st.HasReceiverBuffer {
			label := "Receiver buffer"
			if st.ReceiverBufferEstimated {
				label = "Receiver buffer (estimated)"
			}
			s.metricRow(c, p, label, fmt.Sprintf("%.0f ms", st.ReceiverBufferMs), p.Ink)
		}
		if st.HasNetworkRTT {
			s.metricRow(c, p, "Network round trip",
				fmt.Sprintf("%.0f ms", st.NetworkRTTMs), rttColor(p, st.NetworkRTTMs))
		}
	})
}

// metricRow is one label on the left and its value on the right, the way a
// system inspector lists things. The two are far apart so the eye can compare
// values down the column without reading the labels again.
func (s *Shell) metricRow(c *ui.Context, p palette, label, value string, valueColor ui.Color) {
	ui.Row(c).FillWidth().Children(func() {
		ui.Text(c, label).FontSize(sizeBody).TextColor(p.InkMuted)
		ui.Spacer(c).Grow(1)
		ui.Text(c, value).FontSize(sizeBody).TextColor(valueColor)
	})
}

// trend draws the latency over time against the target, so a drift away from it
// is visible without reading numbers. It is a section of the page, not a card:
// a label, the plot, and the scale, separated by space.
func (s *Shell) trend(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	target := float64(cfg.EffectiveLatencyMs(s.adapted()))

	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Row(c).FillWidth().Children(func() {
			ui.Text(c, "Latency over time").FontSize(sizeBody).TextColor(p.InkMuted)
			ui.Spacer(c).Grow(1)
			if st.HasTotalLatency && len(st.History) > 0 {
				ui.Text(c, fmt.Sprintf("%.0f ms now", st.History[len(st.History)-1])).
					FontSize(sizeBody).TextColor(p.Ink)
			}
		})

		if !st.HasTotalLatency {
			ui.Text(c, "The measured latency appears here once playback starts.").
				FontSize(sizeBody).TextColor(p.InkMuted)
			return
		}

		hist := st.History
		plot := ui.Box(c).FillWidth().Height(120).Background(p.SurfaceHi)
		plot.Draw(func(pt *ui.Painter, r ui.Rect) {
			drawLatencyPlot(pt, r, p, hist, target)
		})

		lo, hi := plotRange(hist, target)
		ui.Row(c).FillWidth().Children(func() {
			ui.Text(c, fmt.Sprintf("%.0f ms", lo)).FontSize(sizeSmall).TextColor(p.InkMuted)
			ui.Spacer(c).Grow(1)
			ui.Text(c, fmt.Sprintf("target %d ms", int(target))).FontSize(sizeSmall).TextColor(p.InkMuted)
			ui.Spacer(c).Grow(1)
			ui.Text(c, fmt.Sprintf("%.0f ms", hi)).FontSize(sizeSmall).TextColor(p.InkMuted)
		})
	})
}

// targetMs is the latency the user asked for. Before a stream starts the engine
// has reported nothing, so the configured value is the honest answer.
func targetMs(cfg settings.Settings, st engine.Metrics) float64 {
	if st.RequestedMs > 0 {
		return float64(st.RequestedMs)
	}
	return float64(cfg.LatencyMs)
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

// plotRange is the vertical range a latency plot is drawn over.
//
// It spans what was actually measured rather than a fixed band around the
// target, so the shape of the series fills the plot instead of sitting in a
// thin strip. The target is always included, and a margin keeps the extremes
// off the edges.
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

// buckets reduces a series to about as many points as the plot has pixels to
// show them in.
//
// A reading arrives four times a second, so a few minutes is hundreds of
// points in a few hundred pixels: drawn one per point the line becomes a picket
// fence and says nothing. Each bucket keeps its own extreme, so a spike still
// shows rather than being averaged away.
func buckets(hist []float64, width float32) []float64 {
	want := int(width)
	if want < 8 {
		want = 8
	}
	if len(hist) <= want {
		return hist
	}
	size := float64(len(hist)) / float64(want)
	out := make([]float64, 0, want)
	for i := 0; i < want; i++ {
		lo := int(float64(i) * size)
		hi := int(float64(i+1) * size)
		if hi > len(hist) {
			hi = len(hist)
		}
		if lo >= hi {
			continue
		}
		// The extreme rather than the mean: a spike is the thing worth seeing.
		peak := hist[lo]
		for _, v := range hist[lo:hi] {
			if v > peak {
				peak = v
			}
		}
		out = append(out, peak)
	}
	return out
}

// drawLatencyPlot draws the series and the target reference.
//
// The reference is drawn in the muted ink rather than a hairline: it is the line
// everything is compared against, so it has to be visible. Two or three places
// mark where the reading went over the target, which is the audible gap.
func drawLatencyPlot(pt *ui.Painter, r ui.Rect, p palette, hist []float64, target float64) {
	if len(hist) == 0 {
		return
	}
	series := buckets(hist, r.W/9)
	lo, hi := plotRange(series, target)
	yOf := func(v float64) float32 {
		f := (v - lo) / (hi - lo)
		return r.Y + r.H - float32(f)*r.H
	}

	ty := yOf(target)
	pt.Line(r.X, ty, r.X+r.W, ty, 1, p.InkMuted)

	if len(series) < 2 {
		return
	}
	step := r.W / float32(len(series)-1)

	// The over-target marks first, so the line reads on top of them.
	for i, v := range series {
		if v <= target {
			continue
		}
		x := r.X + float32(i)*step
		pt.Line(x, yOf(v), x, ty, 1, p.Danger)
	}

	prevX, prevY := r.X, yOf(series[0])
	for i := 1; i < len(series); i++ {
		x, y := r.X+float32(i)*step, yOf(series[i])
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
	ui.Column(c).FillWidth().Gap(20).Children(func() {
		// Volume, as a labelled slider the width of the content.
		gain := cfg.Gain
		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, fmt.Sprintf("Volume  %.0f%%", gain*100)).
				FontSize(sizeBody).TextColor(p.InkMuted)
			ui.Slider(c, &gain, 0, 1).FillWidth().OnChange(func() {
				if s.deps.OnGain != nil {
					s.deps.OnGain(gain)
				}
			})
		})

		// Latency: Auto, or a fixed choice. Auto is the default because the
		// number depends on the network at the time, which the user cannot know.
		s.latencyControl(c, p, cfg, st)

		// One action, on its own, where the eye ends after reading the numbers.
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

	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Row(c).FillWidth().Children(func() {
			ui.Text(c, "Losses").FontSize(sizeBody).TextColor(p.InkMuted)
			ui.Spacer(c).Grow(1)
			if unhealthy {
				ui.Text(c, "something is being lost").FontSize(sizeBody).TextColor(p.Warning)
			} else {
				ui.Text(c, "nothing lost so far").FontSize(sizeBody).TextColor(p.Success)
			}
		})

		// Counters as label-and-value rows, so they line up in one column and
		// can be compared by eye. A zero stays quiet; anything else is called
		// out, because every one of these is an audible gap.
		s.countRow(c, p, "Sender fell behind", st.SenderLateRecoveries)
		s.countRow(c, p, "Audio slots skipped", st.SkippedPackets)
		s.countRow(c, p, "Lost past recovery", losses)
		s.countRow(c, p, "Packets dropped on send", st.MediaSendDropped)
		s.countRow(c, p, "Retransmit queue overflow", st.RetransmitQueueDrops)

		if st.FeedbackDelayed {
			ui.Text(c, "The receiver's feedback is late, which is what a rising ping looks like.").
				FontSize(sizeBody).TextColor(p.Warning)
		}
	})
}

// countRow is one counter. A zero is quiet; a non-zero takes the warning colour
// and says what it means, because these counters are the stutter.
func (s *Shell) countRow(c *ui.Context, p palette, label string, value uint64) {
	col := p.InkMuted
	if value > 0 {
		col = p.Warning
	}
	ui.Row(c).FillWidth().Children(func() {
		ui.Text(c, label).FontSize(sizeBody).TextColor(p.InkMuted)
		ui.Spacer(c).Grow(1)
		ui.Text(c, fmt.Sprintf("%d", value)).FontSize(sizeBody).TextColor(col)
	})
}

func (s *Shell) receiversPage(c *ui.Context, p palette, cfg settings.Settings) {
	var found []discovery.Receiver
	if s.deps.Receivers != nil {
		found = s.deps.Receivers()
	}

	ui.Column(c).FillWidth().Gap(14).Children(func() {
		if len(found) == 0 {
			ui.Text(c, "Looking for receivers…").FontSize(sizeBody).TextColor(p.Ink)
			ui.Text(c, "The PC and the HomePod have to be on the same network.").
				FontSize(sizeBody).TextColor(p.InkMuted)
			return
		}

		for _, r := range found {
			rec := r
			row := ui.Row(c).FillWidth().Padding(12, 14).Gap(12)
			row.Children(func() {
				ui.Column(c).Gap(3).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, rec.Name).FontSize(sizeBody).TextColor(p.Ink)
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
					if ui.Button(c, "Play").Clicked() && s.deps.OnPlay != nil {
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
	ui.Column(c).FillWidth().Gap(26).Children(func() {
		// The mode switch, which is the one setting that changes what the app
		// costs to run.
		ui.Column(c).Gap(8).Children(func() {
			light := cfg.Mode == settings.ModeLightweight
			ui.Row(c).FillWidth().Children(func() {
				ui.Text(c, "Lightweight mode").FontSize(sizeBody).TextColor(p.Ink)
				ui.Spacer(c).Grow(1)
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
			})
			ui.Text(c, "Drops the window material and the trend chart, and samples the engine "+
				"once a second. The audio path is untouched: the engine keeps its own process "+
				"either way.").FontSize(sizeSmall).TextColor(p.InkMuted)
		})

		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, "Closing the window").FontSize(sizeBody).TextColor(p.Ink)
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
