// Package ui draws the AirFlash shell.
//
// The look is the platform's own, the way a Mac app is meant to look: the same
// materials, the same rounded corners, the same quiet hierarchy. Four rules
// carry it.
//
// Native. The window is built from the system's own vocabulary - rounded
// controls, hairline separators, a light ground with a stepped surface, and
// type set in the system's family. Nothing here is drawn to look like a piece
// of hardware.
//
// Layered. Blocks sit on a raised surface rather than being outlined by a box,
// and depth is carried by tone and by a soft shadow rather than by a border.
// Two groups of the same thing are two blocks, not two halves of a rule.
//
// Quiet. Colour states a fact and never decorates: a latency over target, a
// link that has gone, a receiver waiting for a PIN. The chrome around the facts
// is grey, and stays grey.
//
// Roomy. Sections breathe. A gap between two groups says they are different
// things without a line having to say it, and running text is never tightened
// to fit.
package ui

import (
	"github.com/egoist/mygo/ui"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/i18n"
)

// palette is one appearance.
//
// Ground and Ink carry the page; the surface steps raise blocks above it, which
// is what separates one group of rows from another. Accent is the blue the
// platform itself uses for the action, so a button the app draws is the same
// colour as one the system would have drawn. The signal colours state a fact
// and nothing else.
type palette struct {
	Ground        ui.Color
	Surface       ui.Color
	SurfaceRaised ui.Color
	Separator     ui.Color
	Fill          ui.Color
	Ink           ui.Color
	InkMuted      ui.Color
	InkFaint      ui.Color
	Accent        ui.Color
	AccentText    ui.Color
	Hover         ui.Color
	Warning       ui.Color
	Danger        ui.Color
	Success       ui.Color
	Shadow        ui.Color
}

// The corner radii: one shared language of rounding. The platform uses a small
// radius on controls and a larger one on windows and cards, so everything here
// takes the nearer of the two rather than inventing its own.
const (
	radiusControl float32 = 6  // buttons, fields, small chips
	radiusBlock   float32 = 10 // a group of rows, a panel
)

// The type steps: the platform's own, so every size in the interface maps onto
// one of these. Nothing smaller than 13px, and running text at 15px or more.
const (
	sizeSmall float32 = 13 // labels, secondary notes
	sizeBody  float32 = 15 // running text
	sizeLead  float32 = 17 // a short statement, a control label
	sizeTitle float32 = 20 // a section heading
	sizeHero  float32 = 48 // the one figure the window is about
)

// light is the daylight appearance: the system's own light grey, with blocks
// raised a step above it.
func light() palette {
	return palette{
		Ground:        ui.Hex("#F2F2F5"),
		Surface:       ui.Hex("#FFFFFF"),
		SurfaceRaised: ui.Hex("#FFFFFF"),
		Separator:     ui.Hex("#DCDCE1"),
		Fill:          ui.Hex("#EDEDF0"),
		Ink:           ui.Hex("#1D1D1F"),
		InkMuted:      ui.Hex("#6E6E73"),
		InkFaint:      ui.Hex("#9A9AA0"),
		Accent:        ui.Hex("#007AFF"),
		AccentText:    ui.Hex("#FFFFFF"),
		Hover:         ui.Hex("#F0F0F3"),
		Warning:       ui.Hex("#A96B00"),
		Danger:        ui.Hex("#C4291F"),
		Success:       ui.Hex("#1E7A38"),
		Shadow:        ui.Hex("#0000001A"),
	}
}

// dark is the night appearance: the system's own dark grey, with blocks raised
// above it and the accent lightened so it still reads on a dark ground.
func dark() palette {
	return palette{
		Ground:        ui.Hex("#161618"),
		Surface:       ui.Hex("#1E1E21"),
		SurfaceRaised: ui.Hex("#26262A"),
		Separator:     ui.Hex("#3A3A3F"),
		Fill:          ui.Hex("#26262A"),
		Ink:           ui.Hex("#F2F2F4"),
		InkMuted:      ui.Hex("#9B9BA1"),
		InkFaint:      ui.Hex("#6E6E75"),
		Accent:        ui.Hex("#0A84FF"),
		AccentText:    ui.Hex("#FFFFFF"),
		Hover:         ui.Hex("#2A2A2E"),
		Warning:       ui.Hex("#E0A33C"),
		Danger:        ui.Hex("#EB7A70"),
		Success:       ui.Hex("#4FBE7C"),
		Shadow:        ui.Hex("#00000066"),
	}
}

// forDark picks the palette for the current appearance.
func forDark(isDark bool) palette {
	if isDark {
		return dark()
	}
	return light()
}

// GroundCSS is the window's own background, as the CSS colour the window
// options take: the light colour and the dark one, so the platform picks.
//
// It comes from the palette rather than being written out again. A window whose
// background does not match the page drawn inside it shows the difference at its
// edges - which is how a change to the palette leaves the window looking like two
// surfaces that do not quite meet.
func GroundCSS() string {
	return "light-dark(" + cssOf(light().Ground) + ", " + cssOf(dark().Ground) + ")"
}

// cssOf renders one colour the way the window options read it.
func cssOf(c ui.Color) string {
	const hex = "0123456789ABCDEF"
	b := []byte{'#', 0, 0, 0, 0, 0, 0}
	b[1] = hex[c.R>>4]
	b[2] = hex[c.R&0xF]
	b[3] = hex[c.G>>4]
	b[4] = hex[c.G&0xF]
	b[5] = hex[c.B>>4]
	b[6] = hex[c.B&0xF]
	return string(b)
}

// themeFor turns the palette into the framework's theme, so the widgets the
// framework draws - sliders, switches, segmented controls, focus rings - use
// the same colours and the same rounding as the rest of the window. Without
// this the widgets keep the framework's own grey-blue, which reads as two
// applications sharing a window.
func themeFor(p palette, isDark bool) *ui.Theme {
	return &ui.Theme{
		Dark:           isDark,
		Background:     p.Ground,
		Surface:        p.Surface,
		SurfaceHover:   p.Hover,
		SurfacePressed: p.Fill,
		Border:         p.Separator,
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
		Scrollbar:      p.Separator,
		Radius:         radiusControl,
		Spacing:        4,
		FontSize:       sizeBody,
	}
}

// card is one group of settings: a raised, rounded surface with its own
// padding, standing off the page so the groups are countable by eye. The
// platform groups settings this way; so does this.
func card(c *ui.Context, p palette, gap float32, build func()) {
	ui.Column(c).FillWidth().Gap(gap).Radius(radiusBlock).Background(p.Surface).
		Padding(14, 16).Shadow(0, 1, 3, 0, p.Shadow).Children(build)
}

// sectionLabel is the small uppercase label a section opens with. The letters
// are spread because the label is a heading for a block of measurements, not
// running text, and the spacing is what keeps it from reading as one.
func sectionLabel(words i18n.Translator, c *ui.Context, p palette, s string) {
	ui.Text(c, words.T(s)).FontSize(sizeSmall).TextColor(p.InkMuted).
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
