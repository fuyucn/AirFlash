// AirFlash latency probe - Go implementation.
//
// Reproduces the engine's real send cadence (one packet every 7.98 ms) and
// measures how late each deadline is actually met. Run with competing
// goroutines to see what UI work does to the audio path.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	slotMicros = 7980
	spinMicros = 300
	packetSize = 352 * 2 * 2 // 352 frames, stereo, 16-bit PCM
)

var stop atomic.Bool

func main() {
	slots := flag.Int("slots", 1500, "number of 7.98ms slots to run")
	burn := flag.Int("burn", 0, "competing busy goroutines")
	mode := flag.String("mode", "sleep", "sleep | spin")
	udp := flag.Bool("udp", false, "also send a real UDP packet each slot")
	jsonOut := flag.Bool("json", false, "print JSON instead of text")
	flag.Parse()

	var wg sync.WaitGroup
	for i := 0; i < *burn; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x := 0
			for !stop.Load() {
				for k := 0; k < 20000; k++ {
					x += k * k
				}
			}
			if x == -1 {
				fmt.Fprintln(os.Stderr, x)
			}
		}()
	}
	time.Sleep(200 * time.Millisecond)

	var sender *net.UDPConn
	if *udp {
		rcv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			fmt.Fprintln(os.Stderr, "udp listen:", err)
			os.Exit(2)
		}
		defer rcv.Close()
		go func() {
			buf := make([]byte, 4096)
			for {
				if _, _, err := rcv.ReadFromUDP(buf); err != nil {
					return
				}
			}
		}()
		sender, err = net.DialUDP("udp4", nil, rcv.LocalAddr().(*net.UDPAddr))
		if err != nil {
			fmt.Fprintln(os.Stderr, "udp dial:", err)
			os.Exit(2)
		}
		defer sender.Close()
	}

	payload := make([]byte, packetSize)
	late := make([]float64, 0, *slots)
	stalls := 0

	runtime.LockOSThread()
	deadline := time.Now()
	for i := 0; i < *slots; i++ {
		deadline = deadline.Add(slotMicros * time.Microsecond)
		if *mode == "spin" {
			for time.Now().Before(deadline) {
			}
		} else {
			for {
				d := time.Until(deadline)
				if d <= 0 {
					break
				}
				if d > spinMicros*time.Microsecond {
					time.Sleep(min(d-spinMicros*time.Microsecond, 500*time.Microsecond))
				}
			}
		}
		if sender != nil {
			if _, err := sender.Write(payload); err != nil {
				fmt.Fprintln(os.Stderr, "udp write:", err)
				os.Exit(2)
			}
		}
		l := float64(-time.Until(deadline)) / 1e6
		late = append(late, l)
		if l > 20 {
			stalls++
		}
	}

	stop.Store(true)
	wg.Wait()

	sort.Float64s(late)
	at := func(q float64) float64 { return late[int(q*float64(len(late)-1))] }
	over := 0
	for _, l := range late {
		if l > slotMicros/1000 {
			over++
		}
	}
	res := map[string]any{
		"language": "go", "goos": runtime.GOOS, "cpus": runtime.NumCPU(),
		"gomaxprocs": runtime.GOMAXPROCS(0), "mode": *mode, "burn": *burn,
		"udp": *udp, "slots": *slots,
		"p50_ms": at(.50), "p95_ms": at(.95), "p99_ms": at(.99),
		"max_ms": late[len(late)-1],
		"over_one_slot_pct": 100 * float64(over) / float64(len(late)),
		"stalls_over_20ms":  stalls,
	}
	if *jsonOut {
		e := json.NewEncoder(os.Stdout)
		e.SetIndent("", "  ")
		_ = e.Encode(res)
		return
	}
	fmt.Printf("go   mode=%-5s burn=%-3d udp=%-5v | p50=%7.3f p95=%7.3f p99=%7.3f max=%8.3f ms | late>1slot %5.1f%% | stalls>20ms %d\n",
		*mode, *burn, *udp, at(.50), at(.95), at(.99), late[len(late)-1],
		100*float64(over)/float64(len(late)), stalls)
}
