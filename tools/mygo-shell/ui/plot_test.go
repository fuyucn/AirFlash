package ui

import (
	"image"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/i18n"
)

func TestBucketsKeepsASpikeAsAMark(t *testing.T) {
	// A spike is the one reading worth seeing, so it has to survive the
	// reduction. It is carried as a mark on the step rather than as the height
	// the step sits at: a peak made the whole step jump to it, which is what
	// turned the chart into a zigzag.
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
	for _, b := range got {
		if b.Peak > peak {
			peak = b.Peak
		}
	}
	if peak != 500 {
		t.Errorf("peak = %v, want 500: the spike was lost", peak)
	}
	// And the step it sits in still reports an ordinary mean, so the line does
	// not leap to it.
	spikeStep := -1
	for i, b := range got {
		if b.Peak > 100 {
			spikeStep = i
			if b.Mean > 100 {
				t.Errorf("step %d sits at %.0f, want an ordinary mean: the peak belongs to a mark, not the line", i, b.Mean)
			}
		}
	}
	if spikeStep < 0 {
		t.Error("no step reported the spike")
	}
}

func TestBucketsReturnsTheMeanForTheLine(t *testing.T) {
	// The line is drawn from the means, and steps that differ only in one brief
	// reading should not differ in the line. This is the jaggedness the user
	// saw: a few milliseconds of jitter became the height of a whole step.
	flat := make([]float64, 100)
	for i := range flat {
		flat[i] = 480
	}
	// Same series, one reading a little high in each step.
	jittery := append([]float64(nil), flat...)
	for i := 0; i < len(jittery); i += 10 {
		jittery[i] += 6
	}

	one, two := buckets(flat, 20), buckets(jittery, 20)
	if len(one) != len(two) {
		t.Fatalf("got %d and %d buckets, want the same count", len(one), len(two))
	}
	for i := range one {
		// A 6 ms reading in a step of 480 s should not move the line more than a
		// fraction of a millisecond.
		if d := one[i].Mean - two[i].Mean; d > 5 || d < -5 {
			t.Errorf("step %d moved %.0f ms, want the line not to jump for a brief reading", i, d)
		}
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

func TestGapSummaryCountsTheSecondsThatLostAudio(t *testing.T) {
	slots := []engine.Slot{{}, {Skipped: 3}, {}, {Recoveries: 1}, {}}
	if got := gapSummary(i18n.For(i18n.English), slots); got != "2 of 5 s lost audio" {
		t.Errorf("summary = %q, want the two gapped seconds named", got)
	}
}

func TestGapSummarySaysSoWhenNothingWasLost(t *testing.T) {
	// The good case has to be stated, not left blank: an empty chart is
	// indistinguishable from a chart that is not working.
	slots := make([]engine.Slot, 12)
	if got := gapSummary(i18n.For(i18n.English), slots); got != "none in the last 12 s" {
		t.Errorf("summary = %q, want it to say the session was clean", got)
	}
}

func TestGapSummaryIsNotGreenBeforeAnythingIsMeasured(t *testing.T) {
	// Green is a claim. Before the first report there is nothing to claim.
	if got := gapSummaryColor(forDark(false), nil); got == forDark(false).Success {
		t.Error("an unmeasured session is reported as clean")
	}
}

func TestTheGapRibbonDrawsSomethingForEverySecond(t *testing.T) {
	// The ribbon is one column per second. If it dropped columns, the marks
	// would no longer line up with the latency chart above them, which is the
	// only thing that makes them useful.
	slots := make([]engine.Slot, 40)
	slots[10].Skipped = 4

	img := renderRibbon(slots, 400, 34)
	// Find the columns holding the warning colour and check there is one, at
	// roughly the tenth of the width from the left.
	warn := forDark(false).Warning
	first, last := -1, -1
	for x := 0; x < 400; x++ {
		if matches(img.At(x, 17), warn) {
			if first < 0 {
				first = x
			}
			last = x
		}
	}
	if first < 0 {
		t.Fatal("the gapped second was not drawn")
	}
	// Newest is at the right, so second 10 of 40 sits 29 seconds back. At 10 px
	// a second that is near x=100, which is also what makes the marks line up
	// with the latency chart drawn the same way.
	if first < 90 || first > 110 {
		t.Errorf("the mark is at x=%d..%d, want it near x=100 where the tenth second sits", first, last)
	}
}

// renderRibbon draws the ribbon alone, so its pixels can be inspected.
func renderRibbon(slots []engine.Slot, w, h int) *image.RGBA {
	return ui.Render(func(c *ui.Context) {
		p := forDark(false)
		ui.Box(c).Width(float32(w)).Height(float32(h)).Draw(func(pt *ui.Painter, r ui.Rect) {
			drawGapRibbon(pt, r, p, slots)
		})
	}, w, h, 1)
}

// matches reports whether a rendered pixel is the given colour. The renderer
// works in premultiplied alpha, so the comparison is on the channels that
// survive that, at the tolerance a colour round trip leaves.
func matches(c color.Color, want ui.Color) bool {
	r, g, b, _ := c.RGBA()
	return absDiff(uint8(r>>8), want.R) <= 2 &&
		absDiff(uint8(g>>8), want.G) <= 2 &&
		absDiff(uint8(b>>8), want.B) <= 2
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}
