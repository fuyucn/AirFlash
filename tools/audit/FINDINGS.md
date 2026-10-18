# Findings

Each row is a finding, with where it was seen. Severity is what it costs a user:
P0 breaks a feature or crashes, P1 gives a wrong result or a silent failure, P2 is
a latent risk the current code happens not to hit.

## Closed

| severity | where | finding | how it was closed |
|---|---|---|---|
| P0 | `internal/localsound/localsound_windows.go` `withCom` | `CoInitializeEx` ran once for the process, but it is per thread. The mute is called from the engine's event goroutine and from the UI thread, so one of the two ran its COM calls on a thread that never joined an apartment. Windows answers `CO_E_NOTINITIALIZED`, the silence does not happen, and nothing reports it. | apartment filed under the thread id; test writes the old and new ways side by side |
| P1 | `ui/shell.go` `statsPage` | Stats offered Play and Stop acting on whatever was last played to, which reads as "play to I don't know what" | removed; Stats reads numbers, playback lives on the panel and Receivers |
| P1 | `ui/shell.go` `receiversPage` | every row showed Play, including the one already playing, which would start a second session the engine cannot run | the playing row shows Stop |
| P1 | `ui/shell.go` `SetPage` | `pageStats` was enum 0, so a `Shell`'s zero value opened the first window on a page nobody asked for | `pageReceivers` first, so the zero value is the receivers |
| P1 | `ui/shell.go` `sidebar` | a 196 DIP sidebar beside a small viewport left the page a sliver and was the first thing to disappear | a horizontal top bar |
| P1 | `ui/panel.go` `receivers` | the list had no vertical overflow, so more speakers than fit clipped the last one, and the reading and footer with it | the panel is capped and scrolls |
| P1 | `internal/discovery/discovery.go` `trimInstance` | the instance name miekg/dns returns keeps DNS-SD presentation escaping, so a HomePod called "Living Room" showed as `Living\ Room` | `unescapeDNSSD` |
| P1 | `internal/discovery/browse.go` `serviceTypeOf` | returned AirPlay as the default, so a Spotify Connect speaker or a HomeKit hub that answered on the same channel was folded in as a receiver with a Play button | returns "" for a service that is neither, and the record is dropped |
| P1 | `internal/discovery/discovery.go` `Fold` | two records of one device that disagreed on the device id folded apart, listing the same speaker twice | the address decides when the ids do not |
| P1 | `main.go` `trayIcon` | returned nil: `embeddedIcon` was a variable nothing ever assigned, and the asset build the comment named did not exist. The app ran with no icon in the notification area | the icon is drawn in `internal/trayicon` |
| P1 | `internal/engine/spawn_attrs_windows.go` | the engine is a console-subsystem binary and the shell a GUI app, so pressing Play opened a blank console window that stayed for as long as the engine ran | `CREATE_NO_WINDOW` |
| P1 | `ui/shell.go` `receiversPage` | Pair was offered on every row while anything was streaming. The engine's `pair` does `running.take()` and drops it, so pressing Pair stopped the audio in order to ask for a PIN | moved into the receiver's own controls |
| P0 | `main.go` `openWindow` | the window's own close button was never wired to the lifecycle, so `a.win` kept pointing at a window the platform had destroyed. The next `openWindow` took its `Show` branch on a dead window, which does nothing: after closing the window the tray could not bring it back | `w.OnClose` drives `life.Close`, and `openWindow` also refuses a window `IsDestroyed` reports |
| P1 | `internal/engine/state.go` `phase` | the engine names its phases after the AirPlay protocol (`info`, `authenticate`, `setup_session`), none of which is a phase the interface draws. Taken as they arrived, a connection in progress showed as Idle | `phaseOf` maps the protocol steps to connecting; the engine's own name is kept in `Detail` |
| P1 | `ui/shell.go` `settingsPage` | the sample-rate control was declared as `OnSampleRate` and never passed by `main.go`, so it moved the interface and changed nothing | wired to the settings, which the engine is asked for when a session starts |
| P1 | `main.go` `run` | the engine was never closed anywhere in the app, so it outlived the shell: the process stayed in the task list holding the audio device | `OnWillQuit` stops and closes it; the retry loop's backoff also ends at quit rather than sleeping it out |
| P1 | `internal/engine/client.go` `Send` | a command after Close wrote to a nil stdin and panicked, so a caller could not tell a gain change had not landed | `Send` refuses when stdin is nil |
| P2 | `internal/settings/settings.go` `ReducedMotion` | written by normalized and read by nothing, so lightweight mode claimed to stop an animation that does not exist | removed |
| P1 | `internal/settings/settings.go` `Update` | a write that failed was discarded, so a setting the user made could be gone at the next launch with nothing said. The interface still changed, which made it look saved | `WriteErr` records it and the settings page shows it |

## Open

| severity | where | finding |
|---|---|---|
| — | — | none. New findings go here. |

## Closed by the swe-loop run

| severity | where | finding | how it was closed |
|---|---|---|---|
| P2 | `internal/localsound/localsound_windows.go` | `unsafe.Pointer(value.pwszVal)` tripped `go vet` on the Windows build. The union member was a `uintptr` converted back to a pointer, which vet cannot prove safe | typed the member `*uint16`, which is the same width and needs no conversion; the COM vtables moved to `com_windows.go`, which also brought the file under the 400-line gate |
| P2 | `internal/engine/client.go` `read` | a malformed line was skipped without a trace, so an engine bug that emitted broken JSONL looked like an engine that stopped sending | `Client.MalformedLines()` counts them and the first is logged with its reason and a quoted excerpt |
| P2 | `main.go` `startDiscovery` | check-then-act on `discoveryCancel`: two callers could both see nil and both start a browse, the second overwriting the first's cancel | `claimBrowse` uses a compare-and-swap, and the loser cancels the context it made |
| P3 | `main.go` `stopStream` | `localsound.Restore()` ran on the UI thread; a slow audio service stalled the click that asked to stop | `restoreSpeakers` runs it in a goroutine, serialised by a mutex, with the error still logged |
