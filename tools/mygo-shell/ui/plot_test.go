package ui

import "testing"

func TestBucketsKeepsASpike(t *testing.T) {
	// Averaging would erase the one reading that matters, so a bucket keeps its
	// extreme rather than its mean.
	hist := make([]float64, 100)
	for i := range hist {
		hist[i] = 10
	}
	hist[50] = 500

	got := buckets(hist, 10)
	if len(got) != 10 {
		t.Fatalf("got %d buckets, want 10", len(got))
	}
	var peak float64
	for _, v := range got {
		if v > peak {
			peak = v
		}
	}
	if peak != 500 {
		t.Errorf("peak = %v, want 500: the spike was averaged away", peak)
	}
}

func TestBucketsLeavesAShortSeriesAlone(t *testing.T) {
	// Fewer points than pixels needs no reduction.
	hist := []float64{1, 2, 3}
	if got := buckets(hist, 500); len(got) != 3 {
		t.Errorf("got %d points, want the 3 unchanged", len(got))
	}
}

func TestBucketsAlwaysProducesSomething(t *testing.T) {
	// A plot with a width but a series that reduces to nothing must not divide
	// by zero when it is drawn.
	for _, width := range []float32{0, 1, 4} {
		got := buckets([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, width)
		if len(got) == 0 {
			t.Errorf("width %v produced no buckets", width)
		}
	}
}

func TestPlotRangeAlwaysIncludesTheTarget(t *testing.T) {
	// The reference line must never leave the frame, whatever the readings did.
	hist := []float64{100, 200, 300}
	lo, hi := plotRange(hist, 500)
	if lo > 500 || hi < 500 {
		t.Errorf("range %v..%v excludes the target 500", lo, hi)
	}
}

func TestPlotRangeLeavesRoomAroundAFlatSeries(t *testing.T) {
	// A perfectly steady reading would otherwise fill the whole height.
	lo, hi := plotRange([]float64{500, 500, 500}, 500)
	if hi-lo < 20 {
		t.Errorf("range %v..%v is too tight to read", lo, hi)
	}
}
