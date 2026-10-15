//! Session health and bounded media recovery, shared by production and localhost tests.
use crate::{
    crypto::derive,
    rtp::{self, FRAMES, Packetizer, Retransmit},
    rtsp::{Cancellation, Connection, WireError},
};
use anyhow::{Context, Result};
use serde::Serialize;
use serde_json::{Value, json};
use std::{
    collections::VecDeque,
    net::{SocketAddr, UdpSocket},
    sync::{Arc, Mutex},
    thread::{self, JoinHandle},
    time::{Duration, Instant},
};

pub const FEEDBACK_INTERVAL: Duration = Duration::from_secs(2);
pub const FEEDBACK_SOFT_TIMEOUT: Duration = Duration::from_secs(4);
pub const FAILURE_TIMEOUT: Duration = Duration::from_secs(12);
pub const FEEDBACK_FAILURE_LIMIT: u32 = 3;
pub const LATE_LIMIT: Duration = Duration::from_millis(60);
const POLL: Duration = Duration::from_millis(20);
const RETRANSMIT_BUDGET: Duration = Duration::from_millis(1);
const MAX_REQUESTS: usize = 16;
const MAX_RESENDS: usize = 32;
const MAX_PENDING: usize = 512;
// A blocked UDP socket means the local send path is briefly full, not that the
// packet is unusable. Requested kernel headroom and a bounded sender backlog
// turn those drops into bounded delay that the receiver jitter buffer can hide.
const AUDIO_SEND_BUFFER_BYTES: i32 = 128 * 1024;
const CONTROL_SEND_BUFFER_BYTES: i32 = 128 * 1024;
const MAX_FLUSH_PACKETS: usize = 64;
const MIN_BACKLOG_PACKETS: u64 = 8;
const MAX_BACKLOG_PACKETS: u64 = 128;

/// Outcome of one media datagram attempt.
#[derive(Debug)]
pub enum SendDisposition {
    /// The kernel accepted the datagram.
    Sent,
    /// The local send path was full; the caller keeps the packet for a retry.
    Retained,
    /// The socket rejected the datagram for a reason that is not backpressure.
    Discarded,
}

/// Local send-path congestion, reported by Windows as `WSAEWOULDBLOCK` (10035).
///
/// This is a full send buffer, not a broken route or a dead receiver, so the
/// packet is worth retrying rather than discarding.
fn is_send_backpressure(error: &std::io::Error) -> bool {
    matches!(
        error.kind(),
        std::io::ErrorKind::WouldBlock | std::io::ErrorKind::Interrupted
    )
}

/// Ask the kernel for more send-buffer headroom, returning the granted size.
///
/// Windows silently clamps the request to the stack maximum, so the granted
/// value is what the accounting below uses.
fn request_send_buffer(socket: &UdpSocket, requested: i32) -> i32 {
    #[cfg(windows)]
    {
        use std::os::windows::io::AsRawSocket;
        use windows::Win32::Networking::WinSock::{
            SOCKET, SOL_SOCKET, SO_SNDBUF, getsockopt, setsockopt,
        };
        let raw = SOCKET(socket.as_raw_socket() as usize);
        let requested_bytes = requested.to_ne_bytes();
        // Failure is not fatal: the default buffer still works, just smaller.
        if unsafe { setsockopt(raw, SOL_SOCKET, SO_SNDBUF, Some(&requested_bytes)) } != 0 {
            return 0;
        }
        let mut granted: i32 = 0;
        let mut length = std::mem::size_of::<i32>() as i32;
        let ok = unsafe {
            getsockopt(
                raw,
                SOL_SOCKET,
                SO_SNDBUF,
                windows::core::PSTR((&mut granted as *mut i32).cast()),
                &mut length,
            )
        };
        if ok == 0 { granted } else { 0 }
    }
    #[cfg(not(windows))]
    {
        let _ = (socket, requested);
        0
    }
}

#[derive(Clone, Debug, Serialize)]
pub struct Fault {
    pub code: String,
    pub host: String,
    pub channel: String,
    pub retryable: bool,
    pub message: String,
}
impl std::fmt::Display for Fault {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "{} [{} {}: {}]",
            self.message, self.host, self.channel, self.code
        )
    }
}
impl std::error::Error for Fault {}
impl Fault {
    pub fn new(
        host: impl ToString,
        channel: &str,
        code: &str,
        retryable: bool,
        message: impl ToString,
    ) -> Self {
        Self {
            code: code.into(),
            host: host.to_string(),
            channel: channel.into(),
            retryable,
            message: message.to_string(),
        }
    }
    pub fn from_error(host: impl ToString, channel: &str, error: &anyhow::Error) -> Self {
        if let Some(fault) = error.downcast_ref::<Self>() {
            return fault.clone();
        }
        let (code, retryable) = if let Some(wire) = error.downcast_ref::<WireError>() {
            match wire {
                WireError::Cancelled => ("cancelled", false),
                WireError::PeerClosed => ("peer_closed", true),
                WireError::ReadTimeout => ("read_timeout", true),
                WireError::WriteTimeout | WireError::Poisoned => ("write_failed", true),
                WireError::Authentication => ("authentication_failed", false),
            }
        } else if let Some(io) = error.downcast_ref::<std::io::Error>() {
            match io.kind() {
                std::io::ErrorKind::ConnectionReset => ("connection_reset", true),
                _ => ("socket_error", true),
            }
        } else {
            ("protocol_error", false)
        };
        Self::new(host, channel, code, retryable, format!("{error:#}"))
    }
    pub fn event(&self) -> Value {
        let mut value = serde_json::to_value(self).unwrap();
        value["event"] = json!("error");
        value["qualified"] = json!(false);
        value
    }
}
pub fn error_event(error: &anyhow::Error) -> Value {
    if let Some(fault) = error.downcast_ref::<Fault>() {
        return fault.event();
    }
    // Older authentication/capture code carries anyhow context rather than Fault.
    let message = format!("{error:#}");
    let lower = message.to_lowercase();
    let authentication = [
        "pairing",
        "srp",
        "verification",
        "credentials",
        "signature",
        "identity",
        "authentication",
    ]
    .iter()
    .any(|s| lower.contains(s));
    json!({"event":"error", "message":message, "code":if authentication {"authentication_failed"} else {"engine_error"}, "host":null, "channel":null, "retryable":!authentication, "qualified":false})
}
#[derive(Default, Clone, Serialize)]
pub struct HealthSnapshot {
    pub event_messages: u64,
    pub feedback_successes: u64,
    pub feedback_failures: u64,
    pub feedback_rtt_ms: Option<f64>,
    pub feedback_delayed: bool,
}
#[derive(Default)]
struct HealthState {
    metrics: HealthSnapshot,
    failure: Option<Fault>,
    notices: VecDeque<Value>,
}
#[derive(Clone, Default)]
pub struct Health(Arc<Mutex<HealthState>>);
impl Health {
    pub fn snapshot(&self) -> HealthSnapshot {
        self.0.lock().unwrap().metrics.clone()
    }
    pub fn check(&self) -> Result<()> {
        if let Some(error) = &self.0.lock().unwrap().failure {
            return Err(error.clone().into());
        }
        Ok(())
    }
    pub fn notices(&self) -> Vec<Value> {
        self.0.lock().unwrap().notices.drain(..).collect()
    }
    fn fail(&self, fault: Fault) {
        let mut state = self.0.lock().unwrap();
        if state.failure.is_none() {
            state.failure = Some(fault);
        }
    }
    fn notice(&self, host: &str, channel: &str, code: &str, message: &str, recovered: bool) {
        let mut state = self.0.lock().unwrap();
        if state.notices.len() == 16 {
            state.notices.pop_front();
        }
        state.notices.push_back(json!({"event":"warning","host":host,"channel":channel,"code":code,"message":message,"recovered":recovered}));
    }
}
pub struct EventWorker {
    stop: Cancellation,
    worker: Option<JoinHandle<()>>,
}
impl EventWorker {
    pub fn connect(addr: SocketAddr, key: &[u8], health: Health) -> Result<Self> {
        let stop = Cancellation::default();
        let mut connection = Connection::connect(addr, stop.clone())?;
        connection.encrypt(
            derive(key, "Events-Salt", "Events-Read-Encryption-Key"),
            derive(key, "Events-Salt", "Events-Write-Encryption-Key"),
        );
        Self::start(connection, addr.ip().to_string(), health, stop)
    }
    pub fn start(
        mut connection: Connection,
        host: String,
        health: Health,
        stop: Cancellation,
    ) -> Result<Self> {
        let done = stop.clone();
        let worker = thread::Builder::new()
            .name("airplay-events".into())
            .spawn(move || {
                if let Err(error) = event_loop(&mut connection, &health, &done) {
                    if !done.is_cancelled() {
                        health.fail(Fault::from_error(host, "events", &error));
                    }
                }
            })?;
        Ok(Self {
            stop,
            worker: Some(worker),
        })
    }
}
fn event_loop(connection: &mut Connection, health: &Health, stop: &Cancellation) -> Result<()> {
    while !stop.is_cancelled() {
        let Some(msg) = connection.read_for(POLL, Some(stop))? else {
            continue;
        };
        let protocol = msg
            .first
            .split_whitespace()
            .last()
            .filter(|v| matches!(*v, "HTTP/1.1" | "HTTP/1.0" | "RTSP/1.0"))
            .context("invalid event protocol")?;
        let cseq = msg
            .headers
            .get("cseq")
            .map(|v| format!("CSeq: {v}\r\n"))
            .unwrap_or_default();
        connection.write(
            format!("{protocol} 200 OK\r\n{cseq}Content-Length: 0\r\nAudio-Latency: 0\r\n\r\n")
                .as_bytes(),
        )?;
        health.0.lock().unwrap().metrics.event_messages += 1;
    }
    Ok(())
}
impl Drop for EventWorker {
    fn drop(&mut self) {
        self.stop.cancel();
        if let Some(worker) = self.worker.take() {
            let _ = worker.join();
        }
    }
}

#[derive(Clone, Copy)]
pub struct FeedbackTiming {
    pub interval: Duration,
    pub soft: Duration,
    pub hard: Duration,
}
impl Default for FeedbackTiming {
    fn default() -> Self {
        Self {
            interval: FEEDBACK_INTERVAL,
            soft: FEEDBACK_SOFT_TIMEOUT,
            hard: FAILURE_TIMEOUT,
        }
    }
}
pub struct FeedbackWorker {
    stop: Cancellation,
    worker: Option<JoinHandle<()>>,
}
impl FeedbackWorker {
    pub fn start(
        connection: Arc<Mutex<Connection>>,
        host: String,
        health: Health,
        timing: FeedbackTiming,
    ) -> Result<Self> {
        let stop = Cancellation::default();
        let done = stop.clone();
        let worker = thread::Builder::new()
            .name("airplay-feedback".into())
            .spawn(move || {
                if let Err(error) = feedback_loop(&connection, &host, &health, &done, timing) {
                    if !done.is_cancelled() {
                        let fault = Fault::from_error(host, "feedback", &error);
                        if fault.code != "feedback_rejected" {
                            health.0.lock().unwrap().metrics.feedback_failures += 1;
                        }
                        health.fail(fault);
                    }
                }
            })?;
        Ok(Self {
            stop,
            worker: Some(worker),
        })
    }
}
fn feedback_loop(
    connection: &Mutex<Connection>,
    host: &str,
    health: &Health,
    stop: &Cancellation,
    timing: FeedbackTiming,
) -> Result<()> {
    let mut failures = 0;
    let mut next = Instant::now() + timing.interval;
    while !stop.is_cancelled() {
        if Instant::now() < next {
            thread::sleep(POLL.min(next.saturating_duration_since(Instant::now())));
            continue;
        }
        let mut connection = connection.lock().unwrap();
        let started = Instant::now();
        connection.begin_request("POST", "/feedback", &[], &[])?;
        let response = loop {
            stop.check()?;
            let elapsed = started.elapsed();
            if elapsed >= timing.hard {
                return Err(Fault::new(
                    host,
                    "feedback",
                    "feedback_timeout",
                    true,
                    "Receiver feedback exceeded the recovery deadline",
                )
                .into());
            }
            if elapsed >= timing.soft {
                let notify = {
                    let mut s = health.0.lock().unwrap();
                    let notify = !s.metrics.feedback_delayed;
                    s.metrics.feedback_delayed = true;
                    notify
                };
                if notify {
                    health.notice(
                        host,
                        "feedback",
                        "feedback_delayed",
                        "Receiver feedback is delayed; audio continues",
                        false,
                    );
                }
            }
            if let Some(response) =
                connection.read_for(POLL.min(timing.hard - elapsed), Some(stop))?
            {
                if !connection.stale_response(&response) { break response; }
            }
        };
        connection.validate_cseq(&response)?;
        let status = response.status()?;
        if status == 200 {
            failures = 0;
            let recovered = {
                let mut s = health.0.lock().unwrap();
                s.metrics.feedback_successes += 1;
                s.metrics.feedback_rtt_ms = Some(started.elapsed().as_secs_f64() * 1000.0);
                let recovered = s.metrics.feedback_delayed;
                s.metrics.feedback_delayed = false;
                recovered
            };
            if recovered {
                health.notice(
                    host,
                    "feedback",
                    "feedback_recovered",
                    "Receiver feedback recovered",
                    true,
                );
            }
        } else if matches!(status, 500 | 502 | 503 | 504) {
            failures += 1;
            {
                let mut s = health.0.lock().unwrap();
                s.metrics.feedback_failures += 1;
                s.metrics.feedback_delayed = true;
            }
            if failures >= FEEDBACK_FAILURE_LIMIT {
                return Err(Fault::new(
                    host,
                    "feedback",
                    "feedback_rejected",
                    true,
                    format!("Receiver feedback failed {failures} times (status {status})"),
                )
                .into());
            }
            health.notice(
                host,
                "feedback",
                "feedback_retry",
                &format!("Receiver feedback returned {status}; retrying while audio continues"),
                false,
            );
        } else {
            return Err(Fault::new(
                host,
                "feedback",
                if matches!(status, 401 | 403) {
                    "authentication_failed"
                } else {
                    "session_rejected"
                },
                status == 454,
                format!("Receiver feedback rejected: {}", response.first),
            )
            .into());
        }
        drop(connection);
        next = Instant::now() + timing.interval;
    }
    Ok(())
}
impl Drop for FeedbackWorker {
    fn drop(&mut self) {
        self.stop.cancel();
        if let Some(worker) = self.worker.take() {
            let _ = worker.join();
        }
    }
}

#[derive(Default, Clone, Serialize)]
pub struct MediaMetrics {
    pub packets_sent: u64,
    pub media_send_errors: u64,
    pub media_send_backlogged: u64,
    pub media_send_dropped: u64,
    pub media_backlog_peak_bytes: u64,
    pub sync_send_errors: u64,
    pub control_receive_errors: u64,
    pub retransmit_requests: u64,
    pub retransmit_requested_packets: u64,
    pub retransmits_sent: u64,
    pub retransmit_missing: u64,
    pub retransmit_expired: u64,
    pub retransmit_send_errors: u64,
    pub retransmit_queue_drops: u64,
    pub invalid_requests: u64,
}
pub struct MediaTransport {
    pub packetizer: Packetizer,
    pub metrics: MediaMetrics,
    audio: UdpSocket,
    control: UdpSocket,
    remote: SocketAddr,
    pending: VecDeque<(u16, SocketAddr)>,
    backlog: VecDeque<Vec<u8>>,
    backlog_bytes: usize,
    audio_send_buffer: i32,
    control_send_buffer: i32,
    latency: Duration,
    estimated: bool,
    last_success: Instant,
    send_warning: bool,
}
impl MediaTransport {
    pub fn new(
        packetizer: Packetizer,
        audio: UdpSocket,
        control: UdpSocket,
        remote: SocketAddr,
    ) -> Result<Self> {
        audio.set_nonblocking(true)?;
        control.set_nonblocking(true)?;
        // Best effort: a larger kernel queue lets a brief local stall drain
        // instead of returning WSAEWOULDBLOCK on the very next datagram.
        let audio_send_buffer = request_send_buffer(&audio, AUDIO_SEND_BUFFER_BYTES);
        let control_send_buffer = request_send_buffer(&control, CONTROL_SEND_BUFFER_BYTES);
        Ok(Self {
            packetizer,
            metrics: MediaMetrics::default(),
            audio,
            control,
            remote,
            pending: VecDeque::new(),
            backlog: VecDeque::new(),
            backlog_bytes: 0,
            audio_send_buffer,
            control_send_buffer,
            latency: Duration::from_millis(150),
            estimated: true,
            last_success: Instant::now(),
            send_warning: false,
        })
    }
    pub fn configure_latency(&mut self, latency: Duration, estimated: bool) {
        self.latency = latency;
        self.estimated = estimated;
        self.packetizer.configure_latency(latency);
    }
    pub fn control_port(&self) -> Result<u16> {
        Ok(self.control.local_addr()?.port())
    }
    pub fn connect_audio(&self, address: SocketAddr) -> Result<()> {
        Ok(self.audio.connect(address)?)
    }
    pub fn set_control_port(&mut self, port: u16) {
        self.remote.set_port(port);
    }
    pub fn reset_watchdog(&mut self, now: Instant) {
        self.last_success = now;
    }
    pub fn metrics(&self) -> Value {
        let mut v = serde_json::to_value(&self.metrics).unwrap();
        v["receiver_latency_ms"] = json!(self.latency.as_secs_f64() * 1000.0);
        v["receiver_latency_estimated"] = json!(self.estimated);
        v["retransmit_pending"] = json!(self.pending.len());
        v["media_backlog_packets"] = json!(self.backlog.len());
        v["media_backlog_bytes"] = json!(self.backlog_bytes);
        v["media_backlog_limit_packets"] = json!(self.backlog_limit());
        v["audio_send_buffer_bytes"] = json!(self.audio_send_buffer);
        v["control_send_buffer_bytes"] = json!(self.control_send_buffer);
        v
    }
    pub fn sync(&mut self, clock_ns: u64, id: Option<u64>, latency: u32, first: bool) {
        let packet = rtp::sync_packet(self.packetizer.timestamp, latency, clock_ns, id, first);
        if self.control.send_to(&packet, self.remote).is_err() {
            self.metrics.sync_send_errors += 1;
        }
    }
    pub fn send(
        &mut self,
        pcm: &[u8],
        first: bool,
        now: Instant,
        media_time: Instant,
        health: &Health,
    ) -> Result<()> {
        health.check()?;
        let packet = self
            .packetizer
            .packet_at(pcm, first, now, media_time + self.latency)?;
        self.enqueue_media(packet);
        self.flush_media(now, health)
    }
    /// Retain one encoded packet, discarding only audio that is already expired.
    ///
    /// The backlog is bounded by the receiver's jitter budget: a packet held
    /// longer than that budget can no longer be played on time, so the oldest
    /// entries are dropped instead of delivering useless audio.
    fn enqueue_media(&mut self, packet: Vec<u8>) {
        let limit = self.backlog_limit();
        while self.backlog.len() >= limit {
            let expired = self.backlog.pop_front().expect("backlog is not empty");
            self.backlog_bytes = self.backlog_bytes.saturating_sub(expired.len());
            self.metrics.media_send_dropped += 1;
        }
        self.backlog_bytes += packet.len();
        self.backlog.push_back(packet);
        self.metrics.media_backlog_peak_bytes = self
            .metrics
            .media_backlog_peak_bytes
            .max(self.backlog_bytes as u64);
    }
    /// Packet slots that still fit inside the receiver's jitter budget.
    fn backlog_limit(&self) -> usize {
        let slots = self.packetizer.latency_slots();
        // Hold back a quarter of the budget so a retained packet still arrives
        // before the timestamp the receiver will play it at.
        (slots * 3 / 4).clamp(MIN_BACKLOG_PACKETS, MAX_BACKLOG_PACKETS) as usize
    }
    /// Drop the whole backlog after the scheduler skipped expired slots.
    ///
    /// Audio queued before a stall is older than the recovered timeline, so
    /// sending it would only add latency the receiver cannot use.
    pub fn abandon_backlog(&mut self) {
        self.metrics.media_send_dropped += self.backlog.len() as u64;
        self.backlog.clear();
        self.backlog_bytes = 0;
    }
    /// Drain what the local send path accepts, retaining the rest.
    fn flush_media(&mut self, now: Instant, health: &Health) -> Result<()> {
        let mut sent = 0usize;
        while let Some(packet) = self.backlog.pop_front() {
            let result = self.audio.send(&packet).map(|_| ());
            let outcome = self.observe_send(result, now, health)?;
            match outcome {
                SendDisposition::Sent => {
                    sent += 1;
                    self.backlog_bytes = self.backlog_bytes.saturating_sub(packet.len());
                }
                // Retain the packet the kernel refused and stop for this tick.
                SendDisposition::Retained => {
                    self.backlog.push_front(packet);
                    break;
                }
                // A hard failure is not retried; the packet is consumed.
                SendDisposition::Discarded => {
                    self.backlog_bytes = self.backlog_bytes.saturating_sub(packet.len());
                }
            }
            if sent >= MAX_FLUSH_PACKETS {
                break;
            }
        }
        Ok(())
    }
    /// Account for one media datagram attempt.
    ///
    /// A full local send path is transient congestion, so it is retained rather
    /// than counted as loss. Only a genuinely failing socket is an error, and a
    /// socket that never accepts a datagram still trips the twelve-second
    /// watchdog. One failed datagram alone is not evidence of a dead session.
    pub fn observe_send(
        &mut self,
        result: std::io::Result<()>,
        now: Instant,
        health: &Health,
    ) -> Result<SendDisposition> {
        let Err(error) = result else {
            self.metrics.packets_sent += 1;
            self.last_success = now;
            if self.send_warning {
                self.send_warning = false;
                health.notice(
                    &self.remote.ip().to_string(),
                    "media",
                    "media_send_recovered",
                    "Audio sending recovered",
                    true,
                );
            }
            return Ok(SendDisposition::Sent);
        };
        if !self.send_warning {
            self.send_warning = true;
            health.notice(
                &self.remote.ip().to_string(),
                "media",
                "media_send_delayed",
                &format!("Audio send failed temporarily: {error}"),
                false,
            );
        }
        // A send path that never drains is a dead session whether the socket
        // reports backpressure or a hard failure, so the watchdog covers both.
        self.check_send_watchdog(now, &error)?;
        if is_send_backpressure(&error) {
            self.metrics.media_send_backlogged += 1;
            return Ok(SendDisposition::Retained);
        }
        self.metrics.media_send_errors += 1;
        Ok(SendDisposition::Discarded)
    }
    /// Fail the session when no datagram has been accepted for twelve seconds.
    fn check_send_watchdog(&self, now: Instant, error: &std::io::Error) -> Result<()> {
        if now.saturating_duration_since(self.last_success) < FAILURE_TIMEOUT {
            return Ok(());
        }
        Err(Fault::new(
            self.remote.ip(),
            "media",
            "media_send_timeout",
            true,
            error,
        )
        .into())
    }
    pub fn service_retransmits(&mut self) {
        let until = Instant::now() + RETRANSMIT_BUDGET;
        let mut buffer = [0u8; 128];
        for _ in 0..MAX_REQUESTS {
            if Instant::now() >= until {
                break;
            }
            let (n, addr) = match self.control.recv_from(&mut buffer) {
                Ok(value) => value,
                Err(e)
                    if matches!(
                        e.kind(),
                        std::io::ErrorKind::WouldBlock | std::io::ErrorKind::Interrupted
                    ) =>
                {
                    break;
                }
                Err(_) => {
                    self.metrics.control_receive_errors += 1;
                    break;
                }
            };
            if addr.ip() != self.remote.ip() {
                continue;
            }
            let Some((start, count)) = Packetizer::retransmit_request(&buffer[..n]) else {
                self.metrics.invalid_requests += 1;
                continue;
            };
            self.metrics.retransmit_requests += 1;
            self.metrics.retransmit_requested_packets += u64::from(count);
            let accepted = usize::from(count).min(MAX_PENDING - self.pending.len());
            self.metrics.retransmit_queue_drops +=
                usize::from(count).saturating_sub(accepted) as u64;
            for i in 0..accepted {
                self.pending.push_back((start.wrapping_add(i as u16), addr));
            }
        }
        for _ in 0..MAX_RESENDS {
            let now = Instant::now();
            if now >= until {
                break;
            }
            let Some((seq, addr)) = self.pending.pop_front() else {
                break;
            };
            match self.packetizer.retransmit_packet(seq, now) {
                Retransmit::Packet(packet) => {
                    if self.control.send_to(&packet, addr).is_ok() {
                        self.metrics.retransmits_sent += 1;
                    } else {
                        self.metrics.retransmit_send_errors += 1;
                    }
                }
                Retransmit::Missing => self.metrics.retransmit_missing += 1,
                Retransmit::Expired => self.metrics.retransmit_expired += 1,
            }
        }
    }
}

/// The timeline advances for intentionally dropped slots and sent packets alike.
pub struct MediaSchedule {
    pub start: Instant,
    pub frames: u64,
    pub recoveries: u64,
    pub skipped_packets: u64,
    pub max_lateness_us: u128,
    rate: u32,
}
impl MediaSchedule {
    pub fn new(start: Instant, rate: u32) -> Self {
        Self {
            start,
            frames: 0,
            recoveries: 0,
            skipped_packets: 0,
            max_lateness_us: 0,
            rate,
        }
    }
    pub fn deadline(&self) -> Instant {
        self.start + Duration::from_nanos(self.frames * 1_000_000_000 / self.rate as u64)
    }
    pub fn recover(&mut self, now: Instant, live: bool) -> Result<u64> {
        let late = now.saturating_duration_since(self.deadline());
        self.max_lateness_us = self.max_lateness_us.max(late.as_micros());
        if late < LATE_LIMIT {
            return Ok(0);
        }
        if !live {
            return Err(Fault::new(
                "",
                "scheduler",
                "qualification_late",
                false,
                "sender fell >60ms behind; qualification aborted",
            )
            .into());
        }
        let due = (now.saturating_duration_since(self.start).as_nanos() * u128::from(self.rate)
            / 1_000_000_000) as u64;
        let skipped = due.saturating_sub(self.frames) / FRAMES as u64;
        self.frames += skipped * FRAMES as u64;
        self.skipped_packets += skipped;
        self.recoveries += 1;
        Ok(skipped)
    }
    pub fn sent(&mut self) {
        self.frames += FRAMES as u64;
    }
}

#[cfg(test)]
mod schedule_tests {
    use super::*;
    #[test]
    fn schedule_advances_by_selected_rate() {
        let start = Instant::now();
        let mut s = MediaSchedule::new(start, 48_000);
        for _ in 0..10 {
            s.sent();
        }
        assert_eq!(
            s.deadline().saturating_duration_since(start),
            Duration::from_nanos(10u64 * FRAMES as u64 * 1_000_000_000 / 48_000)
        );
        let mut slow = MediaSchedule::new(start, 44_100);
        for _ in 0..10 {
            slow.sent();
        }
        assert!(s.deadline() < slow.deadline());
    }
    #[test]
    fn forty_eight_k_schedule_skips_expected_slots_after_stall() {
        let start = Instant::now();
        let mut s = MediaSchedule::new(start, 48_000);
        let now = start + Duration::from_millis(500);
        let skipped = s.recover(now, true).unwrap();
        let due = (500_000_000u64 * 48_000) / 1_000_000_000;
        assert_eq!(skipped, due / FRAMES as u64);
        assert_eq!(s.frames, skipped * FRAMES as u64);
        assert_eq!(s.skipped_packets, skipped);
    }
}

#[cfg(test)]
mod backlog_tests {
    use super::*;

    fn transport(latency: Duration) -> MediaTransport {
        let audio = UdpSocket::bind("127.0.0.1:0").unwrap();
        let control = UdpSocket::bind("127.0.0.1:0").unwrap();
        let peer = UdpSocket::bind("127.0.0.1:0").unwrap();
        audio.connect(peer.local_addr().unwrap()).unwrap();
        let mut t = MediaTransport::new(
            Packetizer::new([5; 32], 1, 0, 1),
            audio,
            control,
            peer.local_addr().unwrap(),
        )
        .unwrap();
        t.packetizer.configure_latency(latency);
        t
    }

    #[test]
    fn latency_slots_track_the_receiver_budget() {
        // 352 frames at 44.1 kHz is one 8 ms slot, so a 200 ms budget is 25 slots.
        let t = transport(Duration::from_millis(200));
        assert_eq!(t.packetizer.latency_slots(), 25);
        assert_eq!(
            transport(Duration::from_millis(120)).packetizer.latency_slots(),
            15
        );
    }

    #[test]
    fn backlog_is_bounded_by_the_jitter_budget() {
        let mut t = transport(Duration::from_millis(200));
        let limit = t.backlog_limit();
        assert!(limit > 0 && limit < 25, "limit {limit} must leave headroom");
        for _ in 0..limit + 10 {
            t.enqueue_media(vec![0; 1428]);
        }
        // Only the newest audio is retained; older slots are already unplayable.
        assert_eq!(t.backlog.len(), limit);
        assert_eq!(t.metrics.media_send_dropped, 10);
    }

    #[test]
    fn flush_retains_on_backpressure_and_drains_afterwards() {
        let mut t = transport(Duration::from_millis(200));
        let h = Health::default();
        let now = Instant::now();
        t.enqueue_media(vec![0; 1428]);
        t.enqueue_media(vec![1; 1428]);
        // A blocked send retains the packet and does not count it as loss.
        let outcome = t
            .observe_send(
                Err(std::io::ErrorKind::WouldBlock.into()),
                now,
                &h,
            )
            .unwrap();
        assert!(matches!(outcome, SendDisposition::Retained));
        assert_eq!(t.metrics.media_send_errors, 0);
        assert_eq!(t.metrics.media_send_backlogged, 1);
        assert_eq!(h.notices().len(), 1);
        // Recovery closes the warning edge and resumes counting sent packets.
        let outcome = t.observe_send(Ok(()), now, &h).unwrap();
        assert!(matches!(outcome, SendDisposition::Sent));
        assert_eq!(t.metrics.packets_sent, 1);
        assert_eq!(h.notices().len(), 1);
    }

    #[test]
    fn hard_failure_is_still_counted_and_watchdog_still_fires() {
        let mut t = transport(Duration::from_millis(200));
        let h = Health::default();
        let start = Instant::now();
        t.reset_watchdog(start);
        let outcome = t
            .observe_send(
                Err(std::io::ErrorKind::NetworkUnreachable.into()),
                start + Duration::from_secs(1),
                &h,
            )
            .unwrap();
        assert!(matches!(outcome, SendDisposition::Discarded));
        assert_eq!(t.metrics.media_send_errors, 1);
        // Backpressure alone also trips the watchdog when nothing drains.
        let error = t
            .observe_send(
                Err(std::io::ErrorKind::WouldBlock.into()),
                start + FAILURE_TIMEOUT,
                &h,
            )
            .unwrap_err();
        assert_eq!(
            error.downcast_ref::<Fault>().unwrap().code,
            "media_send_timeout"
        );
    }

    #[test]
    fn abandoning_the_backlog_discards_only_queued_audio() {
        let mut t = transport(Duration::from_millis(200));
        for _ in 0..4 {
            t.enqueue_media(vec![0; 1428]);
        }
        assert_eq!(t.backlog.len(), 4);
        t.abandon_backlog();
        assert!(t.backlog.is_empty());
        assert_eq!(t.backlog_bytes, 0);
        assert_eq!(t.metrics.media_send_dropped, 4);
        // The retained-packet accounting is untouched by dropping queued audio.
        assert_eq!(t.metrics.media_send_errors, 0);
    }

    #[test]
    fn metrics_expose_backlog_and_granted_buffers() {
        let t = transport(Duration::from_millis(500));
        let v = t.metrics();
        assert!(v["media_backlog_limit_packets"].as_u64().unwrap() > 0);
        assert_eq!(v["media_backlog_packets"].as_u64().unwrap(), 0);
        assert!(v.get("audio_send_buffer_bytes").is_some());
    }
}
