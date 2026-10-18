// Package adapt picks the latency target for the next session from what the
// last ones actually did.
//
// The engine measures the round trip to the receiver and never acts on it: the
// target stays whatever it was, so a slower network stutters more. This package
// is the missing decision, kept in the shell because it only ever changes the
// target at a session boundary. Nothing here touches the play time of audio
// already in flight, which is what makes it safe.
package adapt

import (
	"math"
	"time"
)

// Observation is what one session turned out to be.
type Observation struct {
	// TargetMs is the latency that session ran with.
	TargetMs int
	// PeakRTTMs is the worst round trip seen during it, or 0 if none was seen.
	PeakRTTMs float64
	// MedianRTTMs is the typical round trip, which says whether the link is
	// merely slow or actually erratic.
	MedianRTTMs float64
	// SenderStalls counts times the sender fell behind and discarded audio.
	SenderStalls uint64
	// SkippedPackets counts the audio slots thrown away when it did.
	SkippedPackets uint64
	// ExpiredResends counts packets the receiver asked for too late to be
	// useful. This is the loss that becomes an audible gap.
	ExpiredResends uint64
	// When the session happened, so old evidence can be discounted.
	At time.Time
}

// Trouble reports whether the session had any evidence of a gap.
func (o Observation) Trouble() bool {
	return o.SenderStalls > 0 || o.SkippedPackets > 0 || o.ExpiredResends > 0
}

// Limits bound what adaptation may do.
const (
	// MinTargetMs and MaxTargetMs mirror what the engine accepts, which is the
	// same range the settings clamp to. The floor used to be 200 here, and the
	// settings' own floor was lowered to 1 when Real-time's 120 ms was found to
	// be silently raised - but this copy was missed, so Auto still handed a
	// user who chose Real-time the 200 ms it was supposed to stop applying.
	MinTargetMs = 1
	MaxTargetMs = 2000

	// safetyFactor is how much of the round trip the buffer should cover. A
	// resend has to reach the receiver before its play deadline, so the budget
	// has to hold several round trips, not one. Three is the smallest value
	// that leaves room for a request, its reply, and one retry.
	safetyFactor = 3.0

	// stepMs is the ordinary move for one session.
	stepMs = 150

	// maxJumpMs bounds a single session's move even when the round trip calls
	// for much more. It converges in a few sessions instead of a dozen, without
	// ever surprising the user with a full jump to the ceiling.
	maxJumpMs = 3 * stepMs

	// settleAfter is how many clean sessions it takes before the target is
	// allowed to come back down. Recovery should be slower than the retreat.
	settleAfter = 2
)

// Recommend returns the target for the next session.
//
// The user's setting is a floor: adaptation only ever raises it, and only when
// a session showed a gap. That keeps the behaviour predictable, and it means a
// good network keeps exactly the latency the user asked for.
func Recommend(userTargetMs int, history []Observation) (target int, reason string) {
	floor := clamp(userTargetMs)

	// Where we are now is what the last session ran with. Without this the
	// target could rise but never come back down.
	current := floor
	if len(history) > 0 && history[len(history)-1].TargetMs > 0 {
		current = clamp(history[len(history)-1].TargetMs)
	}

	if len(history) == 0 {
		return floor, "no sessions yet, using your setting"
	}

	last := history[len(history)-1]

	if !last.Trouble() {
		// Nothing went wrong. Come down only after a few clean sessions in a
		// row, and never below what the user asked for.
		clean := 0
		for i := len(history) - 1; i >= 0 && !history[i].Trouble(); i-- {
			clean++
		}
		if clean >= settleAfter && current > floor {
			next := clamp(current - stepMs)
			if next < floor {
				next = floor
			}
			return next, "recent sessions were clean, easing back toward your setting"
		}
		return current, "the last session was clean"
	}

	// A gap happened, so the target moves up. The round trip sizes the move,
	// but the observed gap is the authority: a session that stuttered shows the
	// target was too small whatever the average round trip says.
	if current >= MaxTargetMs {
		return current, "already at the ceiling"
	}

	next := current + stepMs
	if needed := neededTarget(last); needed > next {
		if cap := current + maxJumpMs; needed > cap {
			next = cap
		} else {
			next = needed
		}
	}
	next = clamp(next)
	if next <= current {
		return current, "already at the ceiling"
	}
	return next, describe(last)
}

// neededTarget is the target a session's own numbers call for. It is a hint for
// how large the step should be, never the decision itself: the gap is.
func neededTarget(o Observation) int {
	// The buffer has to hold the worst round trip seen, several times over, so
	// a resend has room for a request, its reply and one retry.
	worst := o.PeakRTTMs
	if worst <= 0 {
		// No round trip was measured, so the gap was the sender's own. There is
		// no number to size from.
		return 0
	}
	return clamp(int(math.Ceil(worst * safetyFactor)))
}

func describe(o Observation) string {
	switch {
	case o.SenderStalls > 0:
		return "the sender fell behind, so the buffer is being widened"
	case o.ExpiredResends > 0:
		return "resends arrived too late, so the buffer is being widened"
	default:
		return "a gap was recorded, so the buffer is being widened"
	}
}

func clamp(ms int) int {
	if ms < MinTargetMs {
		return MinTargetMs
	}
	if ms > MaxTargetMs {
		return MaxTargetMs
	}
	return ms
}

// Keep bounds the history that is persisted.
func Keep(history []Observation, max int) []Observation {
	if max <= 0 || len(history) <= max {
		return history
	}
	return history[len(history)-max:]
}
