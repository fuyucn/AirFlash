package equalizer

import "testing"

func TestAPresetIsRecognisedAndAMovedBandIsNot(t *testing.T) {
	// A curve the settings are started from and then a band is moved on is no
	// longer that preset. The interface says "Custom", because that is what it
	// has become.
	s := Default()
	s.Apply(Presets[1]) // Bass boost
	if _, ok := s.Is(); !ok {
		t.Fatal("a preset was applied and is not recognised")
	}
	if name := s.Name(); name != "Bass boost" {
		t.Errorf("name = %q, want Bass boost", name)
	}

	s.SetBand(5, 3)
	if name := s.Name(); name != Custom {
		t.Errorf("name = %q after a band moved, want Custom", name)
	}
}

func TestApplyingAPresetTurnsTheEqualizerOn(t *testing.T) {
	// Choosing a preset is a request to hear it. Leaving it off would look like
	// nothing had happened.
	s := Default()
	if s.Enabled {
		t.Fatal("a fresh equalizer is enabled, want it off")
	}
	s.Apply(Presets[2])
	if !s.Enabled {
		t.Error("applying a preset left the equalizer off")
	}
}

func TestABandIsClampedToWhatTheEngineAccepts(t *testing.T) {
	// The engine validates -12..=12 and rejects anything outside it, so a value
	// that got there would only report as a failure afterwards.
	s := Default()
	s.SetBand(0, 40)
	if got := s.BandGainsDB[0]; got != LimitDB {
		t.Errorf("band = %v, want it clamped to %v", got, LimitDB)
	}
	s.SetBand(0, -40)
	if got := s.BandGainsDB[0]; got != -LimitDB {
		t.Errorf("band = %v, want it clamped to -%v", got, LimitDB)
	}
	// And out of range does nothing rather than panicking.
	s.SetBand(99, 5)
	s.SetBand(-1, 5)
}

func TestThePresetsAreTheDesktopHostsCurves(t *testing.T) {
	// Same numbers and same names as the desktop host, so the two draw the same
	// shapes and a person who has used either recognises them.
	want := map[string][]float64{
		"Flat":       {0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"Bass boost": {6, 4, 2, 0, 0, 0, 0, 0, 0, 0},
		"Vocals":     {-3, -3, -2, 0, 2, 4, 3, 1, -1, -2},
		"Pop":        {-1, 1, 3, 4, 2, 0, -1, 1, 2, 3},
		"Rock":       {4, 3, 1, -1, -2, 0, 2, 4, 4, 3},
	}
	for _, p := range Presets {
		w, ok := want[p.Name]
		if !ok {
			t.Errorf("unexpected preset %q", p.Name)
			continue
		}
		if !same(p.Gains, w) {
			t.Errorf("%s = %v, want %v", p.Name, p.Gains, w)
		}
	}
	if len(Presets) != len(want) {
		t.Errorf("%d presets, want the desktop's %d", len(Presets), len(want))
	}
}

func TestLabelSpellsAFrequencyTheWayAudioToolsDo(t *testing.T) {
	// 31.25 Hz and 16000 Hz as a number each are the same information, but one is
	// read at a glance and the other is counted.
	for i, want := range []string{"31.25 Hz", "62.5 Hz", "125 Hz", "250 Hz", "500 Hz",
		"1 kHz", "2 kHz", "4 kHz", "8 kHz", "16 kHz"} {
		if got := Label(i); got != want {
			t.Errorf("Label(%d) = %q, want %q", i, got, want)
		}
	}
}
