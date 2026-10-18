// Package equalizer holds the shell's equalizer settings and the presets.
//
// The engine has a fixed ten-band graphic EQ: the frequencies are fixed at
// 31.25 Hz through 16 kHz, and each band moves between -12 and +12 dB. It has no
// presets of its own, which is why they are here: a preset is a choice of ten
// numbers, and the numbers are the engine's.
//
// The presets are the desktop host's own, so a person who has used that
// recognises the shapes.
package equalizer

import "fmt"

// Frequencies are the ten bands, in hertz. These are the engine's own, and they
// are named rather than derived: a band is chosen by its place in this list.
var Frequencies = []float64{
	31.25, 62.5, 125, 250, 500, 1000, 2000, 4000, 8000, 16000,
}

// LimitDB is how far a band can move, which the engine validates as -12..=12.
const LimitDB = 12

// Settings is one equalizer, as the engine takes it.
type Settings struct {
	// Enabled is whether the equalizer is in the path at all. A disabled one is
	// not the same as a flat one: the engine skips the filters entirely, which
	// costs nothing and changes nothing.
	Enabled bool `json:"enabled"`
	// PreampDB is the overall level, so a curve that boosts everything does not
	// clip.
	PreampDB float64 `json:"preamp_db"`
	// BandGainsDB is one gain per band, in the order of Frequencies.
	BandGainsDB []float64 `json:"band_gains_db"`
}

// Default is off and flat, which is what a fresh install should be.
func Default() Settings {
	return Settings{Enabled: false, PreampDB: 0, BandGainsDB: make([]float64, len(Frequencies))}
}

// Preset is one named curve, which is a choice of ten numbers.
type Preset struct {
	Name string
	// Gains is one per band, in the order of Frequencies.
	Gains []float64
}

// Presets are the desktop host's own, with its own numbers, so the two hosts draw
// the same shapes.
//
// The order matters: it is the order they are offered in.
var Presets = []Preset{
	{"Flat", gains(0, 0, 0, 0, 0, 0, 0, 0, 0, 0)},
	{"Bass boost", gains(6, 4, 2, 0, 0, 0, 0, 0, 0, 0)},
	{"Vocals", gains(-3, -3, -2, 0, 2, 4, 3, 1, -1, -2)},
	{"Pop", gains(-1, 1, 3, 4, 2, 0, -1, 1, 2, 3)},
	{"Rock", gains(4, 3, 1, -1, -2, 0, 2, 4, 4, 3)},
}

// Custom identifies a curve that is not any preset's.
const Custom = "Custom"

// PresetNames are the names, in order, and Custom last.
func PresetNames() []string {
	out := make([]string, 0, len(Presets)+1)
	for _, p := range Presets {
		out = append(out, p.Name)
	}
	return append(out, Custom)
}

// Is reports whether the settings are exactly a named preset's curve, and which.
//
// A curve that a preset was started from and then a band moved on is no longer
// that preset, and the interface says so: it is "Custom", because that is what
// it is now.
func (s Settings) Is() (Preset, bool) {
	for _, p := range Presets {
		if same(p.Gains, s.BandGainsDB) {
			return p, true
		}
	}
	return Preset{}, false
}

// Name is what the settings are called: a preset's name, or Custom.
func (s Settings) Name() string {
	if p, ok := s.Is(); ok {
		return p.Name
	}
	return Custom
}

// Apply sets the curve to a preset's, and turns the equalizer on: choosing a
// preset is a request to hear it, and leaving it off would look like nothing had
// happened.
func (s *Settings) Apply(p Preset) {
	s.Enabled = true
	s.BandGainsDB = append([]float64(nil), p.Gains...)
}

// SetBand moves one band, clamped to what the engine accepts.
//
// The clamp is here rather than left to the engine, because a slider that can be
// dragged past what the engine takes would only report the failure afterwards.
func (s *Settings) SetBand(index int, db float64) {
	if index < 0 || index >= len(s.BandGainsDB) {
		return
	}
	if db > LimitDB {
		db = LimitDB
	}
	if db < -LimitDB {
		db = -LimitDB
	}
	s.BandGainsDB[index] = db
	s.Enabled = true
}

// Clamp keeps every value inside what the engine validates: the preamp and each
// band within -LimitDB..=LimitDB.
//
// It is the one place the rule is written. The sliders clamp what they send, but
// a settings file can hold anything - a hand edit, a build with a different
// limit - and normalized() only checked the band count, so an out-of-range curve
// went to the engine as it stood and the engine refused the whole equalizer.
// The curve on screen was then not the curve being heard.
func (s *Settings) Clamp() {
	s.PreampDB = clampDB(s.PreampDB)
	for i := range s.BandGainsDB {
		s.BandGainsDB[i] = clampDB(s.BandGainsDB[i])
	}
}

func clampDB(db float64) float64 {
	if db > LimitDB {
		return LimitDB
	}
	if db < -LimitDB {
		return -LimitDB
	}
	return db
}

// Label is one band as the interface shows it: the frequency, spelled the way an
// audio tool spells it.
func Label(i int) string {
	if i < 0 || i >= len(Frequencies) {
		return ""
	}
	hz := Frequencies[i]
	// The two bands that are not whole hertz are shown as they are: rounding
	// 62.5 to 62 names a band that does not exist, and %.1f leaves a trailing
	// zero on the ones that are whole.
	switch hz {
	case 31.25:
		return "31.25 Hz"
	case 62.5:
		return "62.5 Hz"
	}
	if hz >= 1000 {
		return fmt.Sprintf("%.0f kHz", hz/1000)
	}
	return fmt.Sprintf("%.0f Hz", hz)
}

// gains builds a curve from ten numbers, so a preset can be written the way it
// reads: high left, low right, one per band from the bottom up.
func gains(vs ...float64) []float64 {
	out := make([]float64, len(Frequencies))
	copy(out, vs)
	return out
}

func same(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
