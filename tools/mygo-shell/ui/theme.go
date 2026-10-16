// Package ui draws the AirFlash shell.
//
// The look is an instrument's: this app exists to measure something, so it is
// drawn like the panel of a measuring device. Three rules carry it.
//
// Grayscale. Everything is black, white or a step of grey between them. The
// only chroma in the interface is a signal - a latency that has gone over
// target, a link that is failing - so colour always means something is wrong
// or something is fine, and never decorates.
//
// Square. Nothing is rounded, not a row, not a button, not a field. Corners
// are right angles and blocks are rectangles that meet.
//
// Dashed. Sections are divided by a dashed hairline rather than by a card or a
// gap. A dash is the cheapest divider there is, and it reads as a rule drawn on
// a chart rather than as a box drawn around a dashboard.
package ui

import (
	"github.com/egoist/mygo/ui"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/i18n"
)

// palette is one appearance.
//
// Ground and Ink carry the page; the surface steps separate blocks by tone.
// Accent is not a colour but the darkest ink, because the action in a
// grayscale interface is a black block. The three signal colours are the only
// chroma in the app.
type palette struct {
	Ground     ui.Color
	Surface    ui.Color
	SurfaceHi  ui.Color
	Ink        ui.Color
	InkMuted   ui.Color
	Accent     ui.Color
	AccentText ui.Color
	Warning    ui.Color
	Danger     ui.Color
	Success    ui.Color
	Hairline   ui.Color
}

// The type steps. The house rules set the floor: nothing smaller than 13px, and
// running text at 16px or more. Five steps is the ceiling, so every size in the
// interface maps onto one of these.
const (
	sizeSmall float32 = 13 // labels, secondary notes
	sizeBody  float32 = 16 // running text
	sizeLead  float32 = 19 // a short statement
	sizeTitle float32 = 22 // a section heading
	sizeHero  float32 = 52 // the one figure the window is about
)

// light is the daylight appearance.
func light() palette {
	return palette{
		Ground:     ui.Hex("#FFFFFF"),
		Surface:    ui.Hex("#F5F5F6"),
		SurfaceHi:  ui.Hex("#EAEAEC"),
		Ink:        ui.Hex("#101013"),
		InkMuted:   ui.Hex("#6C6C75"),
		Accent:     ui.Hex("#101013"),
		AccentText: ui.Hex("#FFFFFF"),
		Warning:    ui.Hex("#9A6400"),
		Danger:     ui.Hex("#B3261E"),
		Success:    ui.Hex("#1B6B3A"),
		Hairline:   ui.Hex("#D3D3D8"),
	}
}

// dark is the night appearance, the same relationships on a dark ground.
func dark() palette {
	return palette{
		Ground:     ui.Hex("#0C0C0E"),
		Surface:    ui.Hex("#16161A"),
		SurfaceHi:  ui.Hex("#212126"),
		Ink:        ui.Hex("#F2F2F4"),
		InkMuted:   ui.Hex("#95959E"),
		Accent:     ui.Hex("#F2F2F4"),
		AccentText: ui.Hex("#0C0C0E"),
		Warning:    ui.Hex("#D9A13C"),
		Danger:     ui.Hex("#E4766C"),
		Success:    ui.Hex("#5FBE85"),
		Hairline:   ui.Hex("#2E2E34"),
	}
}

// forDark picks the palette for the current appearance.
func forDark(isDark bool) palette {
	if isDark {
		return dark()
	}
	return light()
}

// themeFor turns the palette into the framework's theme, so the widgets the
// framework draws - sliders, switches, segmented controls, focus rings - use
// the same colours as the rest of the window. Without this the widgets keep
// the framework's own blue while everything else is the palette's teal, which
// reads as two applications sharing a window.
func themeFor(p palette, isDark bool) *ui.Theme {
	return &ui.Theme{
		Dark:           isDark,
		Background:     p.Ground,
		Surface:        p.Surface,
		SurfaceHover:   p.SurfaceHi,
		SurfacePressed: p.SurfaceHi,
		Border:         p.Hairline,
		Text:           p.Ink,
		TextMuted:      p.InkMuted,
		Accent:         p.Accent,
		AccentHover:    p.Accent,
		AccentPressed:  p.Accent,
		AccentText:     p.AccentText,
		Danger:         p.Danger,
		Warning:        p.Warning,
		Success:        p.Success,
		Selection:      p.Accent,
		Focus:          p.Accent,
		Scrollbar:      p.Hairline,
		// Square: nothing in this interface is rounded.
		Radius: 0,
	}
}

// rule divides two sections. It is a dashed hairline across the bottom of
// whatever it is applied to, which is how this interface separates blocks:
// a rule drawn on a chart, not a box drawn around a dashboard.
func rule(e ui.Element) ui.Element {
	return e.BorderWidth(0, 0, 1, 0).BorderStyle(ui.BorderDashed)
}

// sectionLabel is the small uppercase label a section opens with. The letters
// are spread because the label is a heading for a block of measurements, not
// running text, and the spacing is what keeps it from reading as one.
func sectionLabel(c *ui.Context, p palette, s string) {
	ui.Text(c, s).FontSize(sizeSmall).TextColor(p.InkMuted).
		LetterSpacing(0.9).Bold()
}

// latencyBand describes a measured latency in words and a colour. It never
// grades the user's own choice of target as wrong: it reports what was asked
// for and what arrived.
type latencyBand struct {
	Label string
	Color ui.Color
}

func bandFor(words i18n.Translator, p palette, requested, measured float64, hasMeasured bool) latencyBand {
	if !hasMeasured {
		return latencyBand{Label: words.T("waiting for the first reading"), Color: p.InkMuted}
	}
	over := measured - requested
	switch {
	case over <= 40:
		return latencyBand{Label: words.T("on target"), Color: p.Success}
	case over <= 160:
		return latencyBand{Label: words.T("slightly over target"), Color: p.Warning}
	default:
		return latencyBand{Label: words.T("well over target"), Color: p.Danger}
	}
}
