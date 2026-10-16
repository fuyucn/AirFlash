package engine

import (
	"encoding/json"
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
	PhaseFailed      StreamPhase = "failed"
	PhaseStopped     StreamPhase = "stopped"
)

// Metrics is the live picture the UI draws.
type Metrics struct {
	Phase        StreamPhase
	Detail       string
	RequestedMs  int
	MeasuredMs   float64
	HasMeasured  bool
	PacketsSent  uint64
	SendErrors   uint64
	SendDelayed  uint64
	MaxStallMs   float64
	BacklogDrops uint64
	SampleRate   int
	ClockID      string
	Updated      time.Time

	// Latency history feeds the trend chart, newest last.
	History []float64
}

// State is the shared, mutex-guarded app model.
type State struct {
	mu        sync.RWMutex
	metrics   Metrics
	peers     []Peer
	engine    Hello
	haveHello bool
	lastErr   string
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
			s.metrics.Phase = StreamPhase(p.Phase)
			s.metrics.Detail = p.Phase
		}
	case "streaming":
		var st struct {
			RequestedLatencyMs int    `json:"requested_latency_ms"`
			SampleRate         int    `json:"sample_rate"`
			ClockID            string `json:"clock_id"`
			Members            int    `json:"members"`
		}
		if json.Unmarshal(ev.Raw, &st) == nil {
			s.metrics.Phase = PhaseStreaming
			s.metrics.RequestedMs = st.RequestedLatencyMs
			s.metrics.SampleRate = st.SampleRate
			s.metrics.ClockID = st.ClockID
			s.metrics.Detail = "streaming"
		}
	case "negotiated", "handshake_complete", "peer_info":
		s.metrics.Phase = PhaseNegotiating
	case "transport_metrics":
		var tm struct {
			Metrics TransportMetrics `json:"metrics"`
		}
		if json.Unmarshal(ev.Raw, &tm) == nil {
			m := tm.Metrics
			s.metrics.PacketsSent = m.PacketsSent
			s.metrics.SendErrors = m.SendErrors
			s.metrics.SendDelayed = m.SendDelayed
			s.metrics.BacklogDrops = m.BacklogDrops
			if m.MaxStallMs > s.metrics.MaxStallMs {
				s.metrics.MaxStallMs = m.MaxStallMs
			}
			if m.MeasuredLatencyMs > 0 {
				s.metrics.MeasuredMs = m.MeasuredLatencyMs
				s.metrics.HasMeasured = true
				s.metrics.History = append(s.metrics.History, m.MeasuredLatencyMs)
				if len(s.metrics.History) > 180 {
					s.metrics.History = s.metrics.History[len(s.metrics.History)-180:]
				}
			}
		}
	case "stopped":
		s.metrics.Phase = PhaseStopped
		s.metrics.Detail = "stopped"
	case "warning":
		var w struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(ev.Raw, &w) == nil {
			s.metrics.Detail = w.Message
		}
	case "error":
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(ev.Raw, &e) == nil {
			s.lastErr = e.Message
			s.metrics.Detail = e.Message
			s.metrics.Phase = PhaseFailed
		}
	}
}
