# Media send backpressure and the stutter report

## The reported symptom

A Windows 11 user streaming to a first-generation HomePod (`AudioAccessory1,1`)
reported periodic audible stutter at the 120 ms and 200 ms latency profiles. The
same session became smooth at 500 ms but still occasionally skipped. A local log
sample captured 103 `media_send_delayed` warnings, all reporting
`A non-blocking socket operation could not be completed immediately. (os error 10035)`,
each paired with a `media_send_recovered` warning.

## What the log establishes

`0x10035` is `WSAEWOULDBLOCK`: the local UDP send path was full when the packet
was offered, so the datagram was never transmitted. The warnings were
edge-triggered, so each pair marks the start and end of one stall rather than one
lost packet. In the sample:

- 103 stalls across roughly 1 hour 36 minutes of logged streaming.
- Stall duration median 44 ms, p90 154 ms, maximum 213 ms.
- The first stall arrived 10-22 seconds into a session, then recurred every
  2-7 seconds.
- No `sender_late_recovered`, `socket_error`, `read_timeout` or authentication
  warning appeared, and every `Engine process-exit` was code 0.

The sender scheduler never fell behind, so this was not capture starvation or a
late media loop. It was local send-path congestion that discarded audio.

## Why it was audible

One packet carries 352 stereo frames: 8.1 ms of audio at 44.1 kHz. Before this
change, `MediaTransport::observe_send` counted a `WouldBlock` as
`media_send_errors` and dropped the packet immediately, without retrying. A stall
therefore discarded every packet the socket refused for its whole duration.

The receiver's jitter budget decides whether that is audible:

| Profile | Budget | 44 ms stall | 154 ms stall | 213 ms stall |
| --- | --- | --- | --- | --- |
| realtime | 120 ms | hidden | audible | audible |
| normal | 200 ms | hidden | hidden | audible |
| buffered | 500 ms | hidden | hidden | hidden |

This matches the report exactly: 120 ms stuttered often, 200 ms stuttered
occasionally, and 500 ms was smooth with rare skips. Raising the profile did not
fix the network; it only made the loss less likely to exceed the budget. That is
why the 500 ms workaround was still imperfect, and why it raised latency enough
to make lip-sync visibly wrong on video.

## The change

The sender now treats local backpressure as congestion to absorb rather than loss
to discard:

- `MediaTransport` keeps a bounded `backlog` of encoded packets. A `WouldBlock`
  retains the packet and reports `media_send_backlogged`; the next media tick
  retries it.
- The backlog is bounded by the negotiated jitter budget, using
  `Packetizer::latency_slots`. It holds at most three quarters of the budget, so
  a retained packet still arrives before the timestamp the receiver plays it at.
  Older slots beyond that bound are dropped as `media_send_dropped`, because
  audio that can no longer be played on time is not worth sending.
- A hard socket failure is still counted as `media_send_errors` and is not
  retried. The twelve-second `media_send_timeout` watchdog now covers both a
  permanently blocked and a permanently failing send path.
- `MediaTransport::new` requests a larger `SO_SNDBUF` for the audio and control
  sockets. The request is best effort: Windows clamps it to the stack maximum, so
  the granted size is read back with `getsockopt` and reported.
- After a scheduler stall, `session.rs` abandons the backlog alongside the
  existing `skip_packets` recovery, so pre-stall audio cannot inflate latency on
  the recovered timeline.

The warning edges, receiver semantics, RTP encryption, retransmission behaviour
and the twelve-second failure budget are unchanged.

## What this does and does not fix

Absorbing a short stall keeps audio continuous instead of dropping it, so a
session can now stay smooth at a lower latency profile. The bound means the fix
cannot trade one problem for another: it never holds audio longer than the
receiver can still use, and a stall longer than the jitter budget still produces
a gap rather than growing latency without limit.

This change does not establish why the local send path stalls. Candidate causes
remain outside AirFlash: a VPN or proxy TUN driver, third-party network filter
drivers, NIC power saving, Wi-Fi airtime contention, or an occupied uplink. A
real fix for those belongs to the host configuration, not to this repository.

## Verify on Windows

Play a session and open **Settings > Monitor**. Each member now reports:

- `Send backpressure` and `dropped backlog` counts.
- `Sender backlog: N of M packets (bytes); send buffer: bytes`.

A healthy session shows `send errors: 0` with `dropped backlog: 0`. Backpressure
counts that rise while `send errors` stays 0 indicate the new path is absorbing a
stall that previously produced packet loss. Dropped backlog counts mean the stall
exceeded the jitter budget, which still needs a host-side fix or a higher latency
profile.

Automated coverage: `cargo test` in `native/airflash-engine` exercises the backlog
bound, the retain-and-drain path, the hard-failure and watchdog paths, and the
latency-slot arithmetic. `dotnet test desktop/AirFlash.sln` covers the desktop
metric parsing and the latency estimate. Neither replaces a real HomePod or the
affected network.

## Reported latency

The Monitor page states each latency component separately instead of a single
unqualified number, because only some of them are measured:

| Field | Source | Nature |
| --- | --- | --- |
| Target latency | The selected profile | Configured, not measured |
| Receiver jitter buffer | `latencyMin` returned by the receiver in SETUP | Receiver-reported |
| Local capture delay | `capture_to_send_p95_ms` from the WASAPI capture queue | Measured locally |
| Measured network round trip | AirPlay feedback request round trip | Measured |
| Estimated audible delay | Buffer + capture delay + half the round trip | Estimate |

The engine already received the receiver's `latencyMin` during SETUP but only
reported it inside a per-member text summary. The estimate is computed by
`LatencyEstimate.From`, which sums only observed parts, so a missing measurement
lowers the total instead of inventing a value. When the receiver does not report
a buffer, the requested profile is used and labelled "requested, not
receiver-reported".

The estimate excludes the receiver's own output path and any host processing
before capture, so it is not an acoustic measurement. This is why the page keeps
the label "estimated" rather than "measured".
