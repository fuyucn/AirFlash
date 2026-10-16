package engine

import (
	"encoding/json"
	"testing"
)

// transportEvent is a report shaped exactly like the engine's, taken from its
// own transport_metrics function.
func transportEvent(t *testing.T, members string) Event {
	t.Helper()
	raw := `{"version":1,"id":"c1","session_id":"s","event":"transport_metrics",
	  "session_uptime_ms":4000,"sender_late_recoveries":0,"skipped_packets":0,
	  "max_send_lateness_us":900,"members":[` + members + `]}`
	var ev Event
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatal(err)
	}
	ev.Raw = json.RawMessage(raw)
	return ev
}

const oneMember = `{
  "host":"10.0.10.31",
  "media":{"packets_sent":500,"media_send_errors":0,"media_send_backlogged":3,
    "media_send_dropped":1,"sync_send_errors":0,"retransmit_requests":7,
    "retransmits_sent":6,"retransmit_missing":1,"retransmit_expired":0,
    "retransmit_queue_drops":0,"retransmit_send_errors":0,
    "receiver_latency_ms":480.0,"receiver_latency_estimated":false,
    "media_backlog_packets":2,"media_backlog_limit_packets":47,
    "audio_send_buffer_bytes":65536},
  "health":{"event_messages":12,"feedback_successes":8,"feedback_failures":0,
    "feedback_rtt_ms":18.5,"feedback_delayed":false}}`

func TestTransportMetricsAreReadFromTheEnginesRealShape(t *testing.T) {
	// The engine nests everything under members[].media and members[].health.
	// Reading a flat "metrics" object yields zeros, which is exactly the bug
	// this test exists to catch.
	st := NewState()
	st.Apply(transportEvent(t, oneMember))
	m := st.Snapshot()

	if m.PacketsSent != 500 {
		t.Errorf("packets sent = %d, want 500", m.PacketsSent)
	}
	if !m.HasReceiverBuffer || m.ReceiverBufferMs != 480 {
		t.Errorf("receiver buffer = %v (have=%v), want 480", m.ReceiverBufferMs, m.HasReceiverBuffer)
	}
	if !m.HasNetworkRTT || m.NetworkRTTMs != 18.5 {
		t.Errorf("network rtt = %v (have=%v), want 18.5", m.NetworkRTTMs, m.HasNetworkRTT)
	}
	if m.RetransmitRequests != 7 {
		t.Errorf("retransmit requests = %d, want 7", m.RetransmitRequests)
	}
	if m.RetransmitMissing != 1 {
		t.Errorf("retransmit missing = %d, want 1", m.RetransmitMissing)
	}
	if m.MediaSendDropped != 1 {
		t.Errorf("dropped on send = %d, want 1", m.MediaSendDropped)
	}
}

func TestTotalLatencyIsTheBufferPlusHalfTheRoundTrip(t *testing.T) {
	// What is heard is the receiver's buffer plus one-way transit. Getting this
	// wrong makes the headline number meaningless.
	st := NewState()
	st.Apply(transportEvent(t, oneMember))
	m := st.Snapshot()

	want := 480.0 + 18.5/2
	if !m.HasTotalLatency {
		t.Fatal("total latency was not computed")
	}
	if diff := m.TotalLatencyMs - want; diff > 0.001 || diff < -0.001 {
		t.Errorf("total = %v, want %v", m.TotalLatencyMs, want)
	}
}

func TestMissingRoundTripStillGivesATotal(t *testing.T) {
	// A missing measurement must lower the total, never invent a value.
	member := `{"host":"10.0.10.31",
	  "media":{"receiver_latency_ms":500.0},"health":{"feedback_rtt_ms":null}}`
	st := NewState()
	st.Apply(transportEvent(t, member))
	m := st.Snapshot()

	if m.HasNetworkRTT {
		t.Error("a null round trip must not count as measured")
	}
	if !m.HasTotalLatency {
		t.Fatal("the buffer alone should still give a total")
	}
	if m.TotalLatencyMs != 500 {
		t.Errorf("total = %v, want the buffer alone (500)", m.TotalLatencyMs)
	}
}

func TestWithNoMeasurementAtAllThereIsNoTotal(t *testing.T) {
	member := `{"host":"10.0.10.31","media":{},"health":{}}`
	st := NewState()
	st.Apply(transportEvent(t, member))
	m := st.Snapshot()

	if m.HasTotalLatency {
		t.Errorf("total = %v, want none: nothing was observed", m.TotalLatencyMs)
	}
}

func TestAStereoPairTakesTheDeepestBufferAndWorstRoundTrip(t *testing.T) {
	// With two speakers the slower one bounds what is heard, so the larger
	// values are the honest ones.
	left := `{"host":"10.0.10.31",
	  "media":{"receiver_latency_ms":400.0,"packets_sent":100},
	  "health":{"feedback_rtt_ms":10.0}}`
	right := `{"host":"10.0.10.32",
	  "media":{"receiver_latency_ms":520.0,"packets_sent":120},
	  "health":{"feedback_rtt_ms":35.0}}`
	st := NewState()
	st.Apply(transportEvent(t, left+","+right))
	m := st.Snapshot()

	if m.ReceiverBufferMs != 520 {
		t.Errorf("buffer = %v, want the deeper 520", m.ReceiverBufferMs)
	}
	if m.NetworkRTTMs != 35 {
		t.Errorf("rtt = %v, want the worse 35", m.NetworkRTTMs)
	}
	// Counters add up across both halves.
	if m.PacketsSent != 220 {
		t.Errorf("packets = %d, want both members summed (220)", m.PacketsSent)
	}
}

func TestASenderStallIsCountedFromItsWarning(t *testing.T) {
	// The sender recovering is good, but the audio it threw away is the
	// stutter, so it must be recorded.
	raw := `{"version":1,"id":"c1","session_id":"s","event":"warning",
	  "code":"sender_late_recovered","channel":"scheduler","recovered":true,
	  "skipped_packets":12,"message":"Sender discarded expired audio slots"}`
	var ev Event
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatal(err)
	}
	ev.Raw = json.RawMessage(raw)

	st := NewState()
	st.Apply(ev)
	m := st.Snapshot()

	if m.SenderLateRecoveries != 1 {
		t.Errorf("recoveries = %d, want 1", m.SenderLateRecoveries)
	}
	if m.SkippedPackets != 12 {
		t.Errorf("skipped = %d, want 12", m.SkippedPackets)
	}
}

func TestTheLatencyHistoryGrowsWithEachReport(t *testing.T) {
	st := NewState()
	for i := 0; i < 5; i++ {
		st.Apply(transportEvent(t, oneMember))
	}
	if got := len(st.Snapshot().History); got != 5 {
		t.Errorf("history has %d points, want 5", got)
	}
}

func TestTheHistoryIsBounded(t *testing.T) {
	// An unbounded slice would grow for the length of a session.
	st := NewState()
	for i := 0; i < 400; i++ {
		st.Apply(transportEvent(t, oneMember))
	}
	if got := len(st.Snapshot().History); got > 180 {
		t.Errorf("history has %d points, want it capped at 180", got)
	}
}
