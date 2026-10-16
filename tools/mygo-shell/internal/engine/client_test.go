package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeFakeEngine writes a stand-in engine that appends every command it
// receives to the log path given as its first argument, and answers hello and
// start. The real engine is Windows-only, so the client is tested against this.
func writeFakeEngine(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	if runtime.GOOS == "windows" {
		script := filepath.Join(dir, "fake.ps1")
		body := `param([string]$out)
$input | ForEach-Object {
  Add-Content -Path $out -Value $_
  if ($_ -match '"command":"hello"') {
    Write-Output '{"version":1,"id":"c1","session_id":"s","event":"hello","engine_version":"test","mode":"native","live_loopback":true,"commands":["hello","start","stop"],"production_ready":false}'
  }
  if ($_ -match '"command":"start"') {
    Write-Output '{"version":1,"id":"c2","session_id":"s","event":"streaming","requested_latency_ms":500,"sample_rate":44100,"clock_id":"clk","members":1}'
  }
}
`
		if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return script
	}

	script := filepath.Join(dir, "fake-engine")
	body := `#!/bin/sh
out="$1"
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$out"
  case "$line" in
    *'"command":"hello"'*) printf '%s\n' '{"version":1,"id":"c1","session_id":"s","event":"hello","engine_version":"test","mode":"native","live_loopback":true,"commands":["hello","start","stop"],"production_ready":false}' ;;
    *'"command":"start"'*) printf '%s\n' '{"version":1,"id":"c2","session_id":"s","event":"streaming","requested_latency_ms":500,"sample_rate":44100,"clock_id":"clk","members":1}' ;;
  esac
done
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

// startFake runs the stand-in engine and returns the client, the command log
// and the state the client feeds.
func startFake(t *testing.T) (*Client, string, *State) {
	t.Helper()
	exe := writeFakeEngine(t)
	record := filepath.Join(t.TempDir(), "commands.log")

	state := NewState()
	var mu sync.Mutex
	args := []string{record}
	if runtime.GOOS == "windows" {
		args = nil // the PowerShell stand-in takes the path as a parameter
	}
	if runtime.GOOS == "windows" {
		args = []string{"-File", exe, record}
		exe = "powershell"
	}

	c, err := StartWithArgs(exe, args, func(ev Event) {
		mu.Lock()
		state.Apply(ev)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, record, state
}

func TestHelloRoundTripsThroughTheEngine(t *testing.T) {
	c, _, state := startFake(t)
	if _, err := c.Hello(); err != nil {
		t.Fatalf("hello: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h, ok := state.Hello(); ok {
			if h.EngineVersion != "test" {
				t.Fatalf("engine version = %q, want test", h.EngineVersion)
			}
			if !h.LiveLoopback {
				t.Error("live_loopback should be true")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no hello event arrived")
}

func TestStartStreamCarriesTheRequestedLatency(t *testing.T) {
	c, record, state := startFake(t)

	opts := DefaultStreamOptions()
	opts.LatencyMs = 500
	if _, err := c.StartStream([]Peer{{Host: "10.0.10.31", Port: 7000}}, opts); err != nil {
		t.Fatalf("start: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if state.Snapshot().Phase == PhaseStreaming {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	m := state.Snapshot()
	if m.Phase != PhaseStreaming {
		t.Fatalf("phase = %q, want %q", m.Phase, PhaseStreaming)
	}
	if m.RequestedMs != 500 {
		t.Errorf("requested latency = %d, want 500", m.RequestedMs)
	}
	if m.SampleRate != 44100 {
		t.Errorf("sample rate = %d, want 44100", m.SampleRate)
	}

	// The wire format must be one JSON object per line with version 1.
	if record != "" && runtime.GOOS != "windows" {
		b, err := os.ReadFile(record)
		if err == nil && len(b) > 0 {
			first := firstLine(b)
			var req map[string]any
			if err := json.Unmarshal([]byte(first), &req); err != nil {
				t.Fatalf("command is not JSON: %v (%q)", err, first)
			}
			if v, _ := req["version"].(float64); v != 1 {
				t.Errorf("version = %v, want 1", req["version"])
			}
			if req["command"] != "start" {
				t.Errorf("command = %v, want start", req["command"])
			}
		}
	}
}

func TestSendWithoutAnEngineIsAnError(t *testing.T) {
	c := &Client{}
	if _, err := c.Hello(); err != ErrNotRunning {
		t.Errorf("err = %v, want %v", err, ErrNotRunning)
	}
}

func TestMalformedLinesDoNotStopTheReader(t *testing.T) {
	// The reader must skip junk and keep delivering the events after it.
	got := make(chan string, 4)
	c := &Client{handler: func(ev Event) { got <- ev.Event }}
	go c.read(strings.NewReader("not json\n" + `{"version":1,"id":"x","session_id":"s","event":"streaming"}` + "\n\n"))

	select {
	case ev := <-got:
		if ev != "streaming" {
			t.Errorf("event = %q, want streaming", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the reader stopped at the junk line")
	}
}

func firstLine(b []byte) string {
	for i, ch := range b {
		if ch == '\n' {
			return string(b[:i])
		}
	}
	return string(b)
}
