package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsTargetFiveHundredMilliseconds(t *testing.T) {
	// 500 ms is the value that made a first-generation HomePod play smoothly.
	if got := Default().LatencyMs; got != 500 {
		t.Fatalf("default latency = %d, want 500", got)
	}
}

func TestLightweightDropsTheTrendAndMotion(t *testing.T) {
	s := Default()
	s.Mode = ModeLightweight
	s.ShowTrend = true
	s.ReducedMotion = false

	got := s.normalized()
	if got.ShowTrend {
		t.Error("lightweight mode must not keep the trend chart")
	}
	if !got.ReducedMotion {
		t.Error("lightweight mode must not keep the motion")
	}
}

func TestLightweightSamplesLessOftenThanStandard(t *testing.T) {
	standard := Default()
	light := Default()
	light.Mode = ModeLightweight

	if light.SampleIntervalMs() <= standard.SampleIntervalMs() {
		t.Errorf("lightweight samples every %d ms, standard every %d ms; lightweight must be less often",
			light.SampleIntervalMs(), standard.SampleIntervalMs())
	}
	if light.RedrawIntervalMs() <= standard.RedrawIntervalMs() {
		t.Errorf("lightweight redraws every %d ms, standard every %d ms; lightweight must be less often",
			light.RedrawIntervalMs(), standard.RedrawIntervalMs())
	}
}

func TestNormalizeClampsToEngineRanges(t *testing.T) {
	cases := []struct {
		in   Settings
		want Settings
	}{
		{Settings{LatencyMs: 0}, Settings{LatencyMs: minLatencyMs}},
		{Settings{LatencyMs: 99999}, Settings{LatencyMs: maxLatencyMs}},
		// The desktop's Real-time mode asks for 120 ms, and it has to survive
		// the round trip through settings: the floor here used to be 200, which
		// silently raised it and made the desktop's most aggressive mode
		// unreachable from the shell.
		{Settings{LatencyMs: 120}, Settings{LatencyMs: 120}},
		{Settings{Gain: -1}, Settings{Gain: 0}},
		{Settings{Gain: 4}, Settings{Gain: 1}},
		{Settings{SampleRate: 12345}, Settings{SampleRate: 44100}},
		{Settings{SampleRate: 48000}, Settings{SampleRate: 48000}},
	}
	for _, c := range cases {
		got := c.in.normalized()
		if c.want.LatencyMs != 0 && got.LatencyMs != c.want.LatencyMs {
			t.Errorf("latency %d -> %d, want %d", c.in.LatencyMs, got.LatencyMs, c.want.LatencyMs)
		}
		if c.want.Gain != 0 && got.Gain != c.want.Gain {
			t.Errorf("gain %v -> %v, want %v", c.in.Gain, got.Gain, c.want.Gain)
		}
		if c.want.SampleRate != 0 && got.SampleRate != c.want.SampleRate {
			t.Errorf("rate %d -> %d, want %d", c.in.SampleRate, got.SampleRate, c.want.SampleRate)
		}
	}
}

func TestUnknownModeFallsBackToStandard(t *testing.T) {
	s := Settings{Mode: "turbo"}
	if got := s.normalized().Mode; got != ModeStandard {
		t.Errorf("mode %q -> %q, want %q", "turbo", got, ModeStandard)
	}
}

func TestStoreRoundTripsAndSurvivesABrokenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shell.json")

	st := Open(path)
	st.Update(func(s *Settings) {
		s.LatencyMs = 750
		s.Mode = ModeLightweight
	})

	again := Open(path)
	cur := again.Current()
	if cur.LatencyMs != 750 {
		t.Errorf("reloaded latency = %d, want 750", cur.LatencyMs)
	}
	if cur.Mode != ModeLightweight {
		t.Errorf("reloaded mode = %q, want %q", cur.Mode, ModeLightweight)
	}

	// A corrupt file must fall back to the defaults rather than fail to start.
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := Open(path)
	if broken.Current().LatencyMs != Default().LatencyMs {
		t.Errorf("corrupt file gave latency %d, want the default %d",
			broken.Current().LatencyMs, Default().LatencyMs)
	}
}

func TestAutoNeverGoesBelowTheFloorTheUserSet(t *testing.T) {
	// Auto means "you decide", not "ignore what I said". The setting is the
	// fastest the user is willing to accept.
	s := Default()
	s.LatencyAuto = true
	s.LatencyMs = 500

	if got := s.EffectiveLatencyMs(300); got != 500 {
		t.Errorf("target = %d, want the floor 500: Auto must not go faster than the user asked", got)
	}
}

func TestAutoUsesTheAdaptedTargetWhenItIsHigher(t *testing.T) {
	s := Default()
	s.LatencyAuto = true
	s.LatencyMs = 500

	if got := s.EffectiveLatencyMs(800); got != 800 {
		t.Errorf("target = %d, want the adapted 800", got)
	}
}

func TestAutoFallsBackToTheSettingBeforeAnySession(t *testing.T) {
	s := Default()
	s.LatencyAuto = true
	s.LatencyMs = 500

	if got := s.EffectiveLatencyMs(0); got != 500 {
		t.Errorf("target = %d, want the setting 500 when nothing has been learned yet", got)
	}
}

func TestWithAutoOffTheSettingIsUsedExactly(t *testing.T) {
	s := Default()
	s.LatencyAuto = false
	s.LatencyMs = 750

	if got := s.EffectiveLatencyMs(1200); got != 750 {
		t.Errorf("target = %d, want exactly the setting 750 when Auto is off", got)
	}
}

func TestAutoIsOnByDefault(t *testing.T) {
	// The point of the change: the user should not have to pick a number.
	if !Default().LatencyAuto {
		t.Error("Auto should be on by default")
	}
}

func TestEveryNamedLatencyIsWithinWhatTheEngineAccepts(t *testing.T) {
	// A target outside the engine's range is a start that fails, or a silent
	// clamp the user never sees.
	for _, mode := range LatencyChoices() {
		ms := NamedLatencyMs(mode)
		if ms < minLatencyMs || ms > maxLatencyMs {
			t.Errorf("%s asks for %d ms, outside the engine's %d..%d", mode, ms, minLatencyMs, maxLatencyMs)
		}
	}
}

func TestTheNamedLatenciesAreTheDesktopHosts(t *testing.T) {
	// Real-time, normal and buffered are the desktop's own three, with its own
	// numbers. If the two hosts disagree about what "buffered" means, a user
	// who tries both gets two different results from the same word.
	want := map[LatencyMode]int{
		LatencyRealtime: 120,
		LatencyNormal:   200,
		LatencyBuffered: 500,
	}
	for mode, ms := range want {
		if got := NamedLatencyMs(mode); got != ms {
			t.Errorf("%s = %d ms, want the desktop's %d", mode, got, ms)
		}
	}
}
