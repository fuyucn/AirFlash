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
// gen is the session's generation: the loop gives up as soon as a newer session
// has been started or the user has stopped, whichever came first. Comparing a
// number rather than a boolean is what tells "the user stopped me" apart from
// "the user stopped me and then started something else".
//
// It returns when the session ends for good: the user stopped it, the engine
// died while reconnecting was off, or the attempts ran out.
func (a *App) runSession(s session, gen uint64) {
	cfg := a.settings.Current()
	p := reconnect.Policy{Enabled: cfg.Reconnect, MaxAttempts: cfg.ReconnectAttempts}

	for attempt := 0; ; attempt++ {
		// The user may have pressed stop while a previous attempt was waiting
		// out its backoff, or started a different session. Either way this loop
		// is no longer the one that matters.
		if a.stopping.Load() || a.sessionSeq.Load() != gen {
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
			// The attempt never got an engine, so there is nothing to wait on.
			// It is still a failure the policy may want to retry - a missing
			// binary will fail again, but a start that lost a race with a
			// closing process may not - so it goes through the same decision as
			// a session that dropped.
			d := reconnect.Decide(p, attempt, 0, reconnect.ReasonRetryable)
			if !d.Retry {
				a.recordSession()
				a.repaint()
				return
			}
			a.state.SetReconnecting(d.Attempt, p.MaxAttempts)
			a.repaint()
			if !a.waitBackoff(d.After, gen) {
				a.recordSession()
				a.repaint()
				return
			}
			continue
		}
		// The engine ending is the one thing that does not arrive as an event,
		// because then there is nobody left to send one.
		<-client.Wait()
		// Take it out and let it go. The process has already ended - that is
		// what Wait was waiting for - so closing its stdin is only tidiness;
		// what matters is that the slot is freed rather than holding a client
		// nobody will read from again.
		a.client.CompareAndSwap(client, nil)
		_ = client.Close()

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
		// Wait out the backoff, but not past the user asking to stop or the app
		// going away: a plain sleep would hold the session goroutine - and with
		// it the engine it is about to start again - for up to eight seconds
		// after the shell had already left, or after the user asked for silence.
		if !a.waitBackoff(d.After, gen) {
			a.recordSession()
			a.repaint()
			return
		}
	}
}

// waitBackoff sleeps for d, but not past the session ending.
//
// It returns true when the wait finished, and false when the session is no
// longer the one that matters - the user asked to stop, a newer session was
// started, or the app is going away. A select on time.After alone would run the
// full wait first, which is up to eight seconds of a connection being retried
// after the user asked for silence.
func (a *App) waitBackoff(d time.Duration, gen uint64) bool {
	// Poll rather than needing a channel per stop: the backoff is at most eight
	// seconds and the check is two atomics, so a short tick costs nothing and
	// keeps the stop paths exactly as they are.
	timer := time.NewTimer(d)
	defer timer.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-timer.C:
			return true
		case <-a.quitting:
			return false
		case <-tick.C:
			if a.stopping.Load() || a.sessionSeq.Load() != gen {
				return false
			}
		}
	}
}

// startAttempt runs the engine once and asks it to stream to the receiver.
//
// The engine is started on demand and outlives a session only while it streams:
// when it ends, whatever the reason, the process is gone and the next attempt
// starts a new one. That is what makes an ended session and a dead engine
// indistinguishable from in here, which is why the caller checks the phase.
// failAttempt records an attempt that never got as far as an engine.
//
// runSession returns when there is no client to wait on, so nothing else will
// report a phase for this attempt. Without this the surface keeps the
// "connecting" it was put in - a connection that is not being made and never
// will be, because the loop has already given up.
func (a *App) failAttempt(message string) {
	a.state.Apply(engine.Event{Event: "error", Raw: []byte(
		`{"event":"error","message":"` + message + `"}`)})
	a.repaint()
}

func (a *App) startAttempt(s session) {
	if a.enginePath == "" {
		log.Printf("no engine path configured")
		a.failAttempt("the audio engine was not found")
		return
	}
	client, err := engine.Start(a.enginePath, a.onEngineEvent)
	if err != nil {
		log.Printf("engine start failed: %v", err)
		a.failAttempt("the audio engine could not be started")
		return
	}
	// One engine at a time. A client left in the slot from an attempt that
	// ended without its loop noticing would keep its process alive, and the
	// second engine would then be the one every command goes to.
	if old := a.client.Swap(client); old != nil {
		_, _ = old.Stop()
		_ = old.Close()
	}
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
		// The engine could not be told to start, so nothing will ever report a
		// phase for this attempt. Leaving the model in "connecting" would show a
		// connection that is not being made, so the failure is recorded where
		// the surface reads it.
		log.Printf("start failed: %v", err)
		a.state.Apply(engine.Event{Event: "error", Raw: []byte(
			`{"event":"error","message":"could not start the stream"}`)})
		return
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
