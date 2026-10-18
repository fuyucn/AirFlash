package engine

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"sync"
	"time"
)

// StreamPhase is where a connection currently is.
type StreamPhase string

const (
	PhaseIdle        StreamPhase = "idle"
	PhaseConnecting  StreamPhase = "connecting"
	PhaseNegotiating StreamPhase = "negotiating"
	PhaseStreaming   StreamPhase = "streaming"
	// PhasePairing is a receiver waiting for the four-digit PIN a HomePod shows on
	// its own screen. Nothing else in AirPlay asks for this, and no number of
	// retries gets past it: only the user can.
	PhasePairing StreamPhase = "pairing"
	PhaseFailed  StreamPhase = "failed"
	PhaseStopped StreamPhase = "stopped"
)

// phaseOf maps one of the engine's phase names to the phases the interface
// knows.
//
// The engine names its steps after the AirPlay protocol it is working through:
// "info", "authenticate", "authenticated", "setup_session", "setup_audio".
// Those are its internals, and none of them is one of the phases the shell
// draws - so taking them as they arrive leaves the interface showing "Idle"
// for a connection that is very much in progress, which reads as nothing
// happening. Everything the engine says before audio flows is a connection
// being made, which is what PhaseConnecting means here.
//
// A name this does not recognise keeps its own value, so a phase added to the
// engine later still reaches the interface as itself rather than as a guess.
func phaseOf(name string) StreamPhase {
	switch name {
	case "info", "authenticate", "authenticated",
		"setup_session", "setup_audio", "negotiating", "handshake_complete":
		return PhaseConnecting
	case "negotiated":
		return PhaseNegotiating
	}
	return StreamPhase(name)
}

// tracePhase logs a phase change when AIRFLASH_TRACE_PHASE is set.
//
// A tray app has no console, so a phase that does not move - or moves to the
// wrong one - is otherwise invisible: the user sees a word and a colour, and
// nothing says which event put it there. This is the diagnostic for that: it
// prints the engine's own name, the phase it became, and what the previous one
// was, so a run can be read back and the offending event identified.
func tracePhase(from, to StreamPhase, detail string) {
	if os.Getenv("AIRFLASH_TRACE_PHASE") == "" {
		return
	}
	log.Printf("phase: %q -> %q (detail %q)", from, to, detail)
}

// Metrics is the live picture the UI draws.
//
// The numbers here are the ones that explain a stutter: the receiver's own
// playout buffer (what the user hears as delay), the feedback round trip (the
// network's ping), and the counters that show packets being lost, retried or
// dropped on the way out.
type Metrics struct {
	Phase       StreamPhase
	Detail      string
	RequestedMs int
	SampleRate  int
	ClockID     string
	Updated     time.Time

	// ReceiverBufferMs is the receiver's own playout delay. This is the
	// largest part of what the user hears as latency.
	ReceiverBufferMs        float64
	HasReceiverBuffer       bool
	ReceiverBufferEstimated bool

	// NetworkRTTMs is the engine's feedback round trip: the network's ping.
	NetworkRTTMs     float64
	HasNetworkRTT    bool
	FeedbackDelayed  bool
	FeedbackFailures uint64

	// TotalLatencyMs is the sum of the parts that were actually observed.
	TotalLatencyMs  float64
	HasTotalLatency bool

	PacketsSent          uint64
	MediaSendErrors      uint64
	MediaBacklogged      uint64
	MediaSendDropped     uint64
	RetransmitRequests   uint64
	RetransmitsSent      uint64
	RetransmitMissing    uint64
	RetransmitExpired    uint64
	RetransmitQueueDrops uint64
	BacklogPackets       int
	BacklogLimit         int

	// SenderLateRecoveries and SkippedPackets are the sender's own stalls: it
	// fell behind and discarded expired audio. Each one is an audible gap.
	SenderLateRecoveries uint64
	SkippedPackets       uint64
	MaxSendLatenessUs    uint64

	// History feeds the trend chart, newest last.
	History []float64

	// Slots is one entry per engine report, which the engine sends once a
	// second, so it is a second-by-second record of the session. It is what
	// says where a gap happened rather than only that one did: a latency chart
	// shows the network, and this shows the audio.
	Slots []Slot

	// Model is what the receiver said it is, once it has answered. It comes from
	// the same /info the engine already asks, and it is shown so the user can
	// tell that the device answering is the one they meant.
	Model string

	// Pairing is set while a receiver is waiting for a PIN. Host is the receiver
	// asking, so the prompt can say which speaker is holding up the connection.
	Pairing   bool
	PairingOn string

	// DeviceVolume is the volume the receiver itself reported, 0 to 1, and
	// HasDeviceVolume says whether it has said anything. The engine reads the
	// value back after every change and once a second, so this is what keeps the
	// slider where the speaker actually is rather than where it was last
	// dragged.
	DeviceVolume    float64
	HasDeviceVolume bool
	DeviceVolumeErr string

	// Reconnecting is set while a dropped session is being retried, with the
	// attempt and the total. The engine has no part in this: the retry is the
	// shell's, so the numbers are set from here rather than read from an event.
	Reconnecting     bool
	ReconnectAttempt int
	ReconnectMax     int

	// Capture is what the local capture side is doing. It comes from the
	// engine's capture_metrics, one report a second, and it is the only place
	// the shell can see whether the PC is actually producing audio: the network
	// side stays perfect while nothing is being captured.
	Capture Capture

	// Silent is true while the PC has sent no audio for longer than the standby
	// threshold. The engine reports when it last captured a frame, and the gap
	// between that and now is what this is.
	Silent    bool
	SilentFor time.Duration
	// HasSilence says whether Silent has been measured at all, so an unmeasured
	// gap is not reported as silence.
	HasSilence bool
	// SilentReports is how many consecutive reports carried no new audio, which
	// at one report a second is how many seconds the PC has been quiet.
	SilentReports int

	// SessionPeakRTTMs and rttSamples describe the round trip over the whole
	// session. The peak is what a gap has to fit inside; the spread says
	// whether the link is slow or erratic.
	SessionPeakRTTMs float64
	rttSamples       []float64
}

// Slot is one second of a session.
//
// The counters the engine reports are cumulative, so each slot holds what
// changed since the one before it rather than the total. A slot with nothing
// lost is the common case and stays quiet.
type Slot struct {
	// Skipped is the audio the sender threw away in this second: it fell
	// behind and the slots it had missed were already past their play time.
	Skipped uint64
	// Recoveries is how many times the sender caught up from a stall.
	Recoveries uint64
	// Expired is the audio the receiver asked for too late to be sent again.
	Expired uint64
	// LatencyMs is what the delay was during this second, or 0 if the engine
	// had not measured it yet.
	LatencyMs float64
}

// Gapped reports whether this second had an audible gap in it.
func (s Slot) Gapped() bool { return s.Skipped > 0 || s.Recoveries > 0 || s.Expired > 0 }

// WindowSeconds is how much of a session the shell keeps and draws.
//
// The engine reports once a second, so this is both the number of latency
// points and the number of timeline slots. The two charts sit one above the
// other and are read against each other, so they have to cover the same span:
// a wider window on one would put a spike and its gap at different places.
// Two minutes is as far back as a chart of seconds stays readable.
const WindowSeconds = 120

// SetReconnecting records that a session is being retried, with the attempt and
// the total, so the surfaces can say which attempt they are waiting on.
func (s *State) SetReconnecting(attempt, max int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Zero means no retry is being waited out, which is the ordinary case while
	// a connection is starting or audio is flowing.
	s.metrics.Reconnecting = attempt > 0
	s.metrics.ReconnectAttempt = attempt
	s.metrics.ReconnectMax = max
}

// SessionRTT returns the peak and median round trip of the session so far.
// The median needs a few samples to mean anything, so an empty second value is
// returned until then.
func (s *State) SessionRTT() (peak, median float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	peak = s.metrics.SessionPeakRTTMs
	if len(s.metrics.rttSamples) == 0 {
		return peak, 0
	}
	sorted := append([]float64(nil), s.metrics.rttSamples...)
	sort.Float64s(sorted)
	return peak, sorted[len(sorted)/2]
}

// ResetSession clears the per-session counters, so a new stream is judged on
// its own numbers rather than the last one's.
func (s *State) ResetSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics.SessionPeakRTTMs = 0
	s.metrics.rttSamples = nil
	s.metrics.SenderLateRecoveries = 0
	s.metrics.SkippedPackets = 0
	s.metrics.RetransmitExpired = 0
	s.metrics.RetransmitMissing = 0
	s.metrics.RetransmitQueueDrops = 0
	s.metrics.MediaSendDropped = 0
	s.metrics.History = nil
	s.metrics.Slots = nil
	s.metrics.HasTotalLatency = false
	s.metrics.HasReceiverBuffer = false
	s.metrics.HasNetworkRTT = false
	s.lastSkipped = 0
	s.lastRecoveries = 0
	s.lastExpired = 0
	s.lastAudioQPC = 0
	s.silentReports = 0
	s.metrics.Capture = Capture{}
	s.metrics.Silent = false
	s.metrics.SilentFor = 0
	s.metrics.HasSilence = false
	s.metrics.SilentReports = 0
	s.metrics.Pairing = false
	s.metrics.PairingOn = ""
}

// State is the shared, mutex-guarded app model.
type State struct {
	mu        sync.RWMutex
	metrics   Metrics
	peers     []Peer
	engine    Hello
	haveHello bool
	lastErr   string

	// The last cumulative gap counters the engine reported, so the next report
	// can be turned into what happened in between.
	lastSkipped    uint64
	lastRecoveries uint64
	lastExpired    uint64

	// lastAudioQPC is the capture stamp the engine last reported. It only moves
	// while the PC produces sound, so watching it is how silence is seen without
	// sharing the engine's clock.
	lastAudioQPC  uint64
	silentReports int
}

// diff is how much a cumulative counter moved since it was last read. The
// engine restarts its counters with each session, so a value that goes
// backwards means a new session rather than a negative count.
func diff(now, before uint64) uint64 {
	if now < before {
		return now
	}
	return now - before
}

// NewState returns an idle state.
func NewState() *State {
	return &State{metrics: Metrics{Phase: PhaseIdle}}
}

// Snapshot copies the metrics for the render pass.
func (s *State) Snapshot() Metrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := s.metrics
	m.History = append([]float64(nil), m.History...)
	m.Slots = append([]Slot(nil), m.Slots...)
	return m
}

// Peers returns the known receivers.
func (s *State) Peers() []Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Peer(nil), s.peers...)
}

// SetPeers replaces the receiver list.
func (s *State) SetPeers(p []Peer) {
	s.mu.Lock()
	s.peers = append([]Peer(nil), p...)
	s.mu.Unlock()
}

// Hello returns what the engine reported about itself.
func (s *State) Hello() (Hello, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.engine, s.haveHello
}

// LastError returns the most recent engine error.
func (s *State) LastError() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastErr
}

// Apply folds one engine event into the state.
func (s *State) Apply(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics.Updated = time.Now()

	switch ev.Event {
	case "hello":
		var h Hello
		if json.Unmarshal(ev.Raw, &h) == nil {
			s.engine = h
			s.haveHello = true
		}
	case "phase":
		var p struct {
			Phase string `json:"phase"`
		}
		if json.Unmarshal(ev.Raw, &p) == nil {
			s.metrics.Detail = p.Phase
			from := s.metrics.Phase
			s.metrics.Phase = phaseOf(p.Phase)
			tracePhase(from, s.metrics.Phase, p.Phase)
		}
	case "streaming":
		var st struct {
			RequestedLatencyMs int    `json:"requested_latency_ms"`
			SampleRate         int    `json:"sample_rate"`
			ClockID            string `json:"clock_id"`
			Members            int    `json:"members"`
		}
		if json.Unmarshal(ev.Raw, &st) == nil {
			from := s.metrics.Phase
			s.metrics.Phase = PhaseStreaming
			tracePhase(from, s.metrics.Phase, "streaming")
			s.metrics.RequestedMs = st.RequestedLatencyMs
			s.metrics.SampleRate = st.SampleRate
			s.metrics.ClockID = st.ClockID
			s.metrics.Detail = "streaming"
		}
	case "peer_info":
		// The receiver's own description of itself, from the /info the engine
		// already asks before authenticating. It is kept rather than dropped,
		// because it is the one thing that says the device answering is the
		// HomePod the user picked.
		var info struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(ev.Raw, &info) == nil && info.Model != "" {
			s.metrics.Model = info.Model
		}
		s.metrics.Phase = PhaseNegotiating
	case "pin_required":
		// The engine's pair command reports this when a receiver has no stored
		// credentials for this machine, which is what a HomePod does the first
		// time. It is a phase of its own: the engine is still running and still
		// waiting, and calling this a failure would make the shell give up on a
		// connection that is waiting for one thing only.
		var pin struct {
			Host string
		}
		if json.Unmarshal(ev.Raw, &pin) == nil {
			s.metrics.PairingOn = pin.Host
		}
		s.metrics.Pairing = true
		s.metrics.Phase = PhasePairing
		s.metrics.Detail = ""
	case "paired", "negotiated", "handshake_complete":
		// Paired and the steps after it all mean the same thing to the interface:
		// a PIN, if one was being waited for, is no longer, and the engine is
		// still working through the connection. Written once rather than three
		// times.
		s.metrics.Pairing = false
		s.metrics.PairingOn = ""
		s.metrics.Phase = PhaseNegotiating
	case "device_volume":
		// The receiver's own volume, read back after a change and once a second.
		// The slider is drawn from this, so without it the control shows what
		// the user last dragged to even when the speaker refused the change.
		var v struct {
			Volume    *float64 `json:"volume"`
			Available bool     `json:"available"`
			Message   string   `json:"message"`
		}
		if json.Unmarshal(ev.Raw, &v) != nil {
			break
		}
		if v.Message != "" {
			s.metrics.DeviceVolumeErr = v.Message
		} else {
			s.metrics.DeviceVolumeErr = ""
		}
		if v.Available && v.Volume != nil {
			vol := *v.Volume
			if vol < 0 {
				vol = 0
			}
			if vol > 1 {
				vol = 1
			}
			s.metrics.DeviceVolume = vol
			s.metrics.HasDeviceVolume = true
		}

	case "capture_metrics":
		// The local capture side, once a second. Two things are read from it:
		// the counters, which the Stats page shows, and whether the PC is
		// actually producing audio.
		//
		// The engine stamps each non-silent frame with its own performance
		// counter, and that counter belongs to another process's clock, so the
		// shell cannot subtract it from anything of its own. What it can do is
		// watch the stamp: while audio is flowing it moves every report, and
		// while the PC is silent it does not move at all.
		var capture struct {
			Metrics Capture `json:"metrics"`
		}
		if json.Unmarshal(ev.Raw, &capture) != nil {
			break
		}
		s.metrics.Capture = capture.Metrics

		// The very first stamp is not "the stamp moved" - it is the first thing
		// the engine has said at all, and a session that opened on a silent PC
		// must count that second rather than reset the count to zero.
		first := s.lastAudioQPC == 0
		moved := !first && capture.Metrics.LastAudioQPCNs != s.lastAudioQPC
		s.lastAudioQPC = capture.Metrics.LastAudioQPCNs

		switch {
		case moved || first:
			// Audio arrived - or the session has just opened on it - so whatever
			// silence was being counted is over.
			s.silentReports = 0
		case capture.Metrics.LastAudioQPCNs != 0:
			// The stamp stood still, which is what a quiet PC looks like.
			s.silentReports++
		}
		s.metrics.SilentReports = s.silentReports
		// One report is one second, so the count is the age of the silence.
		s.metrics.SilentFor = time.Duration(s.silentReports) * time.Second
		s.metrics.HasSilence = capture.Metrics.LastAudioQPCNs != 0
		s.metrics.Silent = s.silentReports > 0

	case "transport_metrics":
		var tm TransportMetrics
		if json.Unmarshal(ev.Raw, &tm) != nil {
			break
		}
		s.metrics.SenderLateRecoveries = tm.SenderLateRecoveries
		s.metrics.SkippedPackets = tm.SkippedPackets
		s.metrics.MaxSendLatenessUs = tm.MaxSendLatenessUs

		// With a stereo pair the deepest buffer bounds what is heard, and the
		// worst round trip is the one that matters.
		var deepest float64
		var haveBuffer bool
		var worstRTT float64
		var haveRTT bool
		var members int

		// The engine's counters are cumulative over the session, not per
		// report: it sends the total so far each time. So each report replaces
		// the last rather than adding to it - a running sum would count every
		// packet once per report that followed it. Members of a stereo pair
		// are the one place the numbers do add up, because a pair sends to
		// both halves and each half counts its own.
		var sent, sendErrors, backlogged, dropped uint64
		var requests, resends, missing, expired, queueDrops uint64
		var feedbackFailures uint64

		for _, m := range tm.Members {
			members++
			if m.Media.ReceiverLatencyMs > deepest {
				deepest = m.Media.ReceiverLatencyMs
				haveBuffer = true
				s.metrics.ReceiverBufferEstimated = m.Media.ReceiverLatencyEstimated
			}
			if m.Health.FeedbackRTTMs != nil && *m.Health.FeedbackRTTMs > worstRTT {
				worstRTT = *m.Health.FeedbackRTTMs
				haveRTT = true
			}
			s.metrics.FeedbackDelayed = s.metrics.FeedbackDelayed || m.Health.FeedbackDelayed
			feedbackFailures += m.Health.FeedbackFailures

			// Sum the counters across members: a pair sends to both halves.
			sent += m.Media.PacketsSent
			sendErrors += m.Media.MediaSendErrors
			backlogged += m.Media.MediaSendBacklogged
			dropped += m.Media.MediaSendDropped
			requests += m.Media.RetransmitRequests
			resends += m.Media.RetransmitsSent
			missing += m.Media.RetransmitMissing
			expired += m.Media.RetransmitExpired
			queueDrops += m.Media.RetransmitQueueDrops
			s.metrics.BacklogPackets = max(s.metrics.BacklogPackets, m.Media.BacklogPackets)
			s.metrics.BacklogLimit = max(s.metrics.BacklogLimit, m.Media.BacklogLimitPackets)
		}

		s.metrics.PacketsSent = sent
		s.metrics.MediaSendErrors = sendErrors
		s.metrics.MediaBacklogged = backlogged
		s.metrics.MediaSendDropped = dropped
		s.metrics.RetransmitRequests = requests
		s.metrics.RetransmitsSent = resends
		s.metrics.RetransmitMissing = missing
		s.metrics.RetransmitExpired = expired
		s.metrics.RetransmitQueueDrops = queueDrops
		s.metrics.FeedbackFailures = feedbackFailures

		if haveBuffer {
			s.metrics.ReceiverBufferMs = deepest
			s.metrics.HasReceiverBuffer = true
		}
		if haveRTT {
			s.metrics.NetworkRTTMs = worstRTT
			s.metrics.HasNetworkRTT = true
			if worstRTT > s.metrics.SessionPeakRTTMs {
				s.metrics.SessionPeakRTTMs = worstRTT
			}
			s.metrics.rttSamples = append(s.metrics.rttSamples, worstRTT)
			if len(s.metrics.rttSamples) > 600 {
				s.metrics.rttSamples = s.metrics.rttSamples[len(s.metrics.rttSamples)-600:]
			}
		}

		// The audible delay is the receiver's buffer, plus half the round trip
		// for one-way transit. Only observed parts count, so a missing
		// measurement lowers the total rather than inventing a value.
		if haveBuffer {
			total := deepest
			if haveRTT {
				total += worstRTT / 2
			}
			s.metrics.TotalLatencyMs = total
			s.metrics.HasTotalLatency = true
			s.metrics.History = append(s.metrics.History, total)
			if len(s.metrics.History) > WindowSeconds {
				s.metrics.History = s.metrics.History[len(s.metrics.History)-WindowSeconds:]
			}
		}

		// One entry per report, which the engine sends once a second, so the
		// timeline is the session second by second. The gap counters are
		// cumulative, so what this second held is the difference from the
		// previous report.
		slot := Slot{
			Skipped:    diff(tm.SkippedPackets, s.lastSkipped),
			Recoveries: diff(tm.SenderLateRecoveries, s.lastRecoveries),
			Expired:    diff(expired, s.lastExpired),
		}
		if haveBuffer {
			slot.LatencyMs = s.metrics.TotalLatencyMs
		}
		s.lastSkipped, s.lastRecoveries, s.lastExpired = tm.SkippedPackets, tm.SenderLateRecoveries, expired
		s.metrics.Slots = append(s.metrics.Slots, slot)
		if len(s.metrics.Slots) > WindowSeconds {
			s.metrics.Slots = s.metrics.Slots[len(s.metrics.Slots)-WindowSeconds:]
		}

	case "stopped":
		s.metrics.Phase = PhaseStopped
		s.metrics.Detail = "stopped"
	case "warning":
		var w struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Recovered bool   `json:"recovered"`
			Skipped   uint64 `json:"skipped_packets"`
		}
		if json.Unmarshal(ev.Raw, &w) == nil {
			s.metrics.Detail = w.Message
			// The sender fell behind far enough to discard audio. That is the
			// stutter the user hears, so it is recorded even though the sender
			// recovered.
			if w.Code == "sender_late_recovered" {
				s.metrics.SenderLateRecoveries++
				s.metrics.SkippedPackets += w.Skipped
			}
		}
	case "error":
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(ev.Raw, &e) == nil {
			s.lastErr = e.Message
			s.metrics.Detail = e.Message
			s.metrics.Phase = PhaseFailed
			// A PIN that timed out arrives as an error. The prompt has to go with
			// it, or the panel keeps asking for a PIN nobody is waiting on.
			s.metrics.Pairing = false
			s.metrics.PairingOn = ""
		}
	}
}

// MediaMetrics is one member's media counters, as the engine reports them.
type MediaMetrics struct {
	PacketsSent              uint64  `json:"packets_sent"`
	MediaSendErrors          uint64  `json:"media_send_errors"`
	MediaSendBacklogged      uint64  `json:"media_send_backlogged"`
	MediaSendDropped         uint64  `json:"media_send_dropped"`
	SyncSendErrors           uint64  `json:"sync_send_errors"`
	RetransmitRequests       uint64  `json:"retransmit_requests"`
	RetransmitsSent          uint64  `json:"retransmits_sent"`
	RetransmitMissing        uint64  `json:"retransmit_missing"`
	RetransmitExpired        uint64  `json:"retransmit_expired"`
	RetransmitQueueDrops     uint64  `json:"retransmit_queue_drops"`
	RetransmitSendErrors     uint64  `json:"retransmit_send_errors"`
	ReceiverLatencyMs        float64 `json:"receiver_latency_ms"`
	ReceiverLatencyEstimated bool    `json:"receiver_latency_estimated"`
	BacklogPackets           int     `json:"media_backlog_packets"`
	BacklogLimitPackets      int     `json:"media_backlog_limit_packets"`
	AudioSendBufferBytes     int     `json:"audio_send_buffer_bytes"`
}

// Health is what the engine knows about the link to one member.
type Health struct {
	EventMessages     uint64 `json:"event_messages"`
	FeedbackSuccesses uint64 `json:"feedback_successes"`
	FeedbackFailures  uint64 `json:"feedback_failures"`
	// FeedbackRTTMs is the round trip of the engine's own feedback request.
	// This is the number that moves when the network's ping moves.
	FeedbackRTTMs   *float64 `json:"feedback_rtt_ms"`
	FeedbackDelayed bool     `json:"feedback_delayed"`
}

// MemberMetrics is one receiver's whole picture.
type MemberMetrics struct {
	Host   string       `json:"host"`
	Media  MediaMetrics `json:"media"`
	Health Health       `json:"health"`
}

// Capture is the local capture side's picture, as the engine reports it once a
// second.
type Capture struct {
	DroppedFrames      uint64   `json:"dropped_frames"`
	UnderrunPackets    uint64   `json:"underrun_packets"`
	CaptureFrames      uint64   `json:"capture_frames"`
	MaxQueueAgeMs      float64  `json:"max_queue_age_ms"`
	CaptureToSendP95Ms *float64 `json:"capture_to_send_p95_ms"`
	LastAudioQPCNs     uint64   `json:"last_audio_qpc_ns"`
	InputRate          uint32   `json:"input_rate"`
}

// TransportMetrics is one report from the engine.
type TransportMetrics struct {
	SessionUptimeMs      uint64          `json:"session_uptime_ms"`
	SenderLateRecoveries uint64          `json:"sender_late_recoveries"`
	SkippedPackets       uint64          `json:"skipped_packets"`
	MaxSendLatenessUs    uint64          `json:"max_send_lateness_us"`
	Members              []MemberMetrics `json:"members"`
}
