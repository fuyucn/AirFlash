package ui

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/discovery"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/settings"
)

// panelWidth is the width of the tray panel, in DIPs.
//
// The panel is the app's main surface: it opens from the tray icon and is where
// playback is chosen. It is deliberately narrow, because it sits beside the
// icon it belongs to rather than filling a screen.
const panelWidth = 420

// PanelWidth is the panel's width in DIPs, for the caller that sizes the flyout.
func PanelWidth() int { return panelWidth }

// Panel is the tray panel: the receivers, one control each, and the current
// reading. It is what the user sees almost every time the app is opened.
type Panel struct {
	deps Deps

	// volume is the playing receiver's volume. There is one, not one per
	// receiver, because the engine's command addresses the session and a
	// session is one receiver.
	volume float64
	// volumeKnown is false until the user has moved the slider, so the control
	// shows the receiver's own volume rather than claiming one.
	volumeKnown bool
}

// NewPanel builds the tray panel view.
func NewPanel(d Deps) *Panel {
	return &Panel{deps: d, volume: 1}
}

// panelPad is the room the panel leaves around its content, in DIPs.
const panelPad = 14

// Height returns the height the panel needs for what it will draw, in DIPs.
//
// A flyout has no size of its own: the platform needs a number before the
// panel exists, and the number depends on how many receivers discovery found
// and whether one of them is playing. So the panel is built once off screen and
// measured, which is a few milliseconds on the thread that is opening it
// anyway. Measuring beats a formula because the panel stays the right size when
// a row gains a line.
func (p *Panel) Height() int {
	const ceiling = 1200
	t := ui.NewTester(p.View, panelWidth, ceiling)

	// The lowest thing the panel draws, plus its own padding. Every control
	// carries a label, so the labels bound the content.
	bottom := float32(0)
	for _, s := range t.Texts() {
		if r, ok := t.Find(s); ok {
			bottom = max(bottom, r.Y+r.H)
		}
	}
	if bottom <= 0 {
		return 200
	}
	return int(bottom) + 2*panelPad
}

// View builds one frame of the panel.
//
// The panel draws its own surface only where the platform does not provide one:
// macOS gives a popover its material through Vibrancy, and Windows rounds and
// outlines the flyout itself.
func (p *Panel) View(c *ui.Context) {
	cfg := p.deps.Settings.Current()
	st := p.deps.State.Snapshot()
	pal := forDark(c.Theme().Dark)

	// The framework draws the slider, so give it this app's palette; without
	// it the slider keeps the framework's own blue and the panel shows two
	// accents.
	c.SetTheme(themeFor(pal, c.Theme().Dark))
	c.Root().Background(ui.Transparent)

	box := ui.Column(c).Fill().Padding(14).Gap(12)
	switch {
	case c.Vibrancy():
		// Over the popover's own material.
	default:
		// The platform does not draw a surface, so the panel does. Square, and
		// outlined by a dashed rule like every other block in the app.
		box.Background(pal.Ground).Radius(0).
			BorderWidth(1).BorderStyle(ui.BorderDashed).BorderColor(pal.Hairline)
	}

	box.Children(func() {
		p.header(c, pal, cfg, st)
		p.receivers(c, pal, st)
		p.reading(c, pal, cfg, st)
		p.footer(c, pal)
	})
}

// header names the product and says, in one word, what the engine is doing.
func (p *Panel) header(c *ui.Context, pal palette, cfg settings.Settings, st engine.Metrics) {
	label, col := phaseLabel(pal, st.Phase)
	ui.Row(c).FillWidth().Padding(0, 0, 10, 0).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "AIRFLASH").FontSize(sizeSmall).Bold().TextColor(pal.Ink).
			LetterSpacing(1.2)
		ui.Spacer(c).Grow(1)
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(8).Height(8).Radius(0).Background(col)
			ui.Text(c, label).FontSize(sizeSmall).TextColor(pal.InkMuted)
		})
	}).BorderWidth(0, 0, 1, 0).BorderStyle(ui.BorderDashed).BorderColor(pal.Hairline)
}

// receivers lists what discovery found, one row each. This is the panel's
// reason to exist: choosing what to play to.
func (p *Panel) receivers(c *ui.Context, pal palette, st engine.Metrics) {
	var found []discovery.Receiver
	if p.deps.Receivers != nil {
		found = p.deps.Receivers()
	}

	if len(found) == 0 {
		ui.Column(c).FillWidth().Padding(0, 6).Gap(6).Children(func() {
			ui.Text(c, "Looking for receivers…").FontSize(sizeBody).TextColor(pal.Ink)
			ui.Text(c, "The PC and the HomePod have to be on the same network.").
				FontSize(sizeSmall).TextColor(pal.InkMuted)
		})
		return
	}

	// Which receiver is playing, in the engine's own terms: it streams to the
	// hosts of one receiver, so a receiver is playing when its hosts are the
	// ones being sent to.
	playing := st.Phase == engine.PhaseStreaming
	playingID := ""
	if playing {
		if rec := p.deps.PlayingReceiver(); rec != nil {
			playingID = rec.ID
		}
	}

	ui.Column(c).FillWidth().Gap(0).Children(func() {
		for _, r := range found {
			rec := r
			p.receiverRow(c, pal, rec, playing && rec.ID == playingID)
		}
	})
}

// receiverRow is one receiver: its name, its detail, and one control.
//
// The control is Play on every receiver that can be played to, and Stop on the
// one that is playing. Only the playing receiver gets a Stop and a volume,
// because those act on the session, and there is exactly one session.
func (p *Panel) receiverRow(c *ui.Context, pal palette, rec discovery.Receiver, playing bool) {
	row := ui.Column(c).FillWidth().Padding(10, 9).Gap(8).Radius(0)
	if playing {
		// The playing row is the one block that steps out of the page, so the
		// eye finds it without the accent spending itself on a fill.
		row.Background(pal.SurfaceHi)
	} else if !rec.Online {
		// A receiver that stopped answering is still listed, because the user
		// may want to see that it is the one that went, not have it vanish.
		row.Background(pal.Surface)
	}

	row.Children(func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
				// An offline receiver's name steps back to the muted ink: it is
				// there, but it is not something to play to.
				nameInk := pal.Ink
				if !rec.Online {
					nameInk = pal.InkMuted
				}
				ui.Text(c, rec.Name).FontSize(sizeBody).TextColor(nameInk).
					Ellipsis("…").SingleLine()
				ui.Text(c, recDetail(rec)).FontSize(sizeSmall).TextColor(pal.InkMuted).
					Ellipsis("…").SingleLine()
			})

			switch {
			case !rec.Complete():
				// Half a stereo pair cannot be played to, so it says so
				// instead of offering a control that would sound wrong.
				ui.Text(c, "pair incomplete").FontSize(sizeSmall).TextColor(pal.Warning)
			case !rec.Online && !playing:
				// An offline receiver cannot be played to, so it says so rather
				// than offering a Play that would time out.
				ui.Text(c, "offline").FontSize(sizeSmall).TextColor(pal.InkMuted)
			case playing:
				if ui.Button(c, "Stop").Clicked() && p.deps.OnStop != nil {
					p.deps.OnStop()
				}
			default:
				if ui.Button(c, "Play").Clicked() && p.deps.OnPlay != nil {
					p.deps.OnPlay(rec)
				}
			}
		})

		if playing {
			// Volume belongs to the receiver that is playing. The engine's
			// device-volume command addresses the session, and a session is one
			// receiver, so a slider anywhere else would do nothing.
			p.volumeRow(c, pal, rec)
			p.silenceRow(c, pal, p.deps.State.Snapshot())
		}
	})
}

// silenceRow says that the PC is sending no sound at all.
//
// This is the one failure the network numbers cannot show: while the PC captures
// nothing, the round trip stays perfect, the buffer stays full, and the reading
// on the panel is exactly what it would be for healthy audio. The user hears
// nothing and every number says things are fine, so the silence has to be stated
// on its own.
func (p *Panel) silenceRow(c *ui.Context, pal palette, st engine.Metrics) {
	// A session that has just opened has not measured anything yet, and a PC
	// that never makes a sound reports no stamp at all; neither is silence.
	if !st.HasSilence || st.SilentReports == 0 {
		return
	}
	seconds := int(st.SilentFor.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("no sound from the PC for %d s", seconds)).
			FontSize(sizeSmall).TextColor(pal.Warning)
	})
}

// volumeRow is the playing receiver's own volume, which the engine sets on the
// device rather than in the local mix.
func (p *Panel) volumeRow(c *ui.Context, pal palette, rec discovery.Receiver) {
	vol := p.volume
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "VOL").FontSize(sizeSmall).TextColor(pal.InkMuted).LetterSpacing(0.9)
		ui.Slider(c, &vol, 0, 1).Grow(1).OnChange(func() {
			p.volume = vol
			p.volumeKnown = true
			if p.deps.OnDeviceVolume != nil {
				p.deps.OnDeviceVolume(rec, vol)
			}
		})
		ui.Text(c, fmt.Sprintf("%.0f%%", vol*100)).FontSize(sizeSmall).TextColor(pal.InkMuted)
	})
}

// reading is the current latency, kept to one line so the panel stays a panel.
// The full picture is in the main window.
//
// Before there is a reading, the line reports where the connection got to.
// "no reading yet" says nothing, and a user waiting on a speaker that is
// refusing the session has no way to tell that from one that is merely slow.
func (p *Panel) reading(c *ui.Context, pal palette, cfg settings.Settings, st engine.Metrics) {
	target := targetMs(cfg, st)

	ui.Row(c).FillWidth().Padding(0, 4).AlignItems(ui.End).Children(func() {
		if st.HasTotalLatency {
			band := bandFor(pal, target, st.TotalLatencyMs, true)
			ui.Row(c).Gap(8).AlignItems(ui.End).Children(func() {
				ui.Text(c, fmt.Sprintf("%.0f", st.TotalLatencyMs)).
					FontSize(sizeTitle).Bold().TextColor(pal.Ink)
				ui.Text(c, "ms delay").FontSize(sizeSmall).TextColor(pal.InkMuted)
			})
			ui.Spacer(c).Grow(1)
			ui.Text(c, band.Label).FontSize(sizeSmall).TextColor(band.Color)
			return
		}

		ui.Text(c, fmt.Sprintf("TARGET %d MS", int(target))).
			FontSize(sizeSmall).TextColor(pal.InkMuted)
		ui.Spacer(c).Grow(1)
		ui.Text(c, phaseNote(st)).FontSize(sizeSmall).TextColor(phaseNoteColor(pal, st))
	})
}

// phaseNote is what the engine is doing, in words a user recognises. The engine
// reports its own phase names, which are its internals: "negotiating" and
// "handshake_complete" are not something to show a person waiting for music.
func phaseNote(st engine.Metrics) string {
	switch st.Phase {
	case engine.PhaseConnecting, engine.PhaseNegotiating:
		if st.Reconnecting {
			return fmt.Sprintf("reconnecting %d/%d", st.ReconnectAttempt, st.ReconnectMax)
		}
		return "connecting…"
	case engine.PhaseFailed:
		// The engine's error text is the one thing that says why, and it is
		// usually the most useful line on the panel.
		if st.Detail != "" {
			return st.Detail
		}
		return "could not connect"
	case engine.PhaseStopped:
		return "stopped"
	default:
		return "no reading yet"
	}
}

func phaseNoteColor(p palette, st engine.Metrics) ui.Color {
	switch st.Phase {
	case engine.PhaseConnecting, engine.PhaseNegotiating:
		return p.Warning
	case engine.PhaseFailed:
		return p.Danger
	default:
		return p.InkMuted
	}
}

// footer is the way to the window, and the way out.
func (p *Panel) footer(c *ui.Context, pal palette) {
	ui.Row(c).FillWidth().Gap(8).Padding(10, 0, 0, 0).Children(func() {
		if ui.Button(c, "Stats").Clicked() && p.deps.OnOpenWindow != nil {
			p.deps.OnOpenWindow("stats")
		}
		if ui.Button(c, "Settings").Clicked() && p.deps.OnOpenWindow != nil {
			p.deps.OnOpenWindow("settings")
		}
		ui.Spacer(c).Grow(1)
		if ui.Button(c, "Quit").Clicked() && p.deps.OnQuit != nil {
			p.deps.OnQuit()
		}
	}).BorderWidth(1, 0, 0, 0).BorderStyle(ui.BorderDashed).BorderColor(pal.Hairline)
}

// recDetail is the second line of a receiver: what it is and where it is.
func recDetail(rec discovery.Receiver) string {
	detail := rec.Address
	if name := discovery.DisplayModel(rec.Model); name != "" {
		// The product name, not the internal code: "HomePod" is what the user
		// picked, "AudioAccessory1,1" is what Apple calls it internally.
		detail = name + " · " + rec.Address
	}
	if rec.IsGroup() {
		detail = fmt.Sprintf("stereo %d/2 · %s", len(rec.Members), detail)
	}
	return detail
}
