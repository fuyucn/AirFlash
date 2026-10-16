# Why playback still stutters when the ping rises

Read from the engine's send path. Every number here is a constant in the
source; none of it was measured on a real network.

## The chain

The engine sends one RTP packet every **7.98 ms** (352 frames at 44.1 kHz) and
tells the receiver to play each packet at `send time + latency`, where latency
is your target. The receiver holds that much audio and plays it on its own
clock.

## The rule that explains the symptom

A packet's play deadline is `sent_at + latency`. When the receiver misses a
packet it asks for it again, and the engine answers only if the resend can
still arrive in time:

    rtp.rs:135   pub fn retransmit_packet(&self, seq: u16, now: Instant) -> Retransmit {
    rtp.rs:138       if now >= p.deadline || now - p.sent_at >= self.retention {
    rtp.rs:139           Retransmit::Expired

So the window a resend has is:

    latency - (time already spent) - (round trip)

**A rising ping narrows that window.** Past a point, every resend is `Expired`,
the receiver plays over the gap, and you hear a stutter. This is the mechanism
behind "it stutters more when the ping is higher" — not a coincidence, and not
something the engine compensates for.

## What the engine measures, and what it ignores

`feedback_rtt_ms` is the engine timing its own `/feedback` request to the
receiver: the network's ping, as the engine sees it. It is reported in
`transport_metrics`. It is read in exactly two places, the field and the
assignment:

    transport.rs:184   pub feedback_rtt_ms: Option<f64>,
    transport.rs:398   s.metrics.feedback_rtt_ms = Some(started.elapsed()...);

Nothing branches on it. There is no jitter measurement anywhere in the engine.
**The ping is observed and then ignored.** Nothing adapts when the network
slows down.

## A second finding: your latency choice is not always what is used

At setup the engine asks the receiver for `latencyMin` and, when the receiver
answers, prefers it over the target you chose:

    session.rs:383   let receiver_latency = stream...get("latencyMin")...
    session.rs:387   member.media.configure_latency(
    session.rs:388       effective_latency.unwrap_or(Duration::from_millis(options.latency_ms)),
    session.rs:389       effective_latency.is_none(),

So a receiver that advertises `latencyMin` overrides the setting. Raising the
target from 200 ms to 500 ms helps only when the receiver is *not* dictating the
value. The `negotiated` event reports `receiver_latency_min_samples`, which says
which case you are in — the shell shows it once a session is running.

## The two paths that make a gap

**The sender falls behind.** `MediaSchedule::recover` compares now against the
deadline. Past `LATE_LIMIT` (60 ms) it discards the expired slots, jumps the
timeline forward and calls `abandon_backlog`, throwing away everything queued.
Counted as `sender_late_recoveries` and `skipped_packets`.

**A resend arrives too late.** Counted as `retransmit_expired` (the packet was
known but its deadline had passed) or `retransmit_missing` (too old to still be
in `MAX_HISTORY` = 512 packets).

These are different problems with different fixes, and until now the UI showed
neither. The shell now reads and displays both, so the next session says which
one is happening instead of guessing.

## Budgets that matter

| Constant | Value | What it bounds |
| --- | --- | --- |
| `RETRANSMIT_BUDGET` | 1 ms | Time per slot spent answering resend requests |
| `MAX_RESENDS` | 32 | Packets resent per slot |
| `MAX_REQUESTS` | 16 | Requests read per slot |
| `MAX_PENDING` | 512 | Queued resends; overflow is dropped, not deferred |
| `LATE_LIMIT` | 60 ms | How far behind the sender may fall |
| retention | `max(1 s, latency + 250 ms)` | How long a packet stays resendable |

The 1 ms budget is 12% of a 7.98 ms slot and is spent *after* that slot's packet
has gone out, so a slow resend loop eats into the next slot's deadline. A burst
of requests beyond `MAX_REQUESTS` is silently truncated rather than served late.

## What to change, in order of value

1. **Act on the round trip that is already measured.** Raise the effective
   target when the round trip and the recovery counters rise, lower it when the
   link is quiet. This is the direct fix for the symptom. It must happen at a
   session boundary or in a controlled step — never by rewriting the play time
   of audio already in flight.
2. **Measure jitter, not just a mean round trip.** One sample every two seconds
   cannot see the spikes that cause the gaps.
3. **Report `receiver_latency_min_samples` in the UI** so it is clear when the
   receiver, not the setting, is choosing the delay.
4. **Re-examine the 1 ms retransmit budget**, and the silent truncation at
   `MAX_REQUESTS`.
5. **Keep the counters visible.** `sender_late_recoveries`, `skipped_packets`,
   `retransmit_expired` and `retransmit_queue_drops` are the stutter, counted.

## What this does not claim

None of this was measured on a real network or a real HomePod. Finding 1 and the
retransmit rule are certain — they are the absence of code and a direct reading
of one condition. The relative weight of the two gap paths needs the counters
from a real session to settle.
