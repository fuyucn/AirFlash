package main

// Reconnecting a session that dropped.
//
// The desktop host keeps a session alive across a reconnect and counts the
// attempts; the shell does the same, with the same numbers, so the two hosts
// behave alike. Where the engine reports a fault as an error event and a
// user-pressed stop as a stopped event, neither of those is a reconnect: the
// first is retried and the second is the end of the session.

import (
	"log"
	"strings"
	"time"

	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/reconnect"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/settings"
)

// session is what one playback attempt is made of. It is kept whole rather than
// as loose fields because a retry has to start the same thing that dropped: the
// same hosts, at the target the shell worked out for them.
type session struct {
	peers []engine.Peer
	// receiverID is the receiver these peers belong to, which is what its own
	// settings are keyed by. A stereo pair is one receiver with two halves, so a
	// session has one id and two peers.
	receiverID string
	// startedAt is when this attempt began streaming, which is what separates a
	// good connection that dropped from one that never worked.
	startedAt time.Time
}

// runSession streams to one receiver, and retries it when it drops.
//
// It returns when the session ends for good: the user stopped it, the engine
// died while reconnecting was off, or the attempts ran out.
func (a *App) runSession(s session) {
	cfg := a.settings.Current()
	p := reconnect.Policy{Enabled: cfg.Reconnect, MaxAttempts: cfg.ReconnectAttempts}

	for attempt := 0; ; attempt++ {
		// The user may have pressed stop while a previous attempt was waiting
		// out its backoff, in which case there is no session to start.
		if a.stopping.Load() {
			a.recordSession()
			a.repaint()
			return
		}

		a.startAttempt(s)
		s.startedAt = time.Now()
		// A new attempt is under way, so the retry count is no longer what the
		// surfaces should be showing: from here it is a connection in progress.
		a.state.SetReconnecting(0, 0)

		client := a.client.Load()
		if client == nil {
			return
		}
		// The engine ending is the one thing that does not arrive as an event,
		// because then there is nobody left to send one.
		<-client.Wait()
		a.client.Store(nil)

		// A stopped phase means the session was ended rather than lost: the
		// engine says so, and only that, when it stops cleanly.
		snap := a.state.Snapshot()
		ended := a.stopping.Load() || snap.Phase == engine.PhaseStopped
		a.playing.Store(nil)

		// How long this attempt streamed, which is what separates a connection
		// that worked and dropped from one that never worked at all.
		elapsed := time.Since(s.startedAt)
		reason := reconnect.ReasonRetryable
		switch {
		case ended:
			reason = reconnect.ReasonStopped
		case isAuthFailure(snap.Detail):
			reason = reconnect.ReasonNotRetryable
		}

		d := reconnect.Decide(p, attempt, elapsed, reason)
		if !d.Retry {
			if !ended {
				// Nothing is going to send an error, so the phase is set here: a
				// dead engine is not playing, whatever the panel last showed.
				a.state.Apply(engine.Event{Event: "error", Raw: []byte(
					`{"event":"error","message":"the engine stopped"}`)})
			}
			a.recordSession()
			a.repaint()
			return
		}

		a.state.Apply(engine.Event{Event: "phase", Raw: []byte(
			`{"event":"phase","phase":"connecting"}`)})
		// The retry is the shell's own, so the numbers are set here: the engine
		// reports its own phases and has no idea a second attempt is coming.
		a.state.SetReconnecting(d.Attempt, p.MaxAttempts)
		a.repaint()
		time.Sleep(d.After)
	}
}

// startAttempt runs the engine once and asks it to stream to the receiver.
//
// The engine is started on demand and outlives a session only while it streams:
// when it ends, whatever the reason, the process is gone and the next attempt
// starts a new one. That is what makes an ended session and a dead engine
// indistinguishable from in here, which is why the caller checks the phase.
func (a *App) startAttempt(s session) {
	if a.enginePath == "" {
		log.Printf("no engine path configured")
		return
	}
	client, err := engine.Start(a.enginePath, a.onEngineEvent)
	if err != nil {
		log.Printf("engine start failed: %v", err)
		return
	}
	a.client.Store(client)
	_, _ = client.Hello()

	cfg := a.settings.Current()
	opts := engine.DefaultStreamOptions()
	// The receiver's own settings where it has them, keyed by its id: a stereo
	// pair is one receiver with two halves, so its settings are the pair's.
	switch {
	case s.receiverID != "":
		opts.LatencyMs = cfg.LatencyFor(s.receiverID)
		opts.Gain = cfg.GainFor(s.receiverID)
	default:
		opts.LatencyMs = cfg.EffectiveLatencyMs(a.adaptedTarget())
		opts.Gain = cfg.Gain
	}
	opts.SampleRate = cfg.SampleRate
	// The endpoint the engine captures from, or the system's default render
	// device when none is named. A machine with more than one sound card is the
	// case: the PC's audio may come out of a USB interface the engine would not
	// otherwise pick up.
	opts.CaptureEndpoint = cfg.CaptureEndpoint
	// The equalizer goes in with the stream, so what is heard is what the
	// settings say from the first packet rather than from the first edit.
	opts.Equalizer = cfg.Equalizer
	a.state.ResetSession()

	if _, err := client.StartStream(s.peers, opts); err != nil {
		log.Printf("start failed: %v", err)
	}
	a.settings.Update(func(c *settings.Settings) { c.LastPeer = s.peers[0].Host })
}

// isAuthFailure reports whether an engine message is an authentication failure.
//
// The desktop host treats these as final, because a retry asks the receiver for
// the credentials that just failed: what is needed is a PIN, which is something
// to ask the user for rather than to keep trying.
func isAuthFailure(message string) bool {
	text := strings.ToLower(message)
	for _, word := range []string{"auth", "pair", "pin", "credential", "unauthorized", "forbidden"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

// repaint redraws whatever surface is open.
func (a *App) repaint() {
	if w := a.win.Load(); w != nil {
		w.Invalidate()
	}
	if f := a.flyout.Load(); f != nil {
		f.Invalidate()
	}
}
