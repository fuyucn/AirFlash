// Package settings holds the shell's own preferences and their defaults.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Mode is how much the shell spends on itself.
type Mode string

const (
	// ModeStandard draws glass, animates, and samples metrics often.
	ModeStandard Mode = "standard"
	// ModeLightweight drops the glass and the motion, redraws only when a
	// reading changes, and samples the engine rarely. It exists because every
	// cycle the UI spends competes with the audio send loop for the same Go
	// runtime, and a missed 7.98 ms deadline is an audible gap.
	ModeLightweight Mode = "lightweight"
)

// Settings are persisted between runs.
type Settings struct {
	Mode Mode `json:"mode"`

	// LatencyMs is the target the engine is asked for.
	LatencyMs int `json:"latency_ms"`

	// Gain is the master gain, 0..1.
	Gain float64 `json:"gain"`

	// SampleRate is the requested rate.
	SampleRate int `json:"sample_rate"`

	// AutoConnect starts the last receiver when the shell opens.
	AutoConnect bool `json:"auto_connect"`

	// ShowTrend keeps the latency history chart. Lightweight mode turns it off
	// because a chart is the most expensive thing the UI draws.
	ShowTrend bool `json:"show_trend"`

	// ReducedMotion stops the entrance animations.
	ReducedMotion bool `json:"reduced_motion"`

	// LastPeer is the receiver to auto-connect to.
	LastPeer string `json:"last_peer"`
}

// Default returns the starting settings: lightweight off, and a 500 ms target,
// which is the value that made playback smooth on a first-generation HomePod.
func Default() Settings {
	return Settings{
		Mode:       ModeStandard,
		LatencyMs:  500,
		Gain:       1.0,
		SampleRate: 44100,
		ShowTrend:  true,
	}
}

// Store persists settings as JSON beside the app's other data.
type Store struct {
	mu   sync.Mutex
	path string
	cur  Settings
}

// Open loads settings, falling back to the defaults when the file is absent or
// unreadable. A broken file must never stop the app from starting.
func Open(path string) *Store {
	s := &Store{path: path, cur: Default()}
	if b, err := os.ReadFile(path); err == nil {
		var loaded Settings
		if json.Unmarshal(b, &loaded) == nil {
			s.cur = loaded.normalized()
		}
	}
	return s
}

// DefaultPath is where the shell keeps its settings.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "airflash-shell.json"
	}
	return filepath.Join(dir, "AirFlash", "shell.json")
}

// Current returns a copy of the settings.
func (s *Store) Current() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

// Update changes the settings and writes them.
func (s *Store) Update(fn func(*Settings)) Settings {
	s.mu.Lock()
	fn(&s.cur)
	s.cur = s.cur.normalized()
	cur := s.cur
	path := s.path
	s.mu.Unlock()

	if b, err := json.MarshalIndent(cur, "", "  "); err == nil {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, b, 0o644)
	}
	return cur
}

// normalized keeps the values inside the ranges the engine accepts.
func (s Settings) normalized() Settings {
	if s.Mode != ModeLightweight {
		s.Mode = ModeStandard
	}
	if s.LatencyMs < 200 {
		s.LatencyMs = 200
	}
	if s.LatencyMs > 2000 {
		s.LatencyMs = 2000
	}
	if s.Gain < 0 {
		s.Gain = 0
	}
	if s.Gain > 1 {
		s.Gain = 1
	}
	switch s.SampleRate {
	case 44100, 48000:
	default:
		s.SampleRate = 44100
	}
	// Lightweight implies no trend chart and no motion.
	if s.Mode == ModeLightweight {
		s.ShowTrend = false
		s.ReducedMotion = true
	}
	return s
}

// RedrawIntervalMs is how often the UI repaints a live reading.
func (s Settings) RedrawIntervalMs() int {
	if s.Mode == ModeLightweight {
		return 1000 // once a second is plenty for a human reading
	}
	return 100
}

// SampleIntervalMs is how often the shell folds engine metrics into the model.
func (s Settings) SampleIntervalMs() int {
	if s.Mode == ModeLightweight {
		return 1000
	}
	return 250
}
