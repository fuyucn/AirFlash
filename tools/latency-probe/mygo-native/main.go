// AirFlash probe - mygo with native UI (no webview).
//
// The same send-cadence measurement as the webview app, but the window is
// drawn by MyGo itself on the GPU. This is the variant that shows what the
// webview costs: no WebView2 process tree, no HTML, no JavaScript.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	slotMicros = 7980
	spinMicros = 300
)

// AutoReport is what CI reads.
type AutoReport struct {
	Language   string  `json:"language"`
	UI         string  `json:"ui"`
	CPUs       int     `json:"cpus"`
	GoMax      int     `json:"gomaxprocs"`
	WorkingSet int64   `json:"working_set_bytes"`
	Private    int64   `json:"private_bytes"`
	P50        float64 `json:"p50_ms"`
	P95        float64 `json:"p95_ms"`
	P99        float64 `json:"p99_ms"`
	Max        float64 `json:"max_ms"`
	OverPct    float64 `json:"over_one_slot_pct"`
	Stalls     int     `json:"stalls_over_20ms"`
	Note       string  `json:"note"`
}

type state struct {
	mu      sync.Mutex
	running atomic.Bool
	stop    atomic.Bool
	done    int
	total   int
	p50     float64
	p99     float64
	max     float64
	over    float64
	status  string
	series  []float64
}

func (s *state) snapshot() (int, int, float64, float64, float64, float64, string, []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done, s.total, s.p50, s.p99, s.max, s.over, s.status,
		append([]float64(nil), s.series...)
}

func (s *state) update(fn func()) {
	s.mu.Lock()
	fn()
	s.mu.Unlock()
}

func barColor(v float64) ui.Color {
	if v > 7.98 {
		return ui.Hex("#dc2626")
	}
	return ui.Hex("#2563eb")
}

func (s *state) view(c *ui.Context) {
	done, total, p50, p99, mx, over, status, series := s.snapshot()

	ui.Column(c).Fill().Padding(20).Gap(14).Children(func() {
		ui.Text(c, "AirFlash Probe - mygo native UI").FontSize(22).Bold()
		ui.Text(c, fmt.Sprintf("%d logical CPUs - GOMAXPROCS %d - no webview, no HTML",
			runtime.NumCPU(), runtime.GOMAXPROCS(0))).FontSize(13)

		ui.Row(c).Gap(10).Children(func() {
			if ui.PrimaryButton(c, "Run (no load)").Clicked() && !s.running.Load() {
				go s.measure(800, 0, "burn=0")
			}
			if ui.Button(c, "Run (all cores)").Clicked() && !s.running.Load() {
				go s.measure(800, runtime.NumCPU(), fmt.Sprintf("burn=%d", runtime.NumCPU()))
			}
		})

		ui.Text(c, status).FontSize(13)

		ui.Row(c).Gap(18).Children(func() {
			ui.Text(c, fmt.Sprintf("p50 %.3f ms", p50)).FontSize(15)
			ui.Text(c, fmt.Sprintf("p99 %.3f ms", p99)).FontSize(15)
			ui.Text(c, fmt.Sprintf("max %.3f ms", mx)).FontSize(15)
			ui.Text(c, fmt.Sprintf("late %.1f%%", over)).FontSize(15)
		})

		if total > 0 {
			ui.Text(c, fmt.Sprintf("%d / %d slots", done, total)).FontSize(12)
		}

		const h float32 = 120
		ui.Row(c).Height(h).Gap(1).Children(func() {
			for _, v := range series {
				f := float32(math.Min(v/12, 1))
				barH := f * h
				if barH < 2 {
					barH = 2
				}
				ui.Box(c).Width(2).Height(barH).Background(barColor(v))
			}
		})
	})
}

func (s *state) measure(slots, burn int, phase string) Summary {
	if !s.running.CompareAndSwap(false, true) {
		return Summary{}
	}
	defer s.running.Store(false)
	s.stop.Store(false)

	doneCh := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < burn; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x := 0
			for {
				select {
				case <-doneCh:
					return
				default:
				}
				for k := 0; k < 20000; k++ {
					x += k * k
				}
			}
		}()
	}
	time.Sleep(200 * time.Millisecond)

	late := make([]float64, 0, slots)
	stalls := 0
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	deadline := time.Now()
	for i := 0; i < slots && !s.stop.Load(); i++ {
		deadline = deadline.Add(slotMicros * time.Microsecond)
		for {
			d := time.Until(deadline)
			if d <= 0 {
				break
			}
			if d > spinMicros*time.Microsecond {
				time.Sleep(min(d-spinMicros*time.Microsecond, 500*time.Microsecond))
			}
		}
		l := float64(-time.Until(deadline)) / 1e6
		if l > 20 {
			stalls++
		}
		late = append(late, l)

		if i%20 == 0 {
			sorted := append([]float64(nil), late...)
			sort.Float64s(sorted)
			at := func(q float64) float64 { return sorted[int(q*float64(len(sorted)-1))] }
			o := 0
			for _, v := range sorted {
				if v > slotMicros/1000 {
					o++
				}
			}
			pct := 100 * float64(o) / float64(len(sorted))
			s.update(func() {
				s.done, s.total = i, slots
				s.p50, s.p99, s.max, s.over = at(.50), at(.99), sorted[len(sorted)-1], pct
				s.status = fmt.Sprintf("%s - %d stalls > 20 ms", phase, stalls)
				s.series = append(s.series, l)
				if len(s.series) > 160 {
					s.series = s.series[len(s.series)-160:]
				}
			})
		}
	}
	close(doneCh)
	wg.Wait()

	sort.Float64s(late)
	at := func(q float64) float64 {
		if len(late) == 0 {
			return 0
		}
		return late[int(math.Round(q*float64(len(late)-1)))]
	}
	o := 0
	for _, l := range late {
		if l > slotMicros/1000 {
			o++
		}
	}
	pct := 0.0
	if len(late) > 0 {
		pct = 100 * float64(o) / float64(len(late))
	}
	sum := Summary{
		Slots: len(late), Burn: burn,
		P50: at(.50), P95: at(.95), P99: at(.99), Max: at(1),
		OverPct: pct, Stalls: stalls,
	}
	s.update(func() {
		s.done, s.total = len(late), slots
		s.p50, s.p99, s.max, s.over = sum.P50, sum.P99, sum.Max, pct
		s.status = fmt.Sprintf("%s DONE - p50 %.3f p99 %.3f max %.3f ms - %.1f%% late - %d stalls",
			phase, sum.P50, sum.P99, sum.Max, pct, stalls)
	})
	return sum
}

// Summary is the result of one measurement.
type Summary struct {
	Slots   int     `json:"slots"`
	Burn    int     `json:"burn"`
	P50     float64 `json:"p50_ms"`
	P95     float64 `json:"p95_ms"`
	P99     float64 `json:"p99_ms"`
	Max     float64 `json:"max_ms"`
	OverPct float64 `json:"over_one_slot_pct"`
	Stalls  int     `json:"stalls_over_20ms"`
}

func runAuto(out string, slots, burn int) {
	s := &state{status: "auto"}
	done := make(chan Summary, 1)
	go func() {
		time.Sleep(3 * time.Second) // let the window come up and paint
		done <- s.measure(slots, burn, fmt.Sprintf("burn=%d", burn))
	}()

	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "AirFlash Probe (native)",
			Width:   900,
			Height:  520,
			Content: ui.View(s.view),
		})
	})
	go func() {
		sum := <-done
		ws, priv := processMemory()
		rep := AutoReport{
			Language: "go", UI: "mygo (Go, native UI, no webview)",
			CPUs: runtime.NumCPU(), GoMax: runtime.GOMAXPROCS(0),
			WorkingSet: ws, Private: priv,
			P50: sum.P50, P95: sum.P95, P99: sum.P99, Max: sum.Max,
			OverPct: sum.OverPct, Stalls: sum.Stalls,
			Note: fmt.Sprintf("in-process with live native UI, %d slots, burn=%d", sum.Slots, burn),
		}
		b, _ := json.MarshalIndent(rep, "", "  ")
		_ = os.WriteFile(out, b, 0o644)
		fmt.Println(string(b))
		os.Exit(0)
	}()
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func main() {
	auto := ""
	slots, burn := 800, runtime.NumCPU()
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-auto":
			if i+1 < len(args) {
				auto = args[i+1]
				i++
			}
		case "-slots":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &slots)
				i++
			}
		case "-burn":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &burn)
				i++
			}
		}
	}
	if auto != "" {
		runAuto(auto, slots, burn)
		return
	}

	s := &state{status: "idle - native UI, no webview"}
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "AirFlash Probe (native)",
			Width:   900,
			Height:  520,
			Content: ui.View(s.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
