// Package settings holds the shell's own preferences and their defaults.
package settings

import (
	"encoding/json"
	"fmt"

	"github.com/fuyucn/airflash/tools/mygo-shell/internal/equalizer"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/i18n"
	"os"
	"path/filepath"
	"sync"
	"time"
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

	// LatencyMs is the target the engine is asked for when Auto is off, and the
	// floor Auto will not go below when it is on.
	LatencyMs int `json:"latency_ms"`

	// LatencyAuto lets the shell choose the target from what the network
	// actually did, instead of the user picking a number.
	LatencyAuto bool `json:"latency_auto"`

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

	// Adapt raises the target when a session showed a gap. It only ever moves
	// the target at a session boundary, never during one.
	Adapt bool `json:"adapt"`

	// Language is which words the interface uses. System follows the machine.
	Language string `json:"language"`

	// Equalizer is the shell's own, off and flat by default.
	Equalizer equalizer.Settings `json:"equalizer"`

	// StartAtLogin launches the shell when the machine starts, as the desktop
	// host does through the same Run key.
	StartAtLogin bool `json:"start_at_login"`

	// Reconnect retries a session that dropped, which is what the desktop host
	// calls ForceReconnect. Off by default: reaching for a speaker again by
	// itself is something to ask for.
	Reconnect bool `json:"reconnect"`

	// ReconnectAttempts is how many times to try before giving up. The desktop
	// host bounds this at twenty.
	ReconnectAttempts int `json:"reconnect_attempts"`

	// PerReceiver holds one receiver's own settings, keyed by receiver id. An
	// entry that is absent means "use the global setting", which is why every
	// field is a pointer: nil is "not set" and a value is "overridden".
	PerReceiver map[string]ReceiverOptions `json:"receivers"`

	// Sessions is what previous sessions turned out to be, newest last. It is
	// how the shell knows whether the network is actually getting worse.
	Sessions []Session `json:"sessions"`
}

// Session is one finished session, recorded so the next one can learn from it.
type Session struct {
	TargetMs       int       `json:"target_ms"`
	PeakRTTMs      float64   `json:"peak_rtt_ms"`
	MedianRTTMs    float64   `json:"median_rtt_ms"`
	SenderStalls   uint64    `json:"sender_stalls"`
	SkippedPackets uint64    `json:"skipped_packets"`
	ExpiredResends uint64    `json:"expired_resends"`
	At             time.Time `json:"at"`
}

// ReceiverOptions is one receiver's own settings.
//
// Every field is a pointer, because nil means "use the global setting" rather
// than "off": a HomePod that should stream at 500 ms while everything else uses
// the global 200 needs one field set and the rest left alone.
type ReceiverOptions struct {
	// Volume is 0..100, as the engine takes it.
	Volume *int `json:"volume,omitempty"`
	// LatencyMs is the target for this receiver.
	LatencyMs *int `json:"latency_ms,omitempty"`
	// AutoConnect starts this receiver when it appears, or does not.
	AutoConnect *bool `json:"auto_connect,omitempty"`
	// Hidden keeps the receiver out of the panel without forgetting it.
	Hidden *bool `json:"hidden,omitempty"`
}

// Default returns the starting settings: lightweight off, and a 500 ms target,
// which is the value that made playback smooth on a first-generation HomePod.
func Default() Settings {
	return Settings{
		Mode:              ModeStandard,
		LatencyMs:         500,
		Gain:              1.0,
		SampleRate:        44100,
		ShowTrend:         true,
		Adapt:             true,
		LatencyAuto:       true,
		ReconnectAttempts: 5,
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
	// The engine's own range, 0..2000 (session.rs asserts it). The floor used to
	// be 200 here, which silently raised Real-time's 120 ms to 200 and made the
	// desktop's most aggressive mode unavailable through the shell.
	if s.LatencyMs < minLatencyMs {
		s.LatencyMs = minLatencyMs
	}
	if s.LatencyMs > maxLatencyMs {
		s.LatencyMs = maxLatencyMs
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
	if len(s.Equalizer.BandGainsDB) != len(equalizer.Frequencies) {
		// A settings file from before the equalizer existed, or one written by a
		// build with a different number of bands, must not leave a short slice
		// behind: every band read would panic.
		s.Equalizer = equalizer.Default()
	}
	if s.Language == "" {
		s.Language = string(i18n.System)
	}
	// The desktop host bounds retries at twenty; one is a single attempt with no
	// retry behind it.
	if s.ReconnectAttempts < 1 {
		s.ReconnectAttempts = 1
	}
	if s.ReconnectAttempts > 20 {
		s.ReconnectAttempts = 20
	}
	if len(s.Sessions) > maxSessions {
		s.Sessions = s.Sessions[len(s.Sessions)-maxSessions:]
	}
	return s
}

// minLatencyMs and maxLatencyMs are what the engine accepts, from its own
// validation of latency_ms as 0..=2000. Zero is not offered: it is a valid ask
// and an unusable one, since it leaves no room for a resend.
const (
	minLatencyMs = 1
	maxLatencyMs = 2000
)

// maxSessions bounds the recorded history. Older sessions say less about the
// network than recent ones, and an unbounded list would grow forever.
const maxSessions = 50

// Lang is the language the interface is drawn in, with System resolved.
func (s Settings) Lang() i18n.Language {
	return i18n.For(i18n.Language(s.Language)).Lang
}

// LatencyFor is the target one receiver streams at: its own if it set one, the
// global otherwise.
func (s Settings) LatencyFor(id string) int {
	if o, ok := s.PerReceiver[id]; ok && o.LatencyMs != nil {
		return *o.LatencyMs
	}
	return s.EffectiveLatencyMs(0)
}

// GainFor is one receiver's own gain: its own if it set one, the global
// otherwise.
func (s Settings) GainFor(id string) float64 {
	if o, ok := s.PerReceiver[id]; ok && o.Volume != nil {
		return float64(*o.Volume) / 100
	}
	return s.Gain
}

// HiddenFor reports whether a receiver is kept out of the panel.
//
// Hidden is a fact about one receiver rather than a value to inherit, so it is
// only ever that receiver's own, and an unseen receiver is shown.
func (s Settings) HiddenFor(id string) bool {
	if o, ok := s.PerReceiver[id]; ok && o.Hidden != nil {
		return *o.Hidden
	}
	return false
}

// AutoConnectFor reports whether a receiver is started when it appears. An unset
// receiver follows the global setting.
func (s Settings) AutoConnectFor(id string) bool {
	if o, ok := s.PerReceiver[id]; ok && o.AutoConnect != nil {
		return *o.AutoConnect
	}
	return s.AutoConnect
}

// hasOwn reports whether a receiver has any setting of its own.
//
// A receiver with nothing set follows the global setting for everything, which
// is the state most are in, and is what a list of receivers should say by
// default rather than showing a row of controls that do nothing yet.
func (o ReceiverOptions) HasOwn() bool {
	return o.Volume != nil || o.LatencyMs != nil ||
		o.AutoConnect != nil || o.Hidden != nil
}

// Options returns one receiver's own settings, creating them if they are absent,
// so a caller can change one field without disturbing the rest.
func (s *Settings) Options(id string) ReceiverOptions {
	if s.PerReceiver == nil {
		s.PerReceiver = map[string]ReceiverOptions{}
	}
	return s.PerReceiver[id]
}

// SetOptions stores one receiver's settings.
func (s *Settings) SetOptions(id string, o ReceiverOptions) {
	if s.PerReceiver == nil {
		s.PerReceiver = map[string]ReceiverOptions{}
	}
	s.PerReceiver[id] = o
}

// EffectiveLatencyMs is the target to ask the engine for. With Auto on it is
// what the shell worked out from previous sessions, never below the floor the
// user set; with Auto off it is exactly what the user chose.
func (s Settings) EffectiveLatencyMs(adapted int) int {
	if !s.LatencyAuto {
		return s.LatencyMs
	}
	if adapted <= 0 {
		return s.LatencyMs
	}
	if adapted < s.LatencyMs {
		return s.LatencyMs
	}
	return adapted
}

// Latency modes, named as the desktop host names them. The numbers are what the
// engine is asked for, and the names are what a user picks between: they are the
// only way to tell someone that 120 ms is the aggressive choice and 500 ms the
// safe one without explaining jitter budgets to them.
//
// The same four the desktop offers, so the two hosts do not disagree about
// what "buffered" means.
type LatencyMode string

const (
	// LatencyRealtime is the lowest the engine accepts. It is the most
	// responsive and the first to stutter on a weak network.
	LatencyRealtime LatencyMode = "realtime"
	// LatencyNormal is the desktop's default.
	LatencyNormal LatencyMode = "normal"
	// LatencyBuffered is the safe end of the range, and what a first-generation
	// HomePod needed on the network this was built against.
	LatencyBuffered LatencyMode = "buffered"
	// LatencyCustom is a value chosen to the millisecond, not one of the three.
	LatencyCustom LatencyMode = "custom"
)

// NamedLatencyMs is the target a named mode asks for.
func NamedLatencyMs(mode LatencyMode) int {
	switch mode {
	case LatencyRealtime:
		return 120
	case LatencyBuffered:
		return 500
	default:
		// Normal, and anything unrecognised, which the desktop treats the same
		// way: its own default rather than a guess.
		return 200
	}
}

// LatencyChoices are the named modes, in the order the desktop offers them.
func LatencyChoices() []LatencyMode {
	return []LatencyMode{LatencyRealtime, LatencyNormal, LatencyBuffered}
}

// LatencyLabels are the names shown beside each mode, and the number each one
// asks for. The number is in the label because it is the whole point of the
// choice, and a mode named "buffered" says nothing to someone who has not read
// the numbers.
func LatencyLabels() map[LatencyMode]string {
	return map[LatencyMode]string{
		LatencyRealtime: fmt.Sprintf("Real-time · %d ms", NamedLatencyMs(LatencyRealtime)),
		LatencyNormal:   fmt.Sprintf("Normal · %d ms", NamedLatencyMs(LatencyNormal)),
		LatencyBuffered: fmt.Sprintf("Buffered · %d ms", NamedLatencyMs(LatencyBuffered)),
		LatencyCustom:   "Custom",
	}
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
