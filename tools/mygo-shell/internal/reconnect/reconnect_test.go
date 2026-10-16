package reconnect

import (
	"testing"
	"time"
)

func TestTheDelayDoublesAndIsCapped(t *testing.T) {
	// The desktop's formula is min(retry_ms * 2^(n-1), 8000), so 1s, 2s, 4s,
	// 8s and then 8s forever. A wait that grows without bound would leave a user
	// watching a dead stream for minutes.
	want := []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second,
		MaxDelay, MaxDelay, MaxDelay,
	}
	for i, w := range want {
		if got := Delay(DefaultPolicy(), i+1); got != w {
			t.Errorf("Delay(attempt %d) = %v, want %v", i+1, got, w)
		}
	}
}

func TestAGoodSessionGetsItsAttemptsBack(t *testing.T) {
	// A stream that ran for minutes worked, and its failure is a drop rather
	// than a broken connection. Judging it by the attempts spent before it
	// worked would give up on a network that is fine.
	p := DefaultPolicy()
	p.Enabled = true
	p.MaxAttempts = 2

	// Already spent, but the session lasted.
	d := Decide(p, 2, 5*time.Minute, ReasonRetryable)
	if !d.Retry {
		t.Fatal("a session that streamed for minutes is not retried")
	}
	if d.Attempt != 1 {
		t.Errorf("attempt = %d, want 1: a good session starts the count over", d.Attempt)
	}
}

func TestAttemptsRunOut(t *testing.T) {
	p := DefaultPolicy()
	p.Enabled = true
	p.MaxAttempts = 3

	// Three failures in a row, none of them a good session.
	for i := 0; i < p.MaxAttempts; i++ {
		if d := Decide(p, i, time.Second, ReasonRetryable); !d.Retry {
			t.Fatalf("attempt %d was refused, want it retried while attempts remain", i+1)
		}
	}
	if d := Decide(p, p.MaxAttempts, time.Second, ReasonRetryable); d.Retry {
		t.Error("a fourth attempt was retried after the three allowed")
	}
}

func TestReconnectingOffIsFinal(t *testing.T) {
	// The default is off: a user who has not asked for it does not want the app
	// reaching for a speaker again by itself.
	if d := Decide(DefaultPolicy(), 0, 0, ReasonRetryable); d.Retry {
		t.Error("a failure was retried with reconnecting disabled")
	}
}

func TestAFailureThatCannotBeRetriedIsNot(t *testing.T) {
	// The desktop stops on an authentication failure and on a receiver that says
	// a retry cannot help. Retrying either just fails the same way, longer.
	p := DefaultPolicy()
	p.Enabled = true
	if d := Decide(p, 0, time.Second, ReasonNotRetryable); d.Retry {
		t.Error("a failure that cannot be retried was retried")
	}
}

func TestStoppingIsNeverRetried(t *testing.T) {
	// The user pressed stop. Reaching for the speaker again would start a
	// session they just ended.
	p := DefaultPolicy()
	p.Enabled = true
	if d := Decide(p, 0, time.Minute, ReasonStopped); d.Retry {
		t.Error("a session the user stopped was reconnected")
	}
}

func TestAShortSessionCountsAgainstItself(t *testing.T) {
	// A stream that barely started is still a failure to connect, so it is
	// judged with the other attempts rather than forgiven.
	p := DefaultPolicy()
	p.Enabled = true
	p.MaxAttempts = 2

	if d := Decide(p, 0, time.Second, ReasonRetryable); d.Attempt != 1 {
		t.Errorf("attempt = %d, want 1", d.Attempt)
	}
	if d := Decide(p, 1, time.Second, ReasonRetryable); d.Attempt != 2 {
		t.Errorf("attempt = %d, want 2", d.Attempt)
	}
}
