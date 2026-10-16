// AirFlash probe - mygo (Go) desktop app.
//
// Runs the engine's send cadence inside the app process, right next to a live
// UI, so it shows what a Go audio loop really does when the interface is
// running in the same runtime. That is deliberately the harder case: AirFlash
// keeps its engine in a separate process today.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"math"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

const (
	slotMicros = 7980
	spinMicros = 300
)

// Progress is streamed to the page while a run is in flight.
type Progress struct {
	Running bool    `json:"running"`
	Phase   string  `json:"phase"`
	Done    int     `json:"done"`
	Total   int     `json:"total"`
	Latest  float64 `json:"latest"`
	P50     float64 `json:"p50"`
	P99     float64 `json:"p99"`
	Max     float64 `json:"max"`
	OverPct float64 `json:"overPct"`
	Elapsed float64 `json:"elapsed"`
}

// Summary is the outcome of a finished run.
type Summary struct {
	Mode    string  `json:"mode"`
	Slots   int     `json:"slots"`
	Burn    int     `json:"burn"`
	P50     float64 `json:"p50"`
	P95     float64 `json:"p95"`
	P99     float64 `json:"p99"`
	Max     float64 `json:"max"`
	OverPct float64 `json:"overPct"`
	Stalls  int     `json:"stalls"`
	CPUs    int     `json:"cpus"`
	GoMax   int     `json:"gomaxprocs"`
	Done    bool    `json:"done"`
}

// SweepRow is one load level of a sweep.
type SweepRow struct {
	Burn    int     `json:"burn"`
	P50     float64 `json:"p50"`
	P95     float64 `json:"p95"`
	P99     float64 `json:"p99"`
	Max     float64 `json:"max"`
	OverPct float64 `json:"overPct"`
	Stalls  int     `json:"stalls"`
}

// SweepReport carries the load sweep as it fills in.
type SweepReport struct {
	Running bool       `json:"running"`
	Done    bool       `json:"done"`
	Cores   int        `json:"cores"`
	GoMax   int        `json:"gomaxprocs"`
	Slots   int        `json:"slots"`
	Active  int        `json:"active"`
	Rows    []SweepRow `json:"rows"`
}

// ProgressEvent carries live samples; the page subscribes with events.progress.
var ProgressEvent = mygo.NewEvent[Progress]("progress")

// SweepEvent reports each finished load level.
var SweepEvent = mygo.NewEvent[SweepReport]("sweep")

// Probe is the service the page drives.
type Probe struct {
	mu      sync.Mutex
	stop    atomic.Bool
	running atomic.Bool
	summary Summary
}

// Environment reports the machine the probe is running on.
func (p *Probe) Environment() Summary {
	return Summary{
		Mode:  "in-process with live UI",
		CPUs:  runtime.NumCPU(),
		GoMax: runtime.GOMAXPROCS(0),
		Done:  true,
	}
}

// Run starts one measurement and returns at once.
func (p *Probe) Run(slots int, burn int) string {
	if slots <= 0 {
		slots = 1500
	}
	if !p.running.CompareAndSwap(false, true) {
		return "a run is already in flight"
	}
	p.stop.Store(false)
	go func() {
		defer p.running.Store(false)
		s := p.measure(slots, burn, fmt.Sprintf("burn=%d", burn))
		p.mu.Lock()
		p.summary = s
		p.mu.Unlock()
	}()
	return fmt.Sprintf("started %d slots with %d competing goroutines", slots, burn)
}

// Sweep measures the same loop at rising fractions of the core count, which is
// what shows where a runtime stops holding the 7.98 ms deadline.
func (p *Probe) Sweep(slots int) string {
	if slots <= 0 {
		slots = 800
	}
	if !p.running.CompareAndSwap(false, true) {
		return "a run is already in flight"
	}
	p.stop.Store(false)
	go func() {
		defer p.running.Store(false)

		cores := runtime.NumCPU()
		// Fractions of the core count, de-duplicated so small machines do not
		// run the same level twice.
		candidates := []int{0, cores / 4, cores / 2, 3 * cores / 4, cores, 2 * cores}
		levels := make([]int, 0, len(candidates))
		seen := map[int]bool{}
		for _, c := range candidates {
			if c < 0 || seen[c] {
				continue
			}
			seen[c] = true
			levels = append(levels, c)
		}

		rows := make([]SweepRow, 0, len(levels))
		_ = SweepEvent.Broadcast(SweepReport{
			Running: true, Cores: cores, GoMax: runtime.GOMAXPROCS(0),
			Slots: slots, Rows: rows,
		})

		for _, burn := range levels {
			if p.stop.Load() {
				break
			}
			s := p.measure(slots, burn, fmt.Sprintf("burn=%d", burn))
			rows = append(rows, SweepRow{
				Burn: burn, P50: s.P50, P95: s.P95, P99: s.P99,
				Max: s.Max, OverPct: s.OverPct, Stalls: s.Stalls,
			})
			_ = SweepEvent.Broadcast(SweepReport{
				Running: true, Cores: cores, GoMax: runtime.GOMAXPROCS(0),
				Slots: slots, Active: burn, Rows: rows,
			})
			if s.Slots == 0 {
				break
			}
		}

		_ = SweepEvent.Broadcast(SweepReport{
			Running: false, Done: true, Cores: cores,
			GoMax: runtime.GOMAXPROCS(0), Slots: slots, Rows: rows,
		})
	}()
	return fmt.Sprintf("sweeping %d load levels on %d cores", 6, runtime.NumCPU())
}

// Stop ends the current run early.
func (p *Probe) Stop() string {
	p.stop.Store(true)
	return "stopping"
}

// Summary returns the last finished single run.
func (p *Probe) Summary() Summary {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.summary
}

// measure runs the send cadence with `burn` competing goroutines and reports
// how late each deadline was actually met.
func (p *Probe) measure(slots, burn int, phase string) Summary {
	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < burn; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x := 0
			for {
				select {
				case <-done:
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
	started := time.Now()

	// The engine pins its send loop to one OS thread; do the same here.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	deadline := time.Now()
	for i := 0; i < slots && !p.stop.Load(); i++ {
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

		if i%25 == 0 {
			sorted := append([]float64(nil), late...)
			sort.Float64s(sorted)
			at := func(q float64) float64 { return sorted[int(q*float64(len(sorted)-1))] }
			over := 0
			for _, v := range sorted {
				if v > slotMicros/1000 {
					over++
				}
			}
			_ = ProgressEvent.Broadcast(Progress{
				Running: true, Phase: phase, Done: i, Total: slots, Latest: l,
				P50: at(.50), P99: at(.99), Max: sorted[len(sorted)-1],
				OverPct: 100 * float64(over) / float64(len(sorted)),
				Elapsed: time.Since(started).Seconds(),
			})
		}
	}

	close(done)
	wg.Wait()

	sort.Float64s(late)
	at := func(q float64) float64 {
		if len(late) == 0 {
			return 0
		}
		return late[int(math.Round(q*float64(len(late)-1)))]
	}
	over := 0
	for _, l := range late {
		if l > slotMicros/1000 {
			over++
		}
	}
	pct := 0.0
	if len(late) > 0 {
		pct = 100 * float64(over) / float64(len(late))
	}

	s := Summary{
		Mode: "in-process with live UI", Slots: len(late), Burn: burn,
		P50: at(.50), P95: at(.95), P99: at(.99), Max: at(1),
		OverPct: pct, Stalls: stalls,
		CPUs: runtime.NumCPU(), GoMax: runtime.GOMAXPROCS(0), Done: true,
	}

	_ = ProgressEvent.Broadcast(Progress{
		Running: false, Phase: phase, Done: len(late), Total: slots,
		P50: at(.50), P99: at(.99), Max: at(1), OverPct: pct,
		Elapsed: time.Since(started).Seconds(),
	})
	return s
}

// AutoReport is written by the CI auto mode.
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

// runAuto opens the window, makes sure the UI is live, runs the measurement
// with the machine saturated, writes a JSON report and exits. This is the only
// way to get real Windows numbers for a GUI app from CI.
func runAuto(out string, slots, burn int) {
	p := &Probe{}
	done := make(chan Summary, 1)
	go func() {
		time.Sleep(3 * time.Second) // let the window come up and paint
		done <- p.measure(slots, burn, fmt.Sprintf("burn=%d", burn))
	}()

	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:           "AirFlash Probe",
			Width:           1000,
			Height:          780,
			BackgroundColor: "light-dark(#f6f7f9, #0f1115)",
			URL:             "/",
		})
	})
	go func() {
		s := <-done
		ws, priv := processMemory()
		rep := AutoReport{
			Language: "go", UI: "mygo (Go, webview)", CPUs: runtime.NumCPU(),
			GoMax: runtime.GOMAXPROCS(0), WorkingSet: ws, Private: priv,
			P50: s.P50, P95: s.P95, P99: s.P99, Max: s.Max,
			OverPct: s.OverPct, Stalls: s.Stalls,
			Note: fmt.Sprintf("in-process with live UI, %d slots, burn=%d", s.Slots, burn),
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

	mygo.Bind(&Probe{})
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:           "AirFlash Probe",
			Width:           1000,
			Height:          780,
			MinWidth:        720,
			MinHeight:       560,
			StateKey:        "main",
			BackgroundColor: "light-dark(#f6f7f9, #0f1115)",
			URL:             "/",
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
