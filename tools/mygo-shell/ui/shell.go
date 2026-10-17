package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/discovery"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/equalizer"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/i18n"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/localsound"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/settings"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/shell"
)

// Deps is everything the view reads and calls.
type Deps struct {
	// Words is the language the interface is drawn in. A nil translator draws
	// English, which is what every key already is.
	Words     i18n.Translator
	State     *engine.State
	Settings  *settings.Store
	Life      *shell.Lifecycle
	Receivers func() []discovery.Receiver
	OnPlay    func(discovery.Receiver)
	OnStop    func()
	OnGain    func(float64)
	OnLatency func(int)
	OnAuto    func(bool)
	AdaptedMs func() int
	OnMode    func(settings.Mode)
	OnClose   func()
	OnQuit    func()

	// OnDeviceVolume sets a receiver's own volume, which the engine applies to
	// the device rather than to the local mix.
	OnDeviceVolume func(discovery.Receiver, float64)
	// PlayingReceiver returns the receiver playback is currently going to, or
	// nil when nothing is playing.
	//
	// The engine streams to hosts, not to a receiver, so the caller is the one
	// that knows which receiver those hosts belong to. This is what the
	// window's action operates on and what the volume slider is shown for:
	// without it the action would fall back to whatever discovery listed
	// first, which is not necessarily what is actually playing.
	PlayingReceiver func() *discovery.Receiver
	// OnLanguage changes the language, which rebuilds the window so the change
	// is not half applied.
	OnLanguage func(string)
	// OnSampleRate changes the rate the stream is captured at.
	OnSampleRate func(int)
	// OnEqualizer reports that the equalizer changed, which a live stream is told
	// about at once and a new one inherits from settings.
	OnEqualizer func()
	// OnEqualizerEnabled turns the equalizer on or off.
	OnEqualizerEnabled func(bool)
	// OnEqualizerPreset sets the curve to a named preset.
	OnEqualizerPreset func(name string)
	// OnEqualizerBand moves one band by its index.
	OnEqualizerBand func(index int, db float64)
	// OnEqualizerPreamp moves the overall level.
	OnEqualizerPreamp func(db float64)
	// OnReceiverOption changes one field of one receiver's own settings. The
	// field is named rather than the whole struct, so changing one leaves the
	// others as they were.
	OnReceiverOption func(receiverID, field string, value any)
	// OnStartAtLogin turns the start-with-the-machine setting on or off.
	OnStartAtLogin func(bool)
	// OnDiscoveryInterface changes which adapter mDNS browses on, which is the
	// one network setting that cannot be fixed by retrying: a machine with a
	// VPN up finds its receivers on one adapter only.
	OnDiscoveryInterface func(id string)
	// OnReconnect turns the retry-a-dropped-session setting on or off.
	OnReconnect func(bool)
	// OnReconnectAttempts sets how many times to try.
	OnReconnectAttempts func(int)
	// OnMuteWhileStreaming silences the local speakers while a stream runs.
	OnMuteWhileStreaming func(bool)
	// OnOpenAtLaunch decides whether a launch by hand shows the window instead
	// of the tray panel.
	OnOpenAtLaunch func(bool)
	// OnCaptureEndpoint changes which audio endpoint the engine records from.
	OnCaptureEndpoint func(id string)
	// OnOpenWindow opens the main window on a named page, which is how the
	// panel reaches the full picture and the settings.
	OnOpenWindow func(string)
	// OnManualReceiver adds or removes a receiver the browse cannot find, which
	// is the only way to reach a device on another subnet. Adding reports back
	// through onProblem, because the app is the one that holds the rules: an
	// address that is not one, or one already in the list, is refused there and
	// the reason shown here.
	OnManualReceiver func(add bool, index int, receiver settings.ManualReceiver, onProblem func(string))
	// OnAlias names a receiver, or renames it. An empty name removes the name.
	OnAlias func(receiverID, name string)
	// OnPairPin sends the PIN the receiver asked for, which completes pairing.
	// It is the one thing a retry cannot get past on its own.
	OnPairPin func(pin string)
	// OnPair starts pairing with a receiver, which is where the PIN prompt
	// begins.
	OnPair func(receiver discovery.Receiver)
}

// page identifies the content shown beside the sidebar.
type page int

// The order is the order the menu shows them, and pageReceivers is first so the
// zero value of a page - a Shell that has not been told which page to show -
// opens on the receivers, which is what the window is opened for.
const (
	pageReceivers page = iota
	pageStats
	pageEqualizer
	pageSettings
)

// manualReceiver is the "add one by hand" form: the receiver being typed in,
// and whether the form is open.
type manualReceiver struct {
	// open says the form is showing. It opens on the button and closes on a
	// cancel or a completed add.
	open bool
	name string
	host string
	port string
	// problem is what the last attempt was refused for, shown under the form.
	problem string
}

// Shell is the app's view state that lives between frames.
type Shell struct {
	deps Deps
	page page
	// tuning is the receiver whose own settings are open, or "" when none is.
	// It is the window's, not the app's: it is a view state that lasts as long as
	// the window does, like the page.
	tuning string
	// adding is the receiver being typed in by hand, for a device the browse
	// cannot find. It is the window's for the same reason tuning is: a rebuild
	// should not throw away what was being typed.
	adding manualReceiver
	// entered is when the window opened, for the entrance animation.
	entered time.Time
}

// New builds the shell view.
func New(d Deps) *Shell {
	return &Shell{deps: d, entered: time.Now()}
}

// t translates one of the interface's strings.
func (s *Shell) t(english string) string { return s.deps.Words.T(english) }

// SetPage selects which page the window shows. It exists so a caller can open
// the window on a particular page, and so a render check can look at one
// without clicking through the sidebar.
func (s *Shell) SetPage(name string) {
	switch name {
	case "receivers":
		s.page = pageReceivers
	case "equalizer":
		s.page = pageEqualizer
	case "settings":
		s.page = pageSettings
	case "stats":
		s.page = pageStats
	default:
		// Receivers is the first page, not Stats: the window is opened to choose
		// what to play to or to look at a device, and Stats is a reading one
		// finds after. The panel is the main surface and answers "what am I
		// playing to"; the window answers "which one, and why is it stuttering".
		s.page = pageReceivers
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

	// A top bar over the content, not a sidebar beside it. A horizontal menu is
	// what a narrow window keeps: a 196 DIP sidebar beside a 400 DIP viewport
	// leaves the page a sliver, and on a small window the sidebar is the first
	// thing to go.
	ui.Column(c).Fill().Children(func() {
		s.topBar(c, p, cfg, st)
		s.content(c, p, cfg, st)
	})
}

// topBar is the horizontal menu: the product name, the pages, and the engine's
// state, in one row above the content.
//
// It is a Row rather than a Column because a top bar is what a narrow window
// keeps. A sidebar of a fixed width beside a 400 DIP viewport leaves the page a
// sliver, and the sidebar is the first thing to go when the window is small; a
// bar that wraps and shrinks does not.
func (s *Shell) topBar(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	bar := ui.Row(c).FillWidth().Height(38).Shrink(0).
		Padding(10, 18).Gap(18).Radius(0).
		Background(p.Surface)
	// The rule is under the bar, so the menu and the content meet at a dashed
	// line rather than at a change of tone alone.
	bar.BorderWidth(0, 0, 1, 0).BorderStyle(ui.BorderDashed).BorderColor(p.Hairline)

	bar.Children(func() {
		ui.Text(c, s.t("AIRFLASH")).FontSize(sizeSmall).Bold().TextColor(p.Ink).
			LetterSpacing(1.2)

		ui.Row(c).Grow(1).Gap(4).Children(func() {
			s.navItem(c, p, s.t("Receivers"), pageReceivers)
			s.navItem(c, p, s.t("Stats"), pageStats)
			s.navItem(c, p, s.t("Equalizer"), pageEqualizer)
			s.navItem(c, p, s.t("Settings"), pageSettings)
		})

		// The engine's own state, always visible: whether audio is flowing is the
		// one fact the user needs at a glance.
		label, col := phaseLabel(s.deps.Words, p, st.Phase)
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(8).Height(8).Radius(0).Background(col)
			ui.Text(c, label).FontSize(sizeSmall).TextColor(p.InkMuted)
			if cfg.Mode == settings.ModeLightweight {
				ui.Text(c, s.t("LIGHTWEIGHT")).FontSize(sizeSmall).TextColor(p.InkMuted).
					LetterSpacing(0.9)
			}
		})
	})
}

func (s *Shell) navItem(c *ui.Context, p palette, label string, target page) {
	selected := s.page == target
	// Selection is shown by a step in tone, not by the accent: the accent is
	// reserved for the action and the live reading, so it keeps its meaning. A
	// tab of a menu sits beside the others rather than filling the bar, so it is
	// sized to its label rather than stretched.
	row := ui.Row(c).Padding(6, 10).Radius(0).Gap(8).AlignItems(ui.Center)
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
			// The mark that says which tab is open, beside the label rather than
			// pushed to the far end of the bar.
			ui.Box(c).Width(6).Height(6).Radius(0).Background(p.Accent)
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
			s.receiversPage(c, p, cfg, st)
		case pageEqualizer:
			s.equalizerPage(c, p, cfg)
		case pageSettings:
			s.settingsPage(c, p, cfg, st)
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
			s.gaps(c, p, st)
		}
		s.controls(c, p, cfg, st)
		s.diagnostics(c, p, cfg, st)
	})
}

// gaps draws the session second by second, marking the seconds that lost
// audio.
//
// The latency chart shows the network and is always moving; this shows the
// audio, and is empty until something actually goes wrong. That is the
// question a stutter raises - "did it drop, and when?" - and a line of numbers
// cannot answer it: the shape of the timeline can.
func (s *Shell) gaps(c *ui.Context, p palette, st engine.Metrics) {
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Row(c).FillWidth().Children(func() {
			sectionLabel(s.deps.Words, c, p, "GAPS")
			ui.Spacer(c).Grow(1)
			ui.Text(c, gapSummary(s.deps.Words, st.Slots)).FontSize(sizeBody).
				TextColor(gapSummaryColor(p, st.Slots))
		})

		if len(st.Slots) == 0 {
			ui.Text(c, s.t("One mark per second of playback appears here. Nothing has been lost so far.")).
				FontSize(sizeBody).TextColor(p.InkMuted)
			return
		}

		ribbon := ui.Box(c).FillWidth().Height(34).Radius(0).Background(p.Surface)
		ribbon.Draw(func(pt *ui.Painter, r ui.Rect) {
			drawGapRibbon(pt, r, p, st.Slots)
		})

		ui.Row(c).FillWidth().Children(func() {
			ui.Text(c, fmt.Sprintf("%d s ago", len(st.Slots))).
				FontSize(sizeSmall).TextColor(p.InkMuted)
			ui.Spacer(c).Grow(1)
			ui.Text(c, s.t("now")).FontSize(sizeSmall).TextColor(p.InkMuted)
		})
	})
}

// gapSummary is the one line that answers whether anything was lost.
func gapSummary(words i18n.Translator, slots []engine.Slot) string {
	gapped := 0
	for _, s := range slots {
		if s.Gapped() {
			gapped++
		}
	}
	if len(slots) == 0 {
		return words.T("nothing measured yet")
	}
	if gapped == 0 {
		return fmt.Sprintf(words.T("none in the last %d s"), len(slots))
	}
	return fmt.Sprintf(words.T("%d of %d s lost audio"), gapped, len(slots))
}

func gapSummaryColor(p palette, slots []engine.Slot) ui.Color {
	if len(slots) == 0 {
		// Nothing has been measured, so nothing can be called fine: green here
		// would be a claim the session has not earned.
		return p.InkMuted
	}
	for _, s := range slots {
		if s.Gapped() {
			return p.Warning
		}
	}
	return p.Success
}

// drawGapRibbon draws one column per second: flat where the audio was whole,
// full height where a second lost something.
//
// A gap is drawn as a solid mark rather than a coloured point because it is a
// count, not a level: one skipped slot and twelve look the same on a timeline
// of seconds, and what matters is that the second was not clean.
func drawGapRibbon(pt *ui.Painter, r ui.Rect, p palette, slots []engine.Slot) {
	if len(slots) == 0 || r.W <= 0 {
		return
	}
	// One column per second, but never thinner than a visible mark, so a short
	// session does not draw a picket fence of hairlines.
	col := max(r.W/float32(len(slots)), 3)
	// The newest second is at the right edge, so the eye reads left to right as
	// the session ran.
	x := r.X + r.W - col
	for i := len(slots) - 1; i >= 0 && x+col > r.X; i-- {
		s := slots[i]
		if s.Gapped() {
			pt.Fill(ui.Rect{X: x, Y: r.Y, W: col - 1, H: r.H}, p.Warning, 0)
		} else {
			// A clean second is a low tick: enough to see the session running,
			// quiet enough that a gap stands out against it.
			pt.Fill(ui.Rect{X: x, Y: r.Y + r.H - 3, W: col - 1, H: 3}, p.Hairline, 0)
		}
		x -= col
	}
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
				ui.Text(c, s.t("ms")).FontSize(sizeLead).TextColor(p.InkMuted)
				band := bandFor(s.deps.Words, p, target, st.TotalLatencyMs, true)
				ui.Text(c, band.Label).FontSize(sizeBody).TextColor(band.Color)
			} else {
				ui.Text(c, s.t("—")).FontSize(sizeHero).Bold().TextColor(p.InkMuted)
				ui.Text(c, s.t("waiting for playback")).FontSize(sizeBody).TextColor(p.InkMuted)
			}
		})

		// The parts, as label-and-value rows.
		s.metricRow(c, p, s.t("Target"), fmt.Sprintf("%.0f ms", target), p.Ink)
		if st.HasReceiverBuffer {
			label := "Receiver buffer"
			if st.ReceiverBufferEstimated {
				label = "Receiver buffer (estimated)"
			}
			s.metricRow(c, p, s.t(label), fmt.Sprintf("%.0f ms", st.ReceiverBufferMs), p.Ink)
		}
		if st.HasNetworkRTT {
			s.metricRow(c, p, s.t("Network round trip"),
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
			sectionLabel(s.deps.Words, c, p, "LATENCY OVER TIME")
			ui.Spacer(c).Grow(1)
			if st.HasTotalLatency && len(st.History) > 0 {
				ui.Text(c, fmt.Sprintf("%.0f ms now", st.History[len(st.History)-1])).
					FontSize(sizeBody).TextColor(p.Ink)
			}
		})

		if !st.HasTotalLatency {
			ui.Text(c, s.t("The measured latency appears here once playback starts.")).
				FontSize(sizeBody).TextColor(p.InkMuted)
			return
		}

		hist := st.History
		plot := ui.Box(c).FillWidth().Height(132).Radius(0).Background(p.Surface)
		plot.Draw(func(pt *ui.Painter, r ui.Rect) {
			drawLatencyPlot(pt, r, p, hist, target)
		})

		lo, hi := plotRange(hist, target)
		ui.Row(c).FillWidth().Children(func() {
			ui.Text(c, fmt.Sprintf("%.0f ms", lo)).FontSize(sizeSmall).TextColor(p.InkMuted)
			ui.Spacer(c).Grow(1)
			ui.Text(c, fmt.Sprintf(s.t("target %d ms"), int(target))).FontSize(sizeSmall).TextColor(p.InkMuted)
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
// bucket is one step of a reduced series.
//
// The mean is what the line is drawn from, and the extremes are carried beside
// it so a spike can be marked without being folded into the line.
type bucket struct {
	// Mean is the average reading of the step: what a step-wise line sits at.
	Mean float64
	// Peak is the highest reading in the step.
	Peak float64
	// Base is the lowest reading in the step.
	Base float64
}

// buckets reduces a series to about as many steps as the plot has pixels to
// show them in.
//
// A reading arrives four times a second, so a few minutes is hundreds of points
// in a few hundred pixels: drawn one per point the line becomes a picket fence
// and says nothing. Each step's mean is what the line is drawn from.
//
// The mean rather than the step's peak, which is what this did before: a peak
// made every step jump to the height of whatever brief reading sat in it, and
// the chart read as a zigzag. A mean still shows the drift, which is what the
// chart is for, and the peaks are marked separately so a spike survives.
func buckets(hist []float64, width float32) []bucket {
	want := int(width)
	if want < 8 {
		want = 8
	}
	reduce := func() []bucket {
		out := make([]bucket, 0, want)
		size := float64(len(hist)) / float64(want)
		for i := 0; i < want; i++ {
			lo := int(float64(i) * size)
			hi := int(float64(i+1) * size)
			if hi > len(hist) {
				hi = len(hist)
			}
			if lo >= hi {
				continue
			}
			sum, peak, base := 0.0, hist[lo], hist[lo]
			for _, v := range hist[lo:hi] {
				sum += v
				if v > peak {
					peak = v
				}
				if v < base {
					base = v
				}
			}
			out = append(out, bucket{Mean: sum / float64(hi-lo), Peak: peak, Base: base})
		}
		return out
	}

	if len(hist) <= want {
		// Fewer points than pixels needs no reduction, but the same shape is
		// returned so callers read one type whatever the length of the series.
		out := make([]bucket, 0, len(hist))
		for _, v := range hist {
			out = append(out, bucket{Mean: v, Peak: v, Base: v})
		}
		return out
	}
	return reduce()
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
	lo, hi := plotRange(means(series), target)
	yOf := func(v float64) float32 {
		f := (v - lo) / (hi - lo)
		return r.Y + r.H - float32(f)*r.H
	}

	// The reference the drift is judged against, in the muted ink: it is the line
	// everything is compared to, so it has to be visible rather than a hairline.
	ty := yOf(target)
	pt.Line(r.X, ty, r.X+r.W, ty, 1, p.InkMuted)

	if len(series) < 2 {
		return
	}
	step := r.W / float32(len(series)-1)

	// A step whose readings went over the target carries a mark from its mean up
	// to its peak. Drawn first, so the line reads on top of them.
	for i, b := range series {
		if b.Peak <= target {
			continue
		}
		x := r.X + float32(i)*step
		pt.Line(x, yOf(b.Peak), x, max(yOf(b.Mean), ty), 1, p.Danger)
	}

	prevX, prevY := r.X, yOf(series[0].Mean)
	for i := 1; i < len(series); i++ {
		x, y := r.X+float32(i)*step, yOf(series[i].Mean)
		pt.Line(prevX, prevY, x, y, 1.5, p.Accent)
		prevX, prevY = x, y
	}
}

// means is the series a plot ranges over: the step means, which is what the line
// is drawn from. A peak is a mark, not the line, so it does not stretch the
// range: one brief reading would flatten the drift it was meant to stand out
// from.
func means(series []bucket) []float64 {
	out := make([]float64, len(series))
	for i, b := range series {
		out[i] = b.Mean
	}
	return out
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
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				sectionLabel(s.deps.Words, c, p, "VOLUME")
				ui.Text(c, fmt.Sprintf("%.0f%%", gain*100)).
					FontSize(sizeBody).TextColor(p.InkMuted)
			})
			ui.Slider(c, &gain, 0, 1).FillWidth().OnChange(func() {
				if s.deps.OnGain != nil {
					s.deps.OnGain(gain)
				}
			})
		})

		// No Play or Stop here. A Stats page is for reading what the numbers
		// are; which receiver to play to is the panel's and the Receivers page's
		// question, and both name the device they act on. A button here could
		// only act on whatever was last played to, which reads as "play to I
		// don't know what".
	})
}

// playingReceiver is the receiver playback is going to, or nil.
func (s *Shell) playingReceiver() *discovery.Receiver {
	if s.deps.PlayingReceiver == nil {
		return nil
	}
	return s.deps.PlayingReceiver()
}

// latencyControl offers Auto and, beside it, the targets a user can pin.
//
// Auto is the default because the number is not something a user can know: it
// depends on the network at the time. The setting beside it is the floor Auto
// will not go below, which is the one thing only the user can say.
func (s *Shell) latencyControl(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	// The segmented control holds an index: 0 is Auto, then one per named mode.
	// The labels name the mode and its number, because a bare number is not
	// something to choose between: 120 and 500 mean "aggressive" and "safe",
	// and only the name says which is which.
	choices := settings.LatencyChoices()
	names := settings.LatencyLabels()

	labels := make([]string, 0, len(choices)+1)
	labels = append(labels, s.t("Auto"))
	for _, mode := range choices {
		labels = append(labels, s.t(names[mode]))
	}

	index := 0
	if !cfg.LatencyAuto {
		// A pinned setting that is not one of the named modes shows as Auto
		// until the user picks again; the line below still states the truth.
		for i, mode := range choices {
			if settings.NamedLatencyMs(mode) == cfg.LatencyMs {
				index = i + 1
				break
			}
		}
	}

	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).Gap(10).Children(func() {
			sectionLabel(s.deps.Words, c, p, "LATENCY")
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
					s.deps.OnLatency(settings.NamedLatencyMs(choices[index-1]))
				}
			}
		}

		// What the engine will actually be asked for, so the control is never
		// a mystery.
		effective := cfg.EffectiveLatencyMs(s.adapted())
		line := fmt.Sprintf(s.t("The engine will be asked for %d ms"), effective)
		if cfg.LatencyAuto {
			if effective > cfg.LatencyMs {
				line += fmt.Sprintf(s.t(" · Auto raised it from your floor of %d ms"), cfg.LatencyMs)
			} else {
				line += " · Auto, at your floor"
			}
		}
		ui.Text(c, line).FontSize(sizeSmall).TextColor(p.InkMuted)

		if st.Phase == engine.PhaseStreaming {
			ui.Text(c, s.t("A new target applies to the next start.")).FontSize(sizeSmall).TextColor(p.InkMuted)
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

	// The local capture side, which is a different thing from the network: the
	// round trip and the losses say whether the stream reached the speaker, and
	// these say whether the PC produced anything to send. A PC that captures
	// nothing leaves every network number perfect.
	if st.Capture.CaptureFrames > 0 || st.Capture.InputRate > 0 {
		s.capture(c, p, st)
	}

	// A sender stall is the stutter itself: it fell behind and threw away
	// audio. Packet loss is what makes the receiver ask for it again.
	losses := st.RetransmitMissing + st.RetransmitExpired
	unhealthy := st.SenderLateRecoveries > 0 || st.SkippedPackets > 0 ||
		losses > 0 || st.RetransmitQueueDrops > 0 || st.MediaSendDropped > 0

	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Row(c).FillWidth().Children(func() {
			sectionLabel(s.deps.Words, c, p, "LOSSES")
			ui.Spacer(c).Grow(1)
			if unhealthy {
				ui.Text(c, s.t("something is being lost")).FontSize(sizeBody).TextColor(p.Warning)
			} else {
				ui.Text(c, s.t("nothing lost so far")).FontSize(sizeBody).TextColor(p.Success)
			}
		})

		// Counters as label-and-value rows, so they line up in one column and
		// can be compared by eye. A zero stays quiet; anything else is called
		// out, because every one of these is an audible gap.
		s.countRow(c, p, s.t("Sender fell behind"), st.SenderLateRecoveries)
		s.countRow(c, p, s.t("Audio slots skipped"), st.SkippedPackets)
		s.countRow(c, p, s.t("Lost past recovery"), losses)
		s.countRow(c, p, s.t("Packets dropped on send"), st.MediaSendDropped)
		s.countRow(c, p, s.t("Retransmit queue overflow"), st.RetransmitQueueDrops)

		if st.FeedbackDelayed {
			ui.Text(c, s.t("The receiver's feedback is late, which is what a rising ping looks like.")).
				FontSize(sizeBody).TextColor(p.Warning)
		}
	})
}

// capture is the PC's own side of the stream.
//
// The network numbers cannot show a PC that produces no sound: while it captures
// nothing, the round trip stays perfect and the buffer stays full. These are the
// counters that do show it, and the drop and underrun counts are the ones that
// explain a gap the network had nothing to do with.
func (s *Shell) capture(c *ui.Context, p palette, st engine.Metrics) {
	cap := st.Capture
	unhealthy := cap.DroppedFrames > 0 || cap.UnderrunPackets > 0 || st.SilentReports > 0

	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Row(c).FillWidth().Children(func() {
			sectionLabel(s.deps.Words, c, p, "CAPTURE")
			ui.Spacer(c).Grow(1)
			if unhealthy {
				ui.Text(c, s.t("the PC's own audio is faltering")).FontSize(sizeBody).TextColor(p.Warning)
			} else {
				ui.Text(c, s.t("sound is being captured")).FontSize(sizeBody).TextColor(p.Success)
			}
		})

		if cap.InputRate > 0 {
			s.metricRow(c, p, s.t("Input rate"), fmt.Sprintf("%d Hz", cap.InputRate), p.Ink)
		}
		if p95 := cap.CaptureToSendP95Ms; p95 != nil {
			s.metricRow(c, p, s.t("Capture to send, slowest 5%"),
				fmt.Sprintf("%.1f ms", *p95), p.Ink)
		}
		if cap.MaxQueueAgeMs > 0 {
			s.metricRow(c, p, s.t("Queue age"), fmt.Sprintf("%.1f ms", cap.MaxQueueAgeMs), p.Ink)
		}
		s.countRow(c, p, s.t("Frames dropped"), cap.DroppedFrames)
		s.countRow(c, p, s.t("Buffer underruns"), cap.UnderrunPackets)
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

func (s *Shell) receiversPage(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	var found []discovery.Receiver
	if s.deps.Receivers != nil {
		found = s.deps.Receivers()
	}

	ui.Column(c).FillWidth().Gap(14).Children(func() {
		if len(found) == 0 {
			ui.Text(c, s.t("Looking for receivers…")).FontSize(sizeBody).TextColor(p.Ink)
			ui.Text(c, s.t("The PC and the HomePod have to be on the same network.")).
				FontSize(sizeBody).TextColor(p.InkMuted)
			return
		}

		// Which receiver the engine is streaming to, so a row can say Stop for
		// it rather than a Play that would start a second session the engine
		// cannot run.
		playingID := ""
		if st.Phase == engine.PhaseStreaming {
			if rec := s.playingReceiver(); rec != nil {
				playingID = rec.ID
			}
		}

		for _, r := range found {
			rec := r
			isPlaying := rec.ID == playingID && playingID != ""
			row := ui.Row(c).FillWidth().Padding(14, 2).Gap(12).Radius(0)
			row.Children(func() {
				ui.Column(c).Gap(3).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						// An offline receiver's name steps back, so the list
						// says what is reachable rather than only what was found.
						nameInk := p.Ink
						if !rec.Online {
							nameInk = p.InkMuted
						}
						ui.Text(c, rec.Name).FontSize(sizeBody).TextColor(nameInk)
						if rec.IsGroup() && !rec.Complete() {
							ui.Text(c, s.t("incomplete")).FontSize(sizeSmall).TextColor(p.Warning)
						}
						if !rec.Online {
							ui.Text(c, s.t("offline")).FontSize(sizeSmall).TextColor(p.InkMuted)
						}
					})
					detail := rec.Address
					if name := discovery.DisplayModel(rec.Model); name != "" {
						detail = name + " · " + rec.Address
					}
					if rec.IsGroup() {
						detail = fmt.Sprintf(s.t("stereo · %d/2 · %s"), len(rec.Members), detail)
					}
					ui.Text(c, detail).FontSize(sizeSmall).TextColor(p.InkMuted)
				})

				ui.Spacer(c).Grow(1)

				// One control per row that matches what it would do. A row
				// showing Play for the receiver that is already playing is a
				// button that starts a second session the engine cannot run. The
				// row above already states "incomplete" and "offline", so the two
				// cases that only repeated them are gone.
				switch {
				case isPlaying:
					if ui.Button(c, s.t("Stop")).Clicked() && s.deps.OnStop != nil {
						s.deps.OnStop()
					}
				default:
					if ui.Button(c, s.t("Play")).Clicked() && s.deps.OnPlay != nil {
						s.deps.OnPlay(rec)
					}
				}

				// The way to a receiver's own settings. Always there, rather than
				// only once it has some: otherwise a receiver that has never been
				// tuned could never be, and the first thing a user wants to change
				// is the device they have just added.
				if ui.Button(c, s.t("Tune")).Clicked() {
					s.tuning = rec.ID
				}
			})
			// One dashed rule under each receiver, which is what separates
			// them: the same divider the panel and the sections use.
			row.BorderWidth(0, 0, 1, 0).BorderStyle(ui.BorderDashed).BorderColor(p.Hairline)

			// What this receiver does for itself. It is shown once the receiver
			// has a setting of its own, or while it is open, because a row per
			// device for a setting nobody has changed says the same thing three
			// times over and hides the one that is different.
			if s.tuning == rec.ID || cfg.Options(rec.ID).HasOwn() {
				s.receiverOptions(c, p, cfg, rec)
			}
		}

		s.manualReceivers(c, p, cfg)
	})
}

// receiverOptions is one receiver's own settings: the three that are worth
// having per device.
//
// A HomePod and an Apple TV rarely want the same buffer: the HomePod is further
// from the router, or already playing for something else, and the one setting
// that fixes a stutter is target latency. Leaving it global means every device
// is tuned for whichever happened to be tuned last.
func (s *Shell) receiverOptions(c *ui.Context, p palette, cfg settings.Settings, rec discovery.Receiver) {
	opts := cfg.Options(rec.ID)
	effective := cfg.LatencyFor(rec.ID)
	own := opts.LatencyMs != nil

	// The modes, plus "use the global setting": a receiver that has set nothing
	// follows the global one, and that is the state to be able to get back to.
	choices := settings.LatencyChoices()
	labels := make([]string, 0, len(choices)+1)
	labels = append(labels, s.t("Use global setting"))
	for _, mode := range choices {
		labels = append(labels, settings.LatencyLabels()[mode])
	}
	index := 0
	if own {
		for i, mode := range choices {
			if settings.NamedLatencyMs(mode) == effective {
				index = i + 1
			}
		}
	}

	ui.Column(c).FillWidth().Gap(10).Padding(22, 4, 4, 4).Children(func() {
		ui.Row(c).FillWidth().Gap(14).Children(func() {
			sectionLabel(s.deps.Words, c, p, "LATENCY")
			seg := ui.Segmented(c, &index, labels...)
			if seg.Changed() && s.deps.OnReceiverOption != nil {
				if index == 0 {
					// Back to the global setting, which is "unset" rather than a
					// value, so the field is cleared rather than written.
					s.deps.OnReceiverOption(rec.ID, "latency_ms", nil)
					return
				}
				if index-1 < len(choices) {
					s.deps.OnReceiverOption(rec.ID, "latency_ms",
						settings.NamedLatencyMs(choices[index-1]))
				}
			}
			ui.Text(c, fmt.Sprintf("%d ms%s", effective, ownSuffix(s.t, own))).
				FontSize(sizeSmall).TextColor(p.InkMuted)
		})

		// Pairing, offered here rather than on the row.
		//
		// The engine's pair takes over whatever session is running and cancels it,
		// so a Pair next to a row that is streaming stops the audio the user is
		// listening to in order to ask them for a PIN. It belongs among a
		// receiver's own controls, where it is plainly about that one device, and
		// the panel's prompt is where the answer goes.
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			sectionLabel(s.deps.Words, c, p, "PAIRING")
			ui.Text(c, s.t("Pair this receiver with this PC. A receiver already paired "+
				"does not need it again.")).FontSize(sizeSmall).Grow(1).TextColor(p.InkMuted)
			if s.deps.OnPair != nil && ui.Button(c, s.t("Pair")).Clicked() {
				s.deps.OnPair(rec)
			}
		})

		// What the user calls it. The name the device reports is whatever was
		// typed into the Home app, which cannot be changed from here; an alias
		// can, and it is what we show instead.
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			sectionLabel(s.deps.Words, c, p, "NAME")
			alias := cfg.AliasFor(rec.ID)
			if ui.TextInput(c, &alias).Grow(1).Placeholder(rec.Name).Changed() {
				if s.deps.OnAlias != nil {
					s.deps.OnAlias(rec.ID, strings.TrimSpace(alias))
				}
			}
		})
	})
}

// ownSuffix marks a value this receiver set itself, so the row says which of the
// two numbers on screen is the device's own and which is inherited.
func ownSuffix(words func(string) string, own bool) string {
	if own {
		return " · " + words("its own")
	}
	return " · " + words("global")
}

// manualReceivers lists what the user typed in, and the way to type in another.
//
// The browse finds a device by its mDNS answer, and a speaker on another subnet
// - or behind a router that does not forward multicast - is present but
// invisible. Without this there is no way to reach it at all, which is why the
// desktop host has the same thing.
func (s *Shell) manualReceivers(c *ui.Context, p palette, cfg settings.Settings) {
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		for i, r := range cfg.ManualReceivers {
			index := i
			one := r
			row := ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, receiverName(one.Name, one.Host)).FontSize(sizeBody).TextColor(p.Ink)
				ui.Text(c, fmt.Sprintf("%s:%d", one.Host, one.Port)).FontSize(sizeSmall).
					TextColor(p.InkMuted)
				ui.Spacer(c).Grow(1)
				if ui.Button(c, s.t("Remove")).Clicked() && s.deps.OnManualReceiver != nil {
					s.deps.OnManualReceiver(false, index, settings.ManualReceiver{}, nil)
				}
			})
			row.BorderWidth(0, 0, 1, 0).BorderStyle(ui.BorderDashed).BorderColor(p.Hairline)
		}

		if s.adding.open {
			s.addManualForm(c, p)
			return
		}
		// Not stretched: a wide button here reads as a heading over the rows
		// above it rather than as one more thing to press.
		// Not stretched to the column's width: a wide button here reads as a
		// heading over the rows above it rather than as one more thing to press.
		add := ui.Button(c, s.t("Add one by hand")).Width(160)
		if add.Clicked() {
			s.adding = manualReceiver{open: true, port: "7000"}
		}
		ui.Text(c, s.t("For a receiver the network search cannot see: a speaker on "+
			"another subnet, or one whose announcements the router does not forward.")).
			FontSize(sizeSmall).TextColor(p.InkMuted)
	}).Padding(0, 20, 0, 0).
		BorderWidth(1, 0, 0, 0).BorderStyle(ui.BorderDashed).BorderColor(p.Hairline)
}

// addManualForm is the fields one manual receiver needs.
//
// The address is the only one that is not optional: a receiver with no address
// cannot be reached, and a name with no address is a row that plays nothing.
func (s *Shell) addManualForm(c *ui.Context, p palette) {
	name, host, port := s.adding.name, s.adding.host, s.adding.port
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.End).Children(func() {
			s.field(c, p, &host, s.t("IP address"), 1)
			s.field(c, p, &port, s.t("Port"), 0)
		})
		s.field(c, p, &name, s.t("Name"), 1)

		if s.adding.problem != "" {
			ui.Text(c, s.adding.problem).FontSize(sizeSmall).TextColor(p.Danger)
		}

		ui.Row(c).Gap(10).Children(func() {
			if ui.Button(c, s.t("Add")).Clicked() && s.deps.OnManualReceiver != nil {
				// The form is handed to the app whole, and the app refuses it or
				// keeps it. What the refusal was is reported back and shown here,
				// so the rules the settings enforce are the ones the user is told
				// about rather than a second copy of them.
				// A port that was not typed defaults on the app's side rather than
				// here, so there is one place that knows the AirPlay port.
				portNumber := 0
				if p, err := strconv.Atoi(strings.TrimSpace(port)); err == nil {
					portNumber = p
				}
				s.deps.OnManualReceiver(true, -1, settings.ManualReceiver{
					Name: strings.TrimSpace(name),
					Host: strings.TrimSpace(host),
					Port: portNumber,
				}, func(problem string) { s.adding.problem = problem })
			}
			if ui.Button(c, s.t("Cancel")).Clicked() {
				s.adding = manualReceiver{}
			}
		})
	})
}

// field is one labelled input, grown to the weight given.
func (s *Shell) field(c *ui.Context, p palette, value *string, label string, grow float32) {
	ui.Column(c).Grow(grow).Shrink(1).Gap(6).Children(func() {
		ui.Text(c, label).FontSize(sizeSmall).TextColor(p.InkMuted)
		ui.TextInput(c, value).FillWidth()
	})
}

// receiverName is a manual receiver's name, or its address when none was given.
// A row with no name is still a row the user has to recognise.
func receiverName(name, host string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return host
}

// captureEndpoint is the audio endpoint the engine records from.
//
// The engine's loopback capture reads whatever the console is being played
// through, which is right on a machine with one sound card and wrong on one with
// two: a USB interface the PC is actually playing through is not necessarily the
// one Windows calls default. A choice here is what such a machine needs, and
// with one card there is nothing to choose and the default stands.
func (s *Shell) captureEndpoint(c *ui.Context, p palette, cfg settings.Settings) {
	sectionLabel(s.deps.Words, c, p, "AUDIO INPUT")
	// What the engine records from. The system default is right on a machine with
	// one sound card and wrong on one with two, where the PC's audio may be going
	// through a device Windows does not call default.
	ids, labels := []string{""}, []string{s.t("System default")}
	for _, e := range localsound.Endpoints() {
		// Only a render endpoint can be captured from by loopback: a microphone
		// records the room, not what the PC is playing.
		if e.Capture {
			continue
		}
		ids = append(ids, e.ID)
		labels = append(labels, e.Name)
	}

	s.devicePicker(c, p, devicePicker{
		note: s.t("The sound the PC is playing is recorded from this device. " +
			"A machine with one sound card has nothing to choose."),
		label:   s.t("Input device"),
		unsetAs: s.t("A new device applies to the next stream."),
		ids:     ids,
		labels:  labels,
		chosen:  cfg.CaptureEndpoint,
		onPick: func(id string) {
			if s.deps.OnCaptureEndpoint != nil {
				s.deps.OnCaptureEndpoint(id)
			}
		},
	})
}

func (s *Shell) networkInterface(c *ui.Context, p palette, cfg settings.Settings) {
	sectionLabel(s.deps.Words, c, p, "NETWORK")
	// Which way out a browse takes. A machine with one adapter never needs this;
	// one with a VPN up can find its receivers on only one of them.
	ids, labels := []string{""}, []string{s.t("Automatic")}
	for _, a := range discovery.Interfaces() {
		ids = append(ids, strconv.Itoa(a.Index))
		label := a.Name
		if len(a.Addresses) > 0 {
			label = fmt.Sprintf("%s · %s", a.Name, a.Addresses[0])
		}
		labels = append(labels, label)
	}

	s.devicePicker(c, p, devicePicker{
		note: s.t("The adapter the receiver search runs on. A PC with a VPN up " +
			"can find a HomePod on one adapter only."),
		label:   s.t("Adapter"),
		unsetAs: s.t("A new adapter takes effect the next time the search runs."),
		ids:     ids,
		labels:  labels,
		chosen:  cfg.DiscoveryInterfaceID,
		onPick: func(id string) {
			if s.deps.OnDiscoveryInterface != nil {
				s.deps.OnDiscoveryInterface(id)
			}
		},
	})
}

// devicePicker is a stored id, the list it is chosen from, and what to do when a
// different one is picked.
//
// The two settings that choose hardware - which adapter the search runs on, and
// which device is recorded from - are the same control over two different lists:
// a stored value that may no longer be present, a list whose first entry is "the
// system's choice", and a label that has to be mapped back to the value it stands
// for. Writing that twice left two copies of the "the adapter has gone" case and
// two of the reverse lookup, which is the part that is easy to get wrong.
type devicePicker struct {
	note    string
	label   string
	unsetAs string
	ids     []string
	labels  []string
	chosen  string
	onPick  func(id string)
}

// devicePicker draws one of those controls.
func (s *Shell) devicePicker(c *ui.Context, p palette, d devicePicker) {
	// A stored value the machine no longer holds - set on another machine, or an
	// adapter that was removed - falls back to the system's choice. Offering it
	// would offer a choice that does nothing.
	chosen := ""
	for _, id := range d.ids {
		if id == d.chosen {
			chosen = d.chosen
			break
		}
	}

	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, d.note).FontSize(sizeSmall).TextColor(p.InkMuted)

		// The chosen entry as the control's own label, so the row says which
		// device is in use rather than only what sits beside it.
		sel := ""
		for i, id := range d.ids {
			if id == chosen {
				sel = d.labels[i]
				break
			}
		}
		if ui.Combobox(c, &sel, d.labels).Label(d.label).FillWidth().Changed() {
			for i, label := range d.labels {
				if label == sel {
					d.onPick(d.ids[i])
					break
				}
			}
		}

		// What a change takes effect on, or nothing when the automatic entry is
		// the one in use.
		if chosen != "" {
			ui.Text(c, d.unsetAs).FontSize(sizeSmall).TextColor(p.InkMuted)
		}
	}).Padding(0, 0, 20, 0).
		BorderWidth(0, 0, 1, 0).BorderStyle(ui.BorderDashed).BorderColor(p.Hairline)
}

// equalizerPage is the ten-band graphic equalizer.
//
// The presets are the desktop host's own curves and its own numbers, so a person
// who has used that recognises the shapes. A band moved from one is no longer
// that preset: it is "Custom", which is what it has become.
//
// Every change goes to the engine at once, rather than waiting for a confirm:
// the engine takes a sequence number and keeps the latest, so a slider can be
// dragged as fast as the hand can move.
func (s *Shell) equalizerPage(c *ui.Context, p palette, cfg settings.Settings) {
	eq := cfg.Equalizer
	if len(eq.BandGainsDB) != len(equalizer.Frequencies) {
		eq = equalizer.Default()
	}

	ui.Column(c).FillWidth().Gap(24).Children(func() {
		// On or off, and the preset.
		ui.Column(c).Gap(10).Children(func() {
			ui.Row(c).FillWidth().Children(func() {
				ui.Text(c, s.t("Equalizer")).FontSize(sizeBody).TextColor(p.Ink)
				ui.Spacer(c).Grow(1)
				on := eq.Enabled
				ui.Switch(c, &on).OnChange(func() {
					if s.deps.OnEqualizerEnabled != nil {
						s.deps.OnEqualizerEnabled(on)
					}
				})
			})
			ui.Text(c, s.t("Ten bands from 31 Hz to 16 kHz, each moving up to 12 dB. "+
				"A change is heard as it is made.")).
				FontSize(sizeSmall).TextColor(p.InkMuted)
		})

		// The presets, as a segmented control with Custom at the end.
		//
		// The labels are the presets' own names followed by Custom, which is what
		// PresetNames returns and what the index into Presets below assumes, so
		// the label for a segment is the preset at the same index rather than a
		// second list kept parallel to it.
		labels := make([]string, 0, len(equalizer.Presets)+1)
		for _, q := range equalizer.Presets {
			labels = append(labels, s.t(q.Name))
		}
		labels = append(labels, s.t(equalizer.Custom))

		// Custom, until the current curve is recognised as a preset's.
		index := len(equalizer.Presets)
		if p, ok := eq.Is(); ok {
			for i, q := range equalizer.Presets {
				if q.Name == p.Name {
					index = i
					break
				}
			}
		}
		seg := ui.Segmented(c, &index, labels...)
		if seg.Changed() && index < len(equalizer.Presets) && s.deps.OnEqualizerPreset != nil {
			s.deps.OnEqualizerPreset(equalizer.Presets[index].Name)
		}

		// Preamp, and then the bands.
		preamp := eq.PreampDB
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).FillWidth().Children(func() {
				ui.Text(c, s.t("Preamp")).FontSize(sizeSmall).TextColor(p.Ink)
				ui.Spacer(c).Grow(1)
				ui.Text(c, fmt.Sprintf("%+.1f dB", preamp)).FontSize(sizeSmall).
					TextColor(p.InkMuted)
			})
			ui.Slider(c, &preamp, -equalizer.LimitDB, equalizer.LimitDB).FillWidth().
				OnChange(func() {
					if s.deps.OnEqualizerPreamp != nil {
						s.deps.OnEqualizerPreamp(preamp)
					}
				})
		})

		// The ten bands, each labelled with its frequency.
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			for i := range equalizer.Frequencies {
				band := eq.BandGainsDB[i]
				ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, equalizer.Label(i)).FontSize(sizeSmall).TextColor(p.Ink).
						Width(64)
					ui.Slider(c, &band, -equalizer.LimitDB, equalizer.LimitDB).Grow(1).
						OnChange(func() {
							if s.deps.OnEqualizerBand != nil {
								s.deps.OnEqualizerBand(i, band)
							}
						})
					ui.Text(c, fmt.Sprintf("%+.0f dB", band)).FontSize(sizeSmall).
						TextColor(p.InkMuted).Width(48)
				})
			}
		})
	})
}

// networkInterface is the adapter the browse runs on, as a combobox of what the
// machine actually has.
//
// A machine with one adapter never needs this, which is why the default is "the
// system's own choice" and why it is the first entry. A machine with a VPN up
// can reach its receivers on one adapter only, and which one is not something
// the user can be expected to know: the addresses are shown beside each name so
// the right one is obvious rather than a guess among Ethernet 2, Ethernet 3 and
// Wi-Fi.

// settingsPage is where the app is configured: how much it costs to run, how
// much delay to ask the network for, what to record from and where to look.
//
// Every change here is written to disk as it is made, so there is nothing to
// confirm and no button to press. The one thing worth showing is a write that
// failed, because the interface still changes and the next launch would not
// have the setting.
func (s *Shell) settingsPage(c *ui.Context, p palette, cfg settings.Settings, st engine.Metrics) {
	ui.Column(c).FillWidth().Gap(26).Children(func() {
		// The mode switch, which is the one setting that changes what the app
		// costs to run.
		ui.Column(c).Gap(8).Children(func() {
			light := cfg.Mode == settings.ModeLightweight
			ui.Row(c).FillWidth().Children(func() {
				ui.Text(c, s.t("Lightweight mode")).FontSize(sizeBody).TextColor(p.Ink)
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
			ui.Text(c, s.t("Drops the window material and the trend chart, and samples the engine "+
				"once a second. The audio path is untouched: the engine keeps its own process "+
				"either way.")).FontSize(sizeSmall).TextColor(p.InkMuted)
		}).Padding(0, 0, 20, 0).
			BorderWidth(0, 0, 1, 0).BorderStyle(ui.BorderDashed).BorderColor(p.Hairline)

		// Latency. It belongs here rather than on Stats: it is a setting the
		// user chooses once, not a reading they come back to. Stats shows what
		// the current target is; this is where it is changed.
		ui.Column(c).Gap(8).Children(func() {
			s.latencyControl(c, p, cfg, st)
		}).Padding(0, 0, 20, 0).
			BorderWidth(0, 0, 1, 0).BorderStyle(ui.BorderDashed).BorderColor(p.Hairline)

		// The capture rate. The engine takes 44100 or 48000, and the pair is not
		// interchangeable: a HomePod is asked for whichever the stream is at, and
		// 48 kHz halves the work the engine does per second of audio.
		ui.Column(c).Gap(8).Children(func() {
			sectionLabel(s.deps.Words, c, p, "SAMPLE RATE")
			rates := []int{44100, 48000}
			labels := []string{"44.1 kHz", "48 kHz"}
			index := 0
			for i, r := range rates {
				if r == cfg.SampleRate {
					index = i
				}
			}
			seg := ui.Segmented(c, &index, labels...)
			if seg.Changed() && s.deps.OnSampleRate != nil {
				s.deps.OnSampleRate(rates[index])
			}
			ui.Text(c, s.t("A new rate applies to the next stream.")).
				FontSize(sizeSmall).TextColor(p.InkMuted)
		}).Padding(0, 0, 20, 0).
			BorderWidth(0, 0, 1, 0).BorderStyle(ui.BorderDashed).BorderColor(p.Hairline)

		// Whether a launch by hand shows anything. The tray is the app's surface,
		// so by default opening the panel is what a hand launch does: an app that
		// starts invisible gives the user no way to know it is running.
		ui.Column(c).Gap(8).Children(func() {
			atLaunch := cfg.OpenAtLaunch
			ui.Row(c).FillWidth().Children(func() {
				ui.Text(c, s.t("Show the window when I open the app")).FontSize(sizeBody).TextColor(p.Ink)
				ui.Spacer(c).Grow(1)
				ui.Switch(c, &atLaunch).OnChange(func() {
					if s.deps.OnOpenAtLaunch != nil {
						s.deps.OnOpenAtLaunch(atLaunch)
					}
				})
			})
			ui.Text(c, s.t("Off by default: the app starts in the tray and opens its panel. "+
				"Starting with the machine always stays quiet, here or not.")).
				FontSize(sizeSmall).TextColor(p.InkMuted)
		}).Padding(0, 0, 20, 0).
			BorderWidth(0, 0, 1, 0).BorderStyle(ui.BorderDashed).BorderColor(p.Hairline)

		// Starting with the machine. The desktop host does this through the same
		// Run key, so the two are indistinguishable to the user.
		ui.Column(c).Gap(8).Children(func() {
			login := cfg.StartAtLogin
			ui.Row(c).FillWidth().Children(func() {
				ui.Text(c, s.t("Start when I sign in")).FontSize(sizeBody).TextColor(p.Ink)
				ui.Spacer(c).Grow(1)
				ui.Switch(c, &login).OnChange(func() {
					if s.deps.OnStartAtLogin != nil {
						s.deps.OnStartAtLogin(login)
					}
				})
			})
			ui.Text(c, s.t("The shell starts in the tray at sign-in and connects to the "+
				"last receiver, so nothing has to be opened.")).
				FontSize(sizeSmall).TextColor(p.InkMuted)
		}).Padding(0, 0, 20, 0).
			BorderWidth(0, 0, 1, 0).BorderStyle(ui.BorderDashed).BorderColor(p.Hairline)

		// The adapter mDNS browses on. Nothing else in the app asks which way out
		// the browsing should take, and on a machine with a VPN up that is the
		// difference between finding the HomePod and finding nothing.
		s.networkInterface(c, p, cfg)

		// What the engine records from. It is the system's default by default,
		// which is right for a machine with one sound card; a machine with two
		// is where the choice matters.
		s.captureEndpoint(c, p, cfg)

		// Silencing the local speakers while streaming. The desktop host does
		// this by default, and it is what a user expects: the sound is going to
		// the HomePod.
		ui.Column(c).Gap(8).Children(func() {
			mute := cfg.MuteWhileStreaming
			ui.Row(c).FillWidth().Children(func() {
				ui.Text(c, s.t("Mute the PC speakers while streaming")).FontSize(sizeBody).TextColor(p.Ink)
				ui.Spacer(c).Grow(1)
				ui.Switch(c, &mute).OnChange(func() {
					if s.deps.OnMuteWhileStreaming != nil {
						s.deps.OnMuteWhileStreaming(mute)
					}
				})
			})
			ui.Text(c, s.t("The sound is being sent to the HomePod, so playing it here too would "+
				"be the same voice twice, one delayed behind the other.")).
				FontSize(sizeSmall).TextColor(p.InkMuted)
		}).Padding(0, 0, 20, 0).
			BorderWidth(0, 0, 1, 0).BorderStyle(ui.BorderDashed).BorderColor(p.Hairline)

		// Reconnecting a session that drops. Off by default, because reaching for
		// a speaker again on its own is something to ask for rather than assume.
		ui.Column(c).Gap(8).Children(func() {
			reconnect := cfg.Reconnect
			ui.Row(c).FillWidth().Children(func() {
				ui.Text(c, s.t("Reconnect a dropped stream")).FontSize(sizeBody).TextColor(p.Ink)
				ui.Spacer(c).Grow(1)
				ui.Switch(c, &reconnect).OnChange(func() {
					if s.deps.OnReconnect != nil {
						s.deps.OnReconnect(reconnect)
					}
				})
			})
			ui.Text(c, s.t("When the connection drops, start it again. Each retry waits "+
				"twice as long as the one before, up to eight seconds, and a stream that "+
				"ran for ten seconds starts the count over. A HomePod that asks for a PIN "+
				"is not retried: that needs you.")).
				FontSize(sizeSmall).TextColor(p.InkMuted)

			if cfg.Reconnect {
				attempts := float64(cfg.ReconnectAttempts)
				ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, s.t("Attempts")).FontSize(sizeSmall).TextColor(p.InkMuted)
					ui.Slider(c, &attempts, 1, 20).Grow(1).OnChange(func() {
						if s.deps.OnReconnectAttempts != nil {
							s.deps.OnReconnectAttempts(int(attempts))
						}
					})
					ui.Text(c, fmt.Sprintf("%d", int(attempts))).FontSize(sizeSmall).
						TextColor(p.Ink)
				})
			}
		}).Padding(0, 0, 20, 0).
			BorderWidth(0, 0, 1, 0).BorderStyle(ui.BorderDashed).BorderColor(p.Hairline)

		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, s.t("Closing the window")).FontSize(sizeBody).TextColor(p.Ink)
			ui.Text(c, s.t("Closing releases the window and everything it holds. Playback continues: "+
				"the engine is a separate process, and the app stays in the tray.")).
				FontSize(sizeSmall).TextColor(p.InkMuted)
		})

		// Every setting here is written as it is changed, so there is nothing
		// to save and no button to press. What is worth saying is when a write
		// failed: the control moves and the interface changes, so without this
		// the only sign would be the setting being gone at the next launch.
		if err := s.deps.Settings.WriteErr(); err != nil {
			ui.Text(c, fmt.Sprintf(s.t("Settings could not be saved: %v"), err)).
				FontSize(sizeSmall).TextColor(p.Danger)
		}
	})
}

// phaseLabel turns an engine phase into a word and a colour.
func phaseLabel(words i18n.Translator, p palette, phase engine.StreamPhase) (string, ui.Color) {
	switch phase {
	case engine.PhaseStreaming:
		return words.T("Playing"), p.Success
	case engine.PhaseConnecting, engine.PhaseNegotiating:
		return words.T("Connecting"), p.Warning
	case engine.PhasePairing:
		return words.T("Pairing"), p.Warning
	case engine.PhaseFailed:
		return words.T("Failed"), p.Danger
	case engine.PhaseStopped:
		return words.T("Stopped"), p.InkMuted
	default:
		return words.T("Idle"), p.InkMuted
	}
}

// ensure the mygo import is used for the window options the app passes.
var _ = mygo.App
