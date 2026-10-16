package adapt

import "testing"

func clean(target int) Observation {
	return Observation{TargetMs: target, PeakRTTMs: 8, MedianRTTMs: 6}
}

func TestWithNoHistoryTheUserSettingStands(t *testing.T) {
	got, _ := Recommend(500, nil)
	if got != 500 {
		t.Errorf("target = %d, want the user's 500", got)
	}
}

func TestACleanSessionNeverRaisesTheTarget(t *testing.T) {
	// A good network must keep exactly what the user asked for.
	got, _ := Recommend(500, []Observation{clean(500)})
	if got != 500 {
		t.Errorf("target = %d, want 500: a clean session must not widen the buffer", got)
	}
}

func TestAGapWidensTheBuffer(t *testing.T) {
	// The whole point: a session with a gap raises the target.
	o := Observation{TargetMs: 500, PeakRTTMs: 80, ExpiredResends: 3}
	got, reason := Recommend(500, []Observation{o})
	if got <= 500 {
		t.Errorf("target = %d, want more than 500 after a gap", got)
	}
	if reason == "" {
		t.Error("a reason should be given")
	}
}

func TestTheTargetNeverGoesBelowTheUserSetting(t *testing.T) {
	// The setting is a floor. Adaptation may raise it, never lower it.
	cases := [][]Observation{
		{clean(500)},
		{clean(500), clean(500), clean(500), clean(500)},
		{{TargetMs: 500, PeakRTTMs: 1}},
	}
	for i, h := range cases {
		if got, _ := Recommend(500, h); got < 500 {
			t.Errorf("case %d: target = %d, want at least 500", i, got)
		}
	}
}

func TestASingleSessionCannotJumpToTheCeiling(t *testing.T) {
	// A very bad round trip may call for a large target, but one session must
	// not take it there: the move is bounded so it stays predictable.
	o := Observation{TargetMs: 200, PeakRTTMs: 600, ExpiredResends: 10}
	got, _ := Recommend(200, []Observation{o})
	if got > 200+maxJumpMs {
		t.Errorf("target = %d, want at most %d from 200", got, maxJumpMs)
	}
	if got <= 200 {
		t.Errorf("target = %d, want it raised after a gap", got)
	}
}

func TestTheTargetComesDownAfterSeveralCleanSessions(t *testing.T) {
	// Recovery is allowed, but only after the link has been clean a while.
	raised := Observation{TargetMs: 500, PeakRTTMs: 200, ExpiredResends: 5}

	afterOne, _ := Recommend(500, []Observation{raised, clean(650)})
	if afterOne != 650 {
		t.Errorf("after one clean session target = %d, want it held at 650", afterOne)
	}

	afterTwo, _ := Recommend(500, []Observation{raised, clean(650), clean(650)})
	if afterTwo >= 650 {
		t.Errorf("after two clean sessions target = %d, want it eased down", afterTwo)
	}
	if afterTwo < 500 {
		t.Errorf("target = %d, must not fall below the user's 500", afterTwo)
	}
}

func TestTheTargetIsAlwaysWithinWhatTheEngineAccepts(t *testing.T) {
	// The engine rejects a target outside 200..2000.
	for _, target := range []int{0, 50, 199, 2000, 5000} {
		got, _ := Recommend(target, []Observation{{TargetMs: target, PeakRTTMs: 5000, ExpiredResends: 1}})
		if got < MinTargetMs || got > MaxTargetMs {
			t.Errorf("target %d gave %d, outside %d..%d", target, got, MinTargetMs, MaxTargetMs)
		}
	}
}

func TestAnUnmeasuredRoundTripStillMovesTheTargetOneStep(t *testing.T) {
	// When no round trip was seen the gap was the sender's own, so there is no
	// number to size from. One step of room is the honest answer.
	o := Observation{TargetMs: 500, PeakRTTMs: 0, SenderStalls: 2, SkippedPackets: 30}
	got, _ := Recommend(500, []Observation{o})
	if got != 500+stepMs {
		t.Errorf("target = %d, want %d", got, 500+stepMs)
	}
}

func TestASenderStallCountsAsTrouble(t *testing.T) {
	o := Observation{TargetMs: 500, SenderStalls: 1}
	if !o.Trouble() {
		t.Error("a sender stall should count as trouble")
	}
	if (Observation{}).Trouble() {
		t.Error("an empty observation should not count as trouble")
	}
}

func TestTheCeilingIsRespected(t *testing.T) {
	o := Observation{TargetMs: MaxTargetMs, PeakRTTMs: 5000, ExpiredResends: 1}
	got, _ := Recommend(MaxTargetMs, []Observation{o})
	if got != MaxTargetMs {
		t.Errorf("target = %d, want it held at the ceiling %d", got, MaxTargetMs)
	}
}

func TestKeepBoundsTheHistory(t *testing.T) {
	h := make([]Observation, 20)
	if got := Keep(h, 5); len(got) != 5 {
		t.Errorf("kept %d, want 5", len(got))
	}
	if got := Keep(h, 50); len(got) != 20 {
		t.Errorf("kept %d, want all 20", len(got))
	}
}
