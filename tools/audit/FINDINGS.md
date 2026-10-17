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

## Open

| severity | where | finding |
|---|---|---|
| P2 | `internal/localsound/localsound_windows.go:390` | `unsafe.Pointer(value.pwszVal)` trips `go vet` on the Windows build. Pre-existing. The value is a `uintptr` holding a pointer, which vet cannot see is live |
| P2 | `internal/engine/client.go` `read` | a malformed line is skipped, so an engine bug that emits broken JSONL looks like an engine that stopped sending |
| P2 | `main.go:719` `startDiscovery` | check-then-act on `discoveryCancel` with no lock. Only reachable from the UI thread today; any caller from a goroutine would start two browses |
| P3 | `main.go` `stopStream` | `localsound.Restore()` runs on the UI thread. It is a COM round trip, so a slow audio service stalls the click that stopped playback |
