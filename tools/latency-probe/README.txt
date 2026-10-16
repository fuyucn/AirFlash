AirFlash latency probe
======================

What this measures
------------------
AirFlash's engine sends one audio packet every 7.98 ms. If a send misses that
deadline the HomePod hears a gap, which is the stutter you reported. These
probes run that exact cadence and record how late each deadline is actually
met, in three languages, on your own machine.

Files
-----
  probe-go.exe             Go    - the language mygo is written in
  probe-rust.exe           Rust  - the language AirFlash's engine uses today
  probe-csharp.exe         C#    - the language AirFlash's UI uses today
  AirFlash Probe.exe       a real mygo (Go) desktop app; see below
  run-probes.ps1           runs all three probes and writes probe-log.txt

How to run the command line probes
----------------------------------
Open PowerShell in this folder and run:

  powershell -ExecutionPolicy Bypass -File run-probes.ps1

It takes about 3-4 minutes and writes probe-log.txt next to the script.
Send me that file.

It will peg your CPU during the burn cases. That is the point: the burn cases
are what show whether a language can hold the deadline when the machine is
busy. Close other heavy apps first if you want the idle numbers to be clean.

Reading the output
------------------
  p50 / p95 / p99 / max   how late each send was, in milliseconds
  late>1slot              share of sends that missed by more than a whole slot
  stalls>20ms             count of severe stalls

A healthy result is near zero with 0% late. Anything above 0% late is audible.

The mygo desktop app
--------------------
AirFlash Probe.exe is a real mygo application. It opens a window, draws a live
chart of the send cadence, and runs the same measurement inside its own Go
process while the UI is alive.

That is the test that matters for a Go rewrite: AirFlash's engine is currently
a separate process, so this app is deliberately the harder case - UI and audio
loop sharing one runtime.

Try it with "Competing goroutines" set to your core count, then press Run.

Windows 11 already has the WebView2 runtime this app needs. On Windows 10 you
may need to install it.

Notes
-----
- These are unsigned binaries. Windows SmartScreen may warn; use More info,
  then Run anyway.
- Nothing here touches the network beyond a loopback socket, and nothing is
  sent anywhere. The probes only measure local timing.
- If PowerShell scripts are blocked, run each exe by hand instead, for example:
      .\probe-go.exe -slots 1500
      .\probe-go.exe -slots 1000 -burn 8 -udp
