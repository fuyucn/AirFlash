// AirFlash latency probe - Rust implementation.
//
// Same cadence and same measurement as the Go probe, so the two logs are
// directly comparable. Rust uses OS threads, so `burn` here means competing
// OS threads, not goroutines.
use std::env;
use std::io::Write;
use std::net::UdpSocket;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::{Duration, Instant};

const SLOT: Duration = Duration::from_nanos(7_980_000);
const SPIN: Duration = Duration::from_micros(300);
const PACKET: usize = 352 * 2 * 2;

fn main() {
    let args: Vec<String> = env::args().collect();
    let (mut slots, mut burn) = (1500usize, 0usize);
    let (mut mode, mut udp) = ("sleep".to_string(), false);
    let mut json = false;
    let mut i = 1;
    while i < args.len() {
        match args[i].as_str() {
            "-slots" => { slots = args[i + 1].parse().unwrap(); i += 2; }
            "-burn" => { burn = args[i + 1].parse().unwrap(); i += 2; }
            "-mode" => { mode = args[i + 1].clone(); i += 2; }
            "-udp" => { udp = true; i += 1; }
            "-json" => { json = true; i += 1; }
            _ => i += 1,
        }
    }

    let stop = Arc::new(AtomicBool::new(false));
    let mut handles = Vec::new();
    for _ in 0..burn {
        let stop = stop.clone();
        handles.push(thread::spawn(move || {
            let mut x: u64 = 0;
            while !stop.load(Ordering::Relaxed) {
                for k in 0..20000u64 { x = x.wrapping_add(k.wrapping_mul(k)); }
            }
            if x == 1 { eprintln!("{x}"); }
        }));
    }
    thread::sleep(Duration::from_millis(200));

    // A real local listener, so the send path matches Go's probe exactly and
    // no ICMP port-unreachable can turn into a spurious send error.
    let mut socket: Option<UdpSocket> = None;
    if udp {
        let receiver = UdpSocket::bind("127.0.0.1:0").expect("udp bind");
        let addr = receiver.local_addr().unwrap();
        thread::spawn(move || {
            let mut buf = [0u8; 4096];
            while receiver.recv_from(&mut buf).is_ok() {}
        });
        let s = UdpSocket::bind("127.0.0.1:0").expect("udp bind sender");
        s.connect(addr).expect("udp connect");
        socket = Some(s);
    }

    let payload = vec![0u8; PACKET];
    let mut late: Vec<f64> = Vec::with_capacity(slots);
    let mut stalls = 0usize;

    let mut deadline = Instant::now();
    for _ in 0..slots {
        deadline += SLOT;
        if mode == "spin" {
            while Instant::now() < deadline {}
        } else {
            loop {
                let now = Instant::now();
                if now >= deadline { break; }
                let d = deadline - now;
                if d > SPIN {
                    thread::sleep((d - SPIN).min(Duration::from_micros(500)));
                }
            }
        }
        if let Some(s) = &socket {
            s.send(&payload).expect("udp send");
        }
        let l = Instant::now().duration_since(deadline).as_secs_f64() * 1000.0;
        if l > 20.0 { stalls += 1; }
        late.push(l);
    }

    stop.store(true, Ordering::Relaxed);
    for h in handles { let _ = h.join(); }

    late.sort_by(|a, b| a.partial_cmp(b).unwrap());
    let at = |q: f64| late[(q * (late.len() - 1) as f64) as usize];
    let over = late.iter().filter(|l| **l > 7.98).count();
    let pct = 100.0 * over as f64 / late.len() as f64;

    if json {
        let out = serde_json::json!({
            "language": "rust",
            "os": env::consts::OS,
            "cpus": thread::available_parallelism().map(|v| v.get()).unwrap_or(0),
            "mode": mode, "burn": burn, "udp": udp, "slots": slots,
            "p50_ms": at(0.50), "p95_ms": at(0.95), "p99_ms": at(0.99),
            "max_ms": late[late.len() - 1],
            "over_one_slot_pct": pct,
            "stalls_over_20ms": stalls,
        });
        println!("{}", serde_json::to_string_pretty(&out).unwrap());
    } else {
        println!(
            "rust mode={:<5} burn={:<3} udp={:<5} | p50={:>7.3} p95={:>7.3} p99={:>7.3} max={:>8.3} ms | late>1slot {:>5.1}% | stalls>20ms {}",
            mode, burn, udp, at(0.50), at(0.95), at(0.99), late[late.len() - 1], pct, stalls
        );
    }
    let _ = std::io::stdout().flush();
}
