// Package reconnect decides whether to try a dropped session again, and when.
//
// The desktop app already does this, and this is the same policy in Go, so the
// two hosts behave the same way. Its numbers come from SessionController:
//
//	retry_delay = min(retry_ms * 2^(attempt - 1), 8000)
//
// so the first retry is immediate and each one after it doubles, capped at eight
// seconds. A session that ran for ten seconds or more resets the count, because
// a stream that lasted is a good connection that dropped, not one that never
// worked. Reconnecting stops when the user turns it off, when the attempt count
// is spent, or when the failure is one a retry cannot fix - an authentication
// failure means a PIN, and a receiver that reports itself busy is playing for
// somebody else.
package reconnect

import (
	"math"
	"time"
)

// Timing is how long the shell waits at each step, mirroring the desktop's
// SessionTiming::Default.
type Timing struct {
	Connect time.Duration
	Read    time.Duration
	Retry   time.Duration
}

// Default is the desktop app's own timing: 40 s to connect, 15 s between
// events, and a 1 s base retry delay.
var Default = Timing{Connect: 40 * time.Second, Read: 15 * time.Second, Retry: time.Second}

// Policy is the setting that decides whether to retry at all.
type Policy struct {
	// Enabled is the user's ForceReconnect: without it a failure is final.
	Enabled bool
	// MaxAttempts is how many times to try. 1 to 20, as the desktop validates.
	MaxAttempts int
}

// DefaultPolicy is what a fresh install starts with: retry off, five attempts.
func DefaultPolicy() Policy {
	return Policy{MaxAttempts: 5}
}

// Reason is why a session ended, as far as the retry decision is concerned.
type Reason string

const (
	// ReasonUnset is a failure with nothing said against it.
	ReasonUnset Reason = ""
	// ReasonRetryable is a failure that is worth trying again.
	ReasonRetryable Reason = "retryable"
	// ReasonNotRetryable is a failure that will fail the same way again.
	ReasonNotRetryable Reason = "not_retryable"
	// ReasonStopped is the user pressing stop, which is never retried.
	ReasonStopped Reason = "stopped"
)

// Decision is whether to try again, and how long to wait first.
type Decision struct {
	Retry bool
	// After is how long to wait before the next attempt.
	After time.Duration
	// Attempt is the number of the attempt that is being decided about.
	Attempt int
}

// Decide works out whether to retry a session that just failed.
//
//   - elapsed is how long the session had been streaming when it ended.
//   - attempts is how many attempts have already been made, so the first
//     failure passes 0.
//   - reason is why it failed, and reason.Stopped is never retried.
//
// A session that ran for GoodSessionSeconds or more resets the count: it was a
// working connection that dropped, so it gets the full allowance again rather
// than being judged by failures that came before it worked.
func Decide(p Policy, attempts int, elapsed time.Duration, reason Reason) Decision {
	next := attempts + 1
	if !p.Enabled {
		return Decision{Attempt: next}
	}
	if reason == ReasonStopped || reason == ReasonNotRetryable {
		return Decision{Attempt: next}
	}
	// A good session that dropped is worth as much as a first attempt, so its
	// count starts over before anything counts against it.
	if elapsed >= GoodSessionSeconds {
		next = 1
	}
	if p.MaxAttempts > 0 && next > p.MaxAttempts {
		return Decision{Attempt: next}
	}
	return Decision{
		Retry:   true,
		After:   Delay(p, next),
		Attempt: next,
	}
}

// GoodSessionSeconds is how long a session must have streamed to count as good.
//
// The desktop uses ten seconds. Below that a stream is not really established:
// it is still connecting, and a failure in it is the same failure as never
// connecting at all.
var GoodSessionSeconds = 10 * time.Second

// Delay is how long to wait before attempt n, the desktop's formula.
func Delay(p Policy, n int) time.Duration {
	base := Default.Retry
	if n < 1 {
		n = 1
	}
	scaled := float64(base) * math.Pow(2, float64(n-1))
	if scaled > float64(MaxDelay) {
		return MaxDelay
	}
	return time.Duration(scaled)
}

// MaxDelay is the ceiling on a single wait, as the desktop's 8 s cap.
var MaxDelay = 8 * time.Second
