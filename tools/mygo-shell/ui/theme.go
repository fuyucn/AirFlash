// Package ui draws the AirFlash shell.
//
// The arrangement follows the platform: a sidebar and a toolbar the content
// scrolls under, and content on flat surfaces. Glass is the material the system
// draws, used only where the platform uses it - the bars and a few floating
// controls - never as decoration on content.
//
// The palette is the one the design direction dealt for this brief, "frost and
// slate": cool white and one paler blue-grey step holding everything, type in
// dark slate rather than black, and a single teal accent that appears on the
// action and the live reading only.
package ui

import (
	"github.com/egoist/mygo/ui"
)

// palette is one appearance.
//
// Two values do the visible work: a ground and an ink. One accent carries the
// action. The rest are steps between them, so surfaces separate by tone rather
// than by lines.
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
	sizeSmall  float32 = 13 // labels, secondary notes
	sizeBody   float32 = 16 // running text
	sizeLead   float32 = 19 // a short statement
	sizeFigure float32 = 44 // the headline numbers
	sizeTitle  float32 = 22 // a page's one large line
)

// light is the daylight appearance.
func light() palette {
	return palette{
		Ground:     ui.Hex("#F7F9FA"),
		Surface:    ui.Hex("#FFFFFF"),
		SurfaceHi:  ui.Hex("#ECF0F2"),
		Ink:        ui.Hex("#1F3038"),
		InkMuted:   ui.Hex("#5D6B72"),
		Accent:     ui.Hex("#0D7480"),
		AccentText: ui.Hex("#FFFFFF"),
		Warning:    ui.Hex("#8A5A00"),
		Danger:     ui.Hex("#A32020"),
		Success:    ui.Hex("#1D6B45"),
		Hairline:   ui.Hex("#D6DEE2"),
	}
}

// dark is the night appearance, the same relationships on a dark ground.
func dark() palette {
	return palette{
		Ground:     ui.Hex("#14191C"),
		Surface:    ui.Hex("#1E2529"),
		SurfaceHi:  ui.Hex("#2A3338"),
		Ink:        ui.Hex("#E8EEF1"),
		InkMuted:   ui.Hex("#9AA8AE"),
		Accent:     ui.Hex("#4FB3BE"),
		AccentText: ui.Hex("#0B1113"),
		Warning:    ui.Hex("#E0A94A"),
		Danger:     ui.Hex("#E8756B"),
		Success:    ui.Hex("#63C08D"),
		Hairline:   ui.Hex("#33403F"),
	}
}

// forDark picks the palette for the current appearance.
func forDark(isDark bool) palette {
	if isDark {
		return dark()
	}
	return light()
}

// latencyBand describes a measured latency in words and a colour. It never
// grades the user's own choice of target as wrong: it reports what was asked
// for and what arrived.
type latencyBand struct {
	Label string
	Color ui.Color
}

func bandFor(p palette, requested, measured float64, hasMeasured bool) latencyBand {
	if !hasMeasured {
		return latencyBand{Label: "waiting for the first reading", Color: p.InkMuted}
	}
	over := measured - requested
	switch {
	case over <= 40:
		return latencyBand{Label: "on target", Color: p.Success}
	case over <= 160:
		return latencyBand{Label: "slightly over target", Color: p.Warning}
	default:
		return latencyBand{Label: "well over target", Color: p.Danger}
	}
}
