// Package settings holds the shell's own preferences and their defaults.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"strings"

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

	// LastPeer is the receiver to auto-connect to.
	LastPeer string `json:"last_peer"`

	// Adapt raises the target when a session showed a gap. It only ever moves
	// the target at a session boundary, never during one.
	Adapt bool `json:"adapt"`

	// Language is which words the interface uses. System follows the machine.
	Language string `json:"language"`

	// DiscoveryInterfaceID is the system id of the adapter mDNS runs on, or ""
	// for the system's own choice. A machine with a VPN up can find its
	// receivers on only one of its adapters.
	DiscoveryInterfaceID string `json:"discovery_interface_id"`

	// MuteWhileStreaming silences the local speakers while a stream runs, which
	// is what the desktop host does by default: the audio is being sent to the
	// HomePod, so playing it locally as well is the same voice twice, one
	// delayed behind the other.
	MuteWhileStreaming bool `json:"mute_while_streaming"`

	// ManualReceivers are receivers the user typed in, which the browse cannot
	// find: a device on another subnet, or one whose mDNS the router does not
	// forward. The desktop host has the same escape hatch, because a speaker
	// that is present but invisible is otherwise unreachable.
	ManualReceivers []ManualReceiver `json:"manual_receivers"`

	// ReceiverAliases are the names a user gives a receiver, keyed by the
	// identity it is known by. Identity moves when a device is re-paired or
	// replugged, and an alias is what keeps a row from appearing to be new.
	ReceiverAliases map[string]string `json:"receiver_aliases"`

	// CaptureEndpoint is the id of the audio endpoint to capture, or "" for the
	// system's default render device. The desktop host names that default
	// "loopback": it captures everything the PC plays rather than one program's
	// output.
	CaptureEndpoint string `json:"capture_endpoint"`

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

	// OpenAtLaunch shows the window when the app starts, rather than leaving the
	// app resident in the tray. A launch at login stays quiet on purpose - nobody
	// wants a window at sign-in - so this applies to a launch by hand, which is
	// the one the user asked for. Off by default: the tray is the app's surface,
	// and a window the user did not ask for is a window they have to close.
	OpenAtLaunch bool `json:"open_at_launch"`

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

// ManualReceiver is a receiver the user typed in.
//
// Id is the identity it is matched by as well as what its settings are keyed by,
// which is why it is the same value normalisation produces: an entry that typed
// a MAC with colons and a record that reports one without must be the same
// receiver.
type ManualReceiver struct {
	Id   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
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
		// The desktop host silences the local speakers while streaming, and it is
		// the right default: hearing the audio twice, one copy late, is worse
		// than a second of confusion about where it went.
		MuteWhileStreaming: true,
	}
}

// Store persists settings as JSON beside the app's other data.
type Store struct {
	mu   sync.Mutex
	path string
	cur  Settings

	// writeErr is why the last write failed, or nil. A setting the user made
	// that did not reach the disk is the one failure they cannot see: the
	// control moved, the interface changed, and nothing says the next launch
	// will not have it.
	writeErr error
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

// Current returns a copy of the settings that the caller owns outright.
//
// Copying the struct alone is not a copy: its slices and maps still point at the
// store's own data, so a caller that changed one would be editing the store
// from outside its lock, with nothing written to disk and no test failing. Every
// call site reads today, which is why this has never bitten - but the value
// handed out has to be one that cannot be edited for that to stay true.
func (s *Store) Current() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.clone()
}

// clone is the settings with every reference field copied, so the result shares
// nothing with the original.
func (s Settings) clone() Settings {
	out := s
	out.ManualReceivers = append([]ManualReceiver(nil), s.ManualReceivers...)
	out.Sessions = append([]Session(nil), s.Sessions...)
	out.ReceiverAliases = maps.Clone(s.ReceiverAliases)
	out.PerReceiver = maps.Clone(s.PerReceiver)
	out.Equalizer.BandGainsDB = append([]float64(nil), s.Equalizer.BandGainsDB...)
	return out
}

// Update changes the settings and writes them.
func (s *Store) Update(fn func(*Settings)) Settings {
	s.mu.Lock()
	fn(&s.cur)
	s.cur = s.cur.normalized()
	cur := s.cur
	path := s.path
	s.mu.Unlock()

	// The error describes the last write, not the worst one that ever happened.
	// It is set here on every write, including to nil: a store that failed once
	// - a full disk, a settings file a sync tool held open - would otherwise
	// keep a red warning on the settings page forever, even though every change
	// after it reached the disk.
	var writeErr error
	if b, err := json.MarshalIndent(cur, "", "  "); err == nil {
		writeErr = writeAtomic(path, b)
	}
	s.mu.Lock()
	s.writeErr = writeErr
	s.mu.Unlock()
	return cur
}

// writeAtomic writes the settings so that the file on disk is always a complete
// document, or is the one that was there before.
//
// os.WriteFile truncates the file and then writes into it. Anything that stops
// it half way - the machine losing power, the process being killed, a sync tool
// taking the file - leaves a truncated JSON document. Open cannot parse that and
// falls back to the defaults, so every setting the user chose is lost on the
// next launch and nothing says why.
//
// So it is written beside the file and renamed over it. A rename within a
// directory is atomic, so a reader sees either the whole old document or the
// whole new one.
func writeAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename has succeeded
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	// Removing the old file first is what a rename onto an existing file needs
	// on Windows, but it must only ever remove a file. Removing whatever is
	// there would delete a directory the user had put there and replace it with
	// a file - turning a path that had something in it into one that does not.
	if st, err := os.Lstat(path); err == nil && st.IsDir() {
		return &os.PathError{Op: "write settings over", Path: path,
			Err: errors.New("path is a directory")}
	}
	// Windows will not rename onto an existing file.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(name, path)
}

// WriteErr is why the last change did not reach the disk, or nil when it did.
func (s *Store) WriteErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeErr
}

// clamp keeps a value inside the range the engine accepts, and is the one place
// a floor and a ceiling are applied: three fields are bounded, and writing the
// same pair of comparisons out three times is where a bound gets applied to one
// of them and forgotten on the others.
func clamp[T int | float64](v T, lo, hi T) T {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// normalized keeps the values inside the ranges the engine accepts.
func (s Settings) normalized() Settings {
	if s.Mode != ModeLightweight {
		s.Mode = ModeStandard
	}
	// The engine's own range, 0..2000 (session.rs asserts it). The floor used to
	// be 200 here, which silently raised Real-time's 120 ms to 200 and made the
	// desktop's most aggressive mode unavailable through the shell.
	s.LatencyMs = clamp(s.LatencyMs, minLatencyMs, maxLatencyMs)
	s.Gain = clamp(s.Gain, 0, 1)
	switch s.SampleRate {
	case 44100, 48000:
	default:
		s.SampleRate = 44100
	}
	// Lightweight implies no trend chart: it is the most expensive thing the UI
	// draws, and dropping it is most of what lightweight is for.
	if s.Mode == ModeLightweight {
		s.ShowTrend = false
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
	s.ReconnectAttempts = clamp(s.ReconnectAttempts, 1, 20)
	if len(s.Sessions) > maxSessions {
		s.Sessions = s.Sessions[len(s.Sessions)-maxSessions:]
	}
	// A manual receiver that is not usable is dropped rather than kept: it came
	// from a settings file, and one with no address produces a row in the panel
	// that cannot be played to.
	kept := s.ManualReceivers[:0]
	for _, r := range s.ManualReceivers {
		if r.Id != "" && r.Port > 0 && r.Port <= 65535 {
			kept = append(kept, r)
		}
	}
	s.ManualReceivers = kept
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

// ManualReceiverAt returns the manual receiver at an index, or nil.
func (s Settings) ManualReceiverAt(i int) *ManualReceiver {
	if i < 0 || i >= len(s.ManualReceivers) {
		return nil
	}
	return &s.ManualReceivers[i]
}

// AliasFor is the name a user gave a receiver, or "".
//
// The key is matched after normalisation, because the identity a browse reports
// and the one a user saw in a log are not written the same way: "aa:bb:..." and
// "aabb..." are the same device.
func (s Settings) AliasFor(id string) string {
	if s.ReceiverAliases == nil {
		return ""
	}
	if name := s.ReceiverAliases[id]; name != "" {
		return name
	}
	for key, name := range s.ReceiverAliases {
		if devicesMatch(key, id) {
			return name
		}
	}
	return ""
}

// SetAlias stores a name for a receiver, or removes it where the name is empty.
func (s *Settings) SetAlias(id, name string) {
	if s.ReceiverAliases == nil {
		s.ReceiverAliases = map[string]string{}
	}
	if name == "" {
		delete(s.ReceiverAliases, id)
		return
	}
	// An alias replaces whatever was there rather than accumulating: one name
	// for one receiver is the whole point of the field.
	for key := range s.ReceiverAliases {
		if devicesMatch(key, id) {
			delete(s.ReceiverAliases, key)
		}
	}
	s.ReceiverAliases[id] = name
}

// NormalizeDevice reduces an identity to the form that says whether two of them
// are the same device.
//
// A twelve-character string is a MAC address, which is what a HomePod's device
// id is, and MACs are written with either colons, dashes or neither depending on
// who wrote them. Two that differ only in their separators are one device and
// must match as one, or a receiver appears to be new every time its identity is
// read from somewhere else. Everything else is left alone: an endpoint identity
// such as "10.0.10.31:7000" is only equal to itself.
func NormalizeDevice(id string) string {
	text := strings.TrimSpace(id)
	stripped := strings.NewReplacer(":", "", "-", "", " ", "").Replace(text)
	if len(stripped) == 12 && isHexString(stripped) {
		return strings.ToLower(stripped)
	}
	return text
}

func isHexString(text string) bool {
	for _, c := range text {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return len(text) > 0
}

// devicesMatch reports whether two identities are the same device.
func devicesMatch(a, b string) bool {
	na, nb := NormalizeDevice(a), NormalizeDevice(b)
	if na == "" || nb == "" {
		return false
	}
	return na == nb
}

// AddManualReceiver appends a manual receiver, returning an error when it is not
// usable.
//
// A duplicate is refused rather than replaced: two entries for one address are
// two rows in the panel for one device, and the user cannot tell which is which.
func (s *Settings) AddManualReceiver(r ManualReceiver) error {
	if r.Name == "" && r.Host == "" {
		return errors.New("a manual receiver needs an address")
	}
	if r.Id == "" {
		r.Id = NormalizeDevice(r.Host)
	}
	if r.Id == "" {
		return errors.New("a manual receiver needs an address")
	}
	// A port that was not given defaults to the one AirPlay 2 uses, which is what
	// the desktop host's own manual-receiver dialog does: nobody types it. It is
	// written out rather than taken from the discovery package, which would be a
	// dependency from settings on a network it has no other use for.
	if r.Port == 0 {
		r.Port = 7000
	}
	if r.Port < 1 || r.Port > 65535 {
		return errors.New("a manual receiver's port must be between 1 and 65535")
	}
	if net.ParseIP(r.Host) == nil {
		return errors.New("a manual receiver's address is not an IP address")
	}
	for _, have := range s.ManualReceivers {
		if have.Host == r.Host && have.Port == r.Port {
			return fmt.Errorf("%s:%d is already in the list", r.Host, have.Port)
		}
	}
	s.ManualReceivers = append(s.ManualReceivers, r)
	return nil
}

// DropManualReceiver removes a manual receiver by its index.
func (s *Settings) DropManualReceiver(i int) {
	if i < 0 || i >= len(s.ManualReceivers) {
		return
	}
	s.ManualReceivers = append(s.ManualReceivers[:i], s.ManualReceivers[i+1:]...)
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
