// Package ui draws the AirFlash shell.
//
// The design follows macOS 26's own arrangement rather than a web page's:
// a translucent sidebar and toolbar the content scrolls under, and content
// that sits on flat surfaces. Glass is a material the system draws, used only
// where macOS uses it - the bars and a few floating controls - never as
// decoration on content.
package ui

import (
	"github.com/egoist/mygo/ui"
)

// palette is one appearance. Two values do the visible work: a ground and an
// ink, with one accent that appears on the action and the live reading only.
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

// light is the daylight appearance.
func light() palette {
	return palette{
		Ground:     ui.Hex("#F2F2F7"),
		Surface:    ui.Hex("#FFFFFF"),
		SurfaceHi:  ui.Hex("#E8E8ED"),
		Ink:        ui.Hex("#1C1C1E"),
		InkMuted:   ui.Hex("#6C6C70"),
		Accent:     ui.Hex("#0A84FF"),
		AccentText: ui.Hex("#FFFFFF"),
		Warning:    ui.Hex("#B25000"),
		Danger:     ui.Hex("#D70015"),
		Success:    ui.Hex("#1D7A3E"),
		Hairline:   ui.Hex("#D1D1D6"),
	}
}

// dark is the night appearance.
func dark() palette {
	return palette{
		Ground:     ui.Hex("#1C1C1E"),
		Surface:    ui.Hex("#2C2C2E"),
		SurfaceHi:  ui.Hex("#3A3A3C"),
		Ink:        ui.Hex("#F2F2F7"),
		InkMuted:   ui.Hex("#98989D"),
		Accent:     ui.Hex("#0A84FF"),
		AccentText: ui.Hex("#FFFFFF"),
		Warning:    ui.Hex("#FF9F0A"),
		Danger:     ui.Hex("#FF453A"),
		Success:    ui.Hex("#30D158"),
		Hairline:   ui.Hex("#3A3A3C"),
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
