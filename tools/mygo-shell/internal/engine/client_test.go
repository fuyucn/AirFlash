package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain lets this test binary act as a stand-in engine. The client starts a
// process and speaks JSONL to it; rather than shell scripts, which differ per
// platform, the test re-executes itself with an environment variable set, and
// the helper below becomes the engine.
func TestMain(m *testing.M) {
	if os.Getenv(fakeEngineEnv) != "" {
		runFakeEngine()
		return
	}
	os.Exit(m.Run())
}

const fakeEngineEnv = "AIRFLASH_FAKE_ENGINE"

// runFakeEngine is the stand-in: it reads one JSON command per line and answers
// hello and start, appending every command it saw to the log named by the
// environment.
func runFakeEngine() {
	logPath := os.Getenv(fakeEngineEnv)
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1<<20)
	out := bufio.NewWriter(os.Stdout)

	for in.Scan() {
		line := in.Text()
		if logPath != "" {
			if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
				fmt.Fprintln(f, line)
				f.Close()
			}
		}
		switch {
		case strings.Contains(line, `"command":"hello"`):
			fmt.Fprintln(out, `{"version":1,"id":"c1","session_id":"s","event":"hello","engine_version":"test","mode":"native","live_loopback":true,"commands":["hello","start","stop"],"production_ready":false}`)
		case strings.Contains(line, `"command":"start"`):
			fmt.Fprintln(out, `{"version":1,"id":"c2","session_id":"s","event":"streaming","requested_latency_ms":500,"sample_rate":44100,"clock_id":"clk","members":1}`)
		}
		out.Flush()
	}
}

// startFake runs this test binary as a stand-in engine and returns the client,
// the command log and the state it feeds.
func startFake(t *testing.T) (*Client, string, *State) {
	t.Helper()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test binary: %v", err)
	}
	record := filepath.Join(t.TempDir(), "commands.log")

	state := NewState()
	var mu sync.Mutex
	c, err := StartWithOptions(exe, nil,
		[]string{fakeEngineEnv + "=" + record},
		func(ev Event) {
			mu.Lock()
			state.Apply(ev)
			mu.Unlock()
		})
	if err != nil {
		t.Fatalf("start the stand-in engine: %v", err)
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

	// The wire format is one JSON object per line, with version 1.
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("read the command log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("no command reached the engine")
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &req); err != nil {
		t.Fatalf("the first command is not JSON: %v (%q)", err, lines[0])
	}
	if v, _ := req["version"].(float64); v != 1 {
		t.Errorf("version = %v, want 1", req["version"])
	}
	if req["command"] != "start" {
		t.Errorf("command = %v, want start", req["command"])
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
