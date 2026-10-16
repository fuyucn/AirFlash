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

## Surfaces

Two ways to draw the window, chosen in Settings:

| | Native UI | Webview |
| --- | --- | --- |
| Resident memory | ~53 MB | ~314 MB (main process ~31 MB plus a browser process tree) |
| Ships as | one 9.4 MB file | one 9.4 MB file |
| Depends on | nothing extra | WebView2 |

Native UI is the default recommendation. WebView2 ships with Windows 11 and is
present on the vast majority of Windows 10 machines, but its cost is a process
tree rather than a library, and on Linux it means WebKitGTK.

## Lightweight mode

Settings has one switch that changes what the app costs to run:

- no window material and no glass, so the bars are a plain tone step
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

    main.go                 the app: tray, lifecycle, engine wiring
    ui/                     the window, drawn by mygo
      theme.go              palette and the target-versus-measured band
      shell.go              the views
    internal/engine/        the JSONL v1 client and the app's state
    internal/settings/      preferences, including the mode
    internal/shell/         window lifecycle: open, close, stay resident

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
