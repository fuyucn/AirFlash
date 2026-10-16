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
	for i := 0; i < WindowSeconds+50; i++ {
		st.Apply(transportEvent(t, oneMember))
	}
	if got := len(st.Snapshot().History); got > WindowSeconds {
		t.Errorf("history has %d points, want it capped at %d", got, WindowSeconds)
	}
}

// cumulativeReport is a report whose counters have run up to total, the way the
// engine sends them: it reports the session's running total every second, not
// what happened in the last one.
func cumulativeReport(t *testing.T, total uint64) Event {
	t.Helper()
	member := `{"host":"10.0.10.31","media":{"packets_sent":` +
		itoa(total) + `,"retransmit_requests":` + itoa(total/10) +
		`,"retransmit_expired":` + itoa(total/100) +
		`,"receiver_latency_ms":480.0},"health":{"feedback_failures":` +
		itoa(total/20) + `,"feedback_rtt_ms":18.5}}`
	return transportEvent(t, member)
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

func TestCumulativeCountersAreNotAddedUpAgain(t *testing.T) {
	// The engine reports a running total, so three reports of 125, 250 and 375
	// packets mean 375 packets were sent. Adding the reports together would
	// claim 750, and the counters are what the user reads to judge a stutter.
	st := NewState()
	for _, total := range []uint64{125, 250, 375} {
		st.Apply(cumulativeReport(t, total))
	}
	m := st.Snapshot()

	if m.PacketsSent != 375 {
		t.Errorf("packets sent = %d, want the engine's own total of 375", m.PacketsSent)
	}
	if m.RetransmitRequests != 37 {
		t.Errorf("retransmit requests = %d, want 37", m.RetransmitRequests)
	}
	if m.FeedbackFailures != 18 {
		t.Errorf("feedback failures = %d, want 18", m.FeedbackFailures)
	}
}

func TestAStereoPairStillAddsItsTwoMembersUp(t *testing.T) {
	// Each half of a pair keeps its own counters, so across members the numbers
	// do add - the fix is only about not adding one member's total to itself.
	left := `{"host":"10.0.10.31","media":{"packets_sent":100},
	  "health":{"feedback_failures":3}}`
	right := `{"host":"10.0.10.32","media":{"packets_sent":120},
	  "health":{"feedback_failures":4}}`
	st := NewState()
	st.Apply(transportEvent(t, left+","+right))
	m := st.Snapshot()

	if m.PacketsSent != 220 {
		t.Errorf("packets sent = %d, want 220", m.PacketsSent)
	}
	if m.FeedbackFailures != 7 {
		t.Errorf("feedback failures = %d, want 7", m.FeedbackFailures)
	}
}

func TestTheSlotTimelineRecordsWhereAGapWas(t *testing.T) {
	// A latency chart shows the network. This is the audio: one entry per
	// second, saying which seconds actually lost something.
	st := NewState()
	st.Apply(transportEvent(t, oneMember))
	st.Apply(gapReport(t, 0, 0, 0))
	st.Apply(gapReport(t, 5, 1, 2))
	st.Apply(gapReport(t, 5, 1, 2))

	slots := st.Snapshot().Slots
	if len(slots) != 4 {
		t.Fatalf("got %d slots, want one per report (4)", len(slots))
	}
	if slots[1].Gapped() {
		t.Error("a second with nothing lost was marked as a gap")
	}
	if got := slots[2].Skipped; got != 5 {
		t.Errorf("skipped = %d, want the 5 that were new in that second", got)
	}
	if got := slots[2].Recoveries; got != 1 {
		t.Errorf("recoveries = %d, want 1", got)
	}
	if got := slots[2].Expired; got != 2 {
		t.Errorf("expired = %d, want 2", got)
	}
	// The counters did not move between the last two reports, so the last
	// second lost nothing. Storing the total would have marked it as a gap too.
	if slots[3].Gapped() {
		t.Error("a second whose counters did not move was marked as a gap")
	}
}

func gapReport(t *testing.T, skipped, recoveries, expired uint64) Event {
	t.Helper()
	raw := `{"version":1,"id":"c1","session_id":"s","event":"transport_metrics",
	  "session_uptime_ms":4000,"sender_late_recoveries":` + itoa(recoveries) +
		`,"skipped_packets":` + itoa(skipped) + `,"max_send_lateness_us":900,
	  "members":[{"host":"10.0.10.31","media":{"retransmit_expired":` + itoa(expired) +
		`,"receiver_latency_ms":480.0},"health":{"feedback_rtt_ms":18.5}}]}`
	var ev Event
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatal(err)
	}
	ev.Raw = json.RawMessage(raw)
	return ev
}

func TestTheSlotTimelineIsBounded(t *testing.T) {
	st := NewState()
	for i := 0; i < WindowSeconds+50; i++ {
		st.Apply(gapReport(t, 0, 0, 0))
	}
	if got := len(st.Snapshot().Slots); got > WindowSeconds {
		t.Errorf("timeline has %d slots, want it capped at %d", got, WindowSeconds)
	}
}

func TestANewSessionStartsTheTimelineOver(t *testing.T) {
	// Counters restart with the session, so carrying the old timeline over
	// would report the previous session's gaps as this one's.
	st := NewState()
	st.Apply(gapReport(t, 9, 3, 4))
	st.ResetSession()

	m := st.Snapshot()
	if len(m.Slots) != 0 {
		t.Errorf("a new session kept %d slots from the last one", len(m.Slots))
	}
	// And the next report must not read the restart as a negative difference.
	st.Apply(gapReport(t, 0, 0, 0))
	if got := st.Snapshot().Slots; len(got) != 1 || got[0].Gapped() {
		t.Errorf("after a restart the first slot = %+v, want one clean slot", got)
	}
}

func TestTheTwoChartsCoverTheSameSpan(t *testing.T) {
	// The latency chart and the gap ribbon sit one above the other and are read
	// against each other: a spike and the gap it caused have to line up. They
	// only do if both keep the same number of seconds, so this pins that down.
	st := NewState()
	for i := 0; i < WindowSeconds+40; i++ {
		st.Apply(cumulativeReport(t, uint64(i+1)*125))
	}
	m := st.Snapshot()
	if len(m.History) != len(m.Slots) {
		t.Errorf("latency has %d points and the timeline has %d slots: "+
			"a spike and its gap would sit at different places",
			len(m.History), len(m.Slots))
	}
}
