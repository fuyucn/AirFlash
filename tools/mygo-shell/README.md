# AirFlash shell (mygo)

A desktop shell for AirFlash, written in Go with [mygo](https://github.com/egoist/mygo).
It replaces the WPF/C# host; the Rust engine is unchanged.

## Why a shell at all

AirFlash's engine is a separate process that speaks JSONL v1 on stdin/stdout.
The shell starts it, writes commands to it and reads events back. Keeping the
engine in its own process is not an implementation detail - it is what lets the
Go side hold the engine's 7.98 ms send deadline. Measured on a 4-core Windows
runner with the CPU saturated, the same send loop missed its deadline on 80% of
slots when it shared a Go runtime with the UI, and 0% when it ran alone.

It is also what lets the window be closed without interrupting playback: the
engine outlives the window.

## Platforms

Windows and Linux. macOS is deliberately out of scope: macOS plays to a HomePod
through AirPlay itself, so there is nothing here for it to add.

The engine is Windows-only today. Its audio capture is WASAPI (`wasapi.rs`,
gated by `cfg(windows)`), and `session.rs` calls into it directly. On Linux the
shell builds and runs, but the engine needs a capture backend before playback
works - PipeWire or PulseAudio monitor, behind the same trait the WASAPI path
would implement. The AirPlay side of the engine (RTSP, HAP, PTP, RTP, ALAC) is
platform-neutral already.

## One file

`AirFlash.exe` is a single file with the engine inside it. On first start the
shell unpacks the engine to `%LOCALAPPDATA%\AirFlash\engine`, named after a
digest of its own contents, and reuses it after that: an upgraded shell never
reuses a stale engine.

The engine is still a separate process. That is deliberate, not an oversight:
a Go runtime cannot hold the engine's 7.98 ms send deadline while the UI shares
it (measured: 73% of slots missed with the UI in-process, 0% when the audio path
has its own). Embedding keeps the split without asking the user to manage two
files.

A build with no engine in `internal/enginebin/payload` still compiles and starts;
it reports `engine_bundled: false` rather than failing.

## The panel is the app

A tray click opens a narrow panel beside the icon: the receivers, one control
each, and the current reading. That is what the app is almost every time it is
opened, and it matches the upstream WPF app's own `ControlPanel.xaml` - 440 DIP
wide, sized to its content, no taskbar button.

The window is for the two things the panel is too small for: the full reading on
Stats, and the settings. Nothing opens a window at launch.

The panel is a flyout, which means it is destroyed when it closes rather than
hidden, so it holds nothing while the user is not looking at it.

### The look

Grayscale, square, and divided by dashed hairlines. The app exists to measure
something, so it is drawn like the panel of a measuring device. Colour is a
signal - a latency over target, a second that lost audio - and never
decoration. `Theme.Radius` is 0: nothing in the interface is rounded.

## Latency: Auto

The engine measures the round trip to the receiver and never acts on it, so a
slower network stutters more: a resend only helps if it arrives before the
packet's play deadline, which is `target - elapsed - round trip`. When the ping
rises that window closes and the receiver plays over the gap.

**Auto** is the default. It picks the target from what previous sessions turned
out to be, and it is bounded by the setting beside it, which is a floor: Auto
raises the target when a session showed a gap and eases it back after several
clean ones. It only ever changes the target at a session boundary, never the
play time of audio already in flight, which is what makes it safe.

Choosing a fixed number turns Auto off and pins that target exactly.

## The Stats page

The first page is Stats. It shows:

- the target, the receiver's playout buffer, and the network round trip, set
  large, with the total that the observed parts add up to
- a chart of the latency over time, with the target as a visible reference line
  and readings that went over it marked
- a timeline of the session second by second, marking the seconds that lost
  audio. The engine reports once a second, so one mark is one second, and a gap
  lines up with the latency spike that caused it
- the counters that explain a stutter: how often the sender fell behind, how
  many audio slots it discarded, and how many resends arrived too late

`docs/STUTTER-ANALYSIS.md` reads the engine's send path and explains why a
rising ping causes gaps, with the constants and the code that does it.

The counters come from the engine as running totals for the session, so each
report replaces the last rather than adding to it. They add up only across the
two halves of a stereo pair, where each half counts its own.

## What the desktop host has that this does not

Two things, both of which need the engine to change rather than the shell:

- **Real-time buffer adaptation.** The desktop host can move the target mid-session
  when the network degrades. The engine takes a latency once, at `start`, and has
  no command to change it during a stream.
- **Busy detection.** A receiver already streaming to another device can be told
  apart by a bit in the `statusFlags` of its `/info` response
  (`0x20000`). The engine reads `/info` for its model and version and reports
  nothing else, so the shell has nothing to read.

Everything else the desktop host does, this does: per-receiver settings, the
equalizer, reconnect, standby, start at login, capture endpoint, discovery
adapter, quiet-mode autostart, and PIN pairing.

## Lightweight mode

Settings has one switch that changes what the app costs to run:

- no entrance animation
- the trend chart is dropped
- metrics are sampled once a second instead of four times

The audio path is untouched. The engine keeps its own process either way, so
lightweight mode changes what the shell spends, never what the engine sends.

## Closing the window

Closing the window destroys it, which is what releases everything it holds -
including a webview's browser processes. The app stays in the tray and playback
continues, because the engine is not the window.

## Layout

    main.go                 the app: tray, panel, window, engine wiring
    ui/                     the window, drawn by mygo
      theme.go              palette, type steps, and the dashed rule
      panel.go              the tray panel: receivers, one control each
      shell.go              the window: Stats, Receivers, Settings
    internal/engine/        the JSONL v1 client and the app's state
    internal/settings/      preferences, including the mode
    internal/shell/         window lifecycle: open, close, stay resident
    cmd/render/             draws the surfaces to PNG files, for looking at
    docs/DESIGN.md          what the design is and why, honestly

## Looking at it without a HomePod

    go run ./cmd/render /tmp/shots

Writes the panel and all three window pages to PNG files, with a synthetic
session. Looking at the running surface is the only way to judge a layout, and
this is the cheapest way to get a picture - it found four bugs that the code did
not show.

## Running

    go run . -engine /path/to/airflash-engine

Without `-engine`, the shell looks for `airflash-engine` beside its own binary.

`-probe out.json` writes a start-up report and exits; CI uses it to prove the
shell launches and can describe itself without a display.

## Tests

    go test ./...

The engine client is tested against a stand-in engine process, because the real
one is Windows-only. The lifecycle and settings tests are written so that
breaking the behaviour they describe makes them fail.
