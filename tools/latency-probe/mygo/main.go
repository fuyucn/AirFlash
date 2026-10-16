// AirFlash probe - mygo (Go) desktop app.
//
// Runs the engine's send cadence inside the app process, right next to a live
// UI, so it shows what a Go audio loop really does when the interface is
// running in the same runtime. The frontend draws the live numbers.
package main

import (
	"context"
	"fmt"
	"log"
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

// ProgressEvent carries live samples; the page subscribes with events.progress.
var ProgressEvent = mygo.NewEvent[Progress]("progress")

// Probe is the service the page drives.
type Probe struct {
	mu      sync.Mutex
	stop    atomic.Bool
	running atomic.Bool
	summary Summary
}

// Run starts a measurement beside the live UI and returns at once.
func (p *Probe) Run(slots int, burn int) string {
	if slots <= 0 {
		slots = 1500
	}
	if !p.running.CompareAndSwap(false, true) {
		return "a run is already in flight"
	}
	p.stop.Store(false)
	p.mu.Lock()
	p.summary = Summary{Mode: "in-process", Slots: slots, Burn: burn, CPUs: runtime.NumCPU(), GoMax: runtime.GOMAXPROCS(0)}
	p.mu.Unlock()
	go p.run(slots, burn)
	return fmt.Sprintf("started %d slots with %d competing goroutines", slots, burn)
}

// Stop ends the run early.
func (p *Probe) Stop() string {
	p.stop.Store(true)
	return "stopping"
}

// Summary returns the last finished run.
func (p *Probe) Summary() Summary {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.summary
}

func (p *Probe) run(slots int, burn int) {
	var wg sync.WaitGroup
	for i := 0; i < burn; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x := 0
			for !p.stop.Load() {
				for k := 0; k < 20000; k++ {
					x += k * k
				}
			}
			if x == -1 {
				fmt.Fprintln(log.Writer(), x)
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
				Running: true, Done: i, Total: slots, Latest: l,
				P50: at(.50), P99: at(.99), Max: sorted[len(sorted)-1],
				OverPct: 100 * float64(over) / float64(len(sorted)),
				Elapsed: time.Since(started).Seconds(),
			})
		}
	}

	p.stop.Store(true)
	wg.Wait()
	_ = runtime.GOMAXPROCS(0)

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

	p.mu.Lock()
	p.summary = Summary{
		Mode: "in-process", Slots: len(late), Burn: burn,
		P50: at(.50), P95: at(.95), P99: at(.99), Max: at(1),
		OverPct: pct, Stalls: stalls,
		CPUs: runtime.NumCPU(), GoMax: runtime.GOMAXPROCS(0), Done: true,
	}
	p.mu.Unlock()

	_ = ProgressEvent.Broadcast(Progress{
		Running: false, Done: len(late), Total: slots,
		P50: at(.50), P99: at(.99), Max: at(1), OverPct: pct,
		Elapsed: time.Since(started).Seconds(),
	})
	p.running.Store(false)
}

func main() {
	mygo.Bind(&Probe{})
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:           "AirFlash Probe",
			Width:           980,
			Height:          700,
			MinWidth:        640,
			MinHeight:       480,
			StateKey:        "main",
			BackgroundColor: "light-dark(#f6f7f9, #0f1115)",
			URL:             "/",
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	_ = context.Background()
}
