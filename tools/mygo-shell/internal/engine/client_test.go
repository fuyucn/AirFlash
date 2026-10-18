package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

func TestAMalformedLineIsCountedRatherThanDropped(t *testing.T) {
	// Skipping junk keeps the stream alive, but a skip that leaves no trace makes
	// an engine bug that emits broken JSONL look like an engine that stopped
	// sending: the readings stall and nothing says why.
	got := make(chan string, 4)
	c := &Client{handler: func(ev Event) { got <- ev.Event }}
	go c.read(strings.NewReader("not json\n" + `{"version":1,"id":"x","session_id":"s","event":"streaming"}` + "\n"))

	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the reader stopped before the valid line")
	}
	if n := c.MalformedLines(); n != 1 {
		t.Errorf("malformed lines = %d, want 1: a skipped line has to be recorded", n)
	}
}

func TestAValidStreamCountsNoMalformedLines(t *testing.T) {
	// The counter must not fire on ordinary traffic, or it is noise nobody reads.
	got := make(chan string, 4)
	c := &Client{handler: func(ev Event) { got <- ev.Event }}
	go c.read(strings.NewReader(`{"version":1,"id":"x","session_id":"s","event":"streaming"}` + "\n"))

	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("the reader did not deliver the line")
	}
	if n := c.MalformedLines(); n != 0 {
		t.Errorf("malformed lines = %d on valid input, want 0", n)
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

func TestTheEngineProcessEndingIsReported(t *testing.T) {
	// The engine dies and the events simply stop. Without a signal the shell
	// keeps showing the last reading as though audio were still flowing, which
	// is the one thing a streaming app must never imply.
	client, _, _ := startFake(t)

	if _, err := client.Hello(); err != nil {
		t.Fatal(err)
	}

	// Wait blocks until the process ends, which is nothing while it lives.
	select {
	case <-client.Wait():
		t.Fatal("Wait fired while the engine is still running")
	default:
	}

	// The stand-in leaves when its stdin closes, which is how a real engine
	// ends: it stops, and the only thing that records that is this.
	client.Close()
	select {
	case <-client.Wait():
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not report the engine ending")
	}
}

func TestRunningGoesFalseWhenTheEngineDies(t *testing.T) {
	// The panel asks Running() to decide whether it can still send commands.
	client, _, _ := startFake(t)

	if _, err := client.Hello(); err != nil {
		t.Fatal(err)
	}
	if !client.Running() {
		t.Fatal("Running is false before the engine has ended")
	}

	client.Close()
	deadline := time.Now().Add(5 * time.Second)
	for client.Running() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if client.Running() {
		t.Error("Running still reports the engine alive after it ended")
	}
}

func TestClosingStdinEndsTheEngine(t *testing.T) {
	// The shell is going away, so there is nobody left to read the engine's
	// events or write it commands. Closing its stdin is what asks it to exit:
	// the stand-in reads commands until it sees EOF and then returns, as the
	// real engine does.
	//
	// Without this the shell goes away and the engine stays behind, holding the
	// audio device and looking like a second copy of the app in the task list.
	c, _, _ := startFake(t)
	if _, err := c.Hello(); err != nil {
		t.Fatalf("hello: %v", err)
	}
	if !c.Running() {
		t.Fatal("the engine is not running, so this proves nothing")
	}

	// What quit does to the engine.
	_, _ = c.Stop()
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case <-c.Wait():
	case <-time.After(5 * time.Second):
		t.Fatal("the engine was still running after Close: it would outlive the shell")
	}
	if c.Running() {
		t.Error("Running still reports true after the engine ended")
	}
}

func TestACommandAfterCloseIsRefusedRatherThanWritten(t *testing.T) {
	// A command written to a closed stdin is an error nobody sees, because
	// there is no reader. Saying so is what lets the caller notice the engine is
	// gone instead of believing a gain change landed.
	c, _, _ := startFake(t)
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := c.SetGain(0.5); err == nil {
		t.Error("a command after Close was accepted, so a caller would think it was applied")
	}
}

func TestACloseCanStillHappenWhileAWriteIsStuck(t *testing.T) {
	// Send held the client's lock across the write to the engine's stdin. That
	// write blocks when the pipe buffer is full and the engine has stopped
	// reading - a wedged or exiting engine. While it blocked, Close could not
	// run either, because Close takes the same lock: so the one thing that can
	// tell a wedged engine to go away was itself waiting behind the write that
	// needed it to.
	//
	// The lock guards the handle, not the write.
	c := &Client{cmd: &exec.Cmd{}, stdin: blockedWriter{}, session: "s"}

	writing := make(chan struct{})
	go func() {
		c.Send("hello", nil)
		close(writing)
	}()
	select {
	case <-writing:
		t.Fatal("the write did not block, so this test proves nothing")
	case <-time.After(50 * time.Millisecond):
	}

	// Close must get through while the write is still stuck.
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	select {
	case <-closed:
		// What Close does is not the point; that it returned is.
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked behind a stuck write: a wedged engine cannot be shut down")
	}

	// And a command sent after the close is refused rather than written, so the
	// handle is not used once it is gone.
	if _, err := c.Send("stop", nil); err != ErrNotRunning {
		t.Errorf("a command after Close returned %v, want %v", err, ErrNotRunning)
	}
}

// blockedWriter is a stdin the engine never reads, so a write to it blocks the
// way a full pipe does.
type blockedWriter struct{}

func (blockedWriter) Write(p []byte) (int, error) {
	select {}
}

func (blockedWriter) Close() error { return nil }
