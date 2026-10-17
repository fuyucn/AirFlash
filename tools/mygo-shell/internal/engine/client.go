// Package engine speaks the AirPlay engine's JSONL v1 protocol.
//
// The engine is a separate process: this package starts it, writes one JSON
// command per line to its stdin and reads one JSON event per line from its
// stdout. Keeping the audio path in its own process is deliberate - it is the
// only way a Go runtime keeps the 7.98 ms send deadline when the UI is busy.
package engine

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Command names the engine accepts, from its `hello` event.
const (
	CmdHello           = "hello"
	CmdStart           = "start"
	CmdStop            = "stop"
	CmdSetGain         = "set_gain"
	CmdSetEqualizer    = "set_equalizer"
	CmdSetDeviceVolume = "set_device_volume"
	CmdPair            = "pair"
	CmdPairPin         = "pair_pin"
	ProtocolVersion    = 1
)

// Event is one line from the engine.
type Event struct {
	Version   int             `json:"version"`
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	Event     string          `json:"event"`
	Raw       json.RawMessage `json:"-"`
}

// Hello is the engine's greeting.
type Hello struct {
	EngineVersion   string   `json:"engine_version"`
	Mode            string   `json:"mode"`
	Qualification   string   `json:"qualification"`
	LiveLoopback    bool     `json:"live_loopback"`
	Commands        []string `json:"commands"`
	ProductionReady bool     `json:"production_ready"`
}

// Handler receives every event the engine emits. It is called from the
// reader goroutine, so it must not block for long.
type Handler func(Event)

// Client owns one engine process.
type Client struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	seq     atomic.Uint64
	session string
	handler Handler
	exited  atomic.Bool
	// exitedCh is closed once, when the engine process ends.
	exitedCh   chan struct{}
	exitedOnce sync.Once
}

// ErrNotRunning is returned when no engine process is alive.
var ErrNotRunning = errors.New("engine is not running")

// Start launches the engine and begins reading its events.
func Start(path string, handler Handler) (*Client, error) {
	return StartWithArgs(path, nil, handler)
}

// StartWithArgs launches the engine with arguments. Tests use it to point a
// stand-in engine at a log file.
func StartWithArgs(path string, args []string, handler Handler) (*Client, error) {
	return StartWithOptions(path, args, nil, handler)
}

// StartWithOptions launches the engine with arguments and an environment.
func StartWithOptions(path string, args []string, env []string, handler Handler) (*Client, error) {
	cmd := exec.Command(path, args...)
	hideConsole(cmd)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("engine stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("engine stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("engine stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start engine: %w", err)
	}

	c := &Client{cmd: cmd, stdin: stdin, handler: handler, session: newSessionID(),
		exitedCh: make(chan struct{})}
	go c.read(stdout)
	go drain(stderr)
	go func() {
		_ = cmd.Wait()
		c.exited.Store(true)
		c.exitedOnce.Do(func() { close(c.exitedCh) })
	}()
	return c, nil
}

// Wait reports when the engine process ends, so the shell can tell the
// difference between a session that ended and an engine that died.
//
// Without this the shell has no way to see the engine crash: the events stop
// arriving, the readings stop moving, and the panel keeps showing the last
// number it was given as though audio were still flowing.
func (c *Client) Wait() <-chan struct{} {
	return c.exitedCh
}

// Running reports whether the process is still alive.
func (c *Client) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cmd != nil && !c.exited.Load()
}

// Session returns the id this client stamps on its commands.
func (c *Client) Session() string { return c.session }

func (c *Client) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	// The engine caps a command at 64 KiB; give events the same headroom.
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // a malformed line must not kill the stream
		}
		ev.Raw = append(json.RawMessage(nil), line...)
		if c.handler != nil {
			c.handler(ev)
		}
	}
}

func drain(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		// Engine diagnostics are not protocol; drop them.
	}
}

// Send writes one command and returns the id it used.
func (c *Client) Send(command string, params any) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd == nil || c.exited.Load() {
		return "", ErrNotRunning
	}
	id := fmt.Sprintf("c%d", c.seq.Add(1))
	payload := map[string]any{
		"version":    ProtocolVersion,
		"id":         id,
		"session_id": c.session,
		"command":    command,
	}
	if params != nil {
		payload["params"] = params
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	if _, err := c.stdin.Write(append(line, '\n')); err != nil {
		return "", fmt.Errorf("write %s: %w", command, err)
	}
	return id, nil
}

// Hello asks the engine what it can do.
func (c *Client) Hello() (string, error) { return c.Send(CmdHello, nil) }

// StartStream begins live loopback playback to the given peers.
func (c *Client) StartStream(peers []Peer, opts StreamOptions) (string, error) {
	params := map[string]any{
		"peers":       peers,
		"source":      "loopback",
		"duration_ms": 0,
		"latency_ms":  opts.LatencyMs,
		"gain":        opts.Gain,
		"sample_rate": opts.SampleRate,
		"timing":      opts.Timing,
	}
	if opts.CaptureEndpoint != "" {
		params["capture_endpoint"] = opts.CaptureEndpoint
	}
	if opts.Equalizer != nil {
		params["equalizer"] = opts.Equalizer
	}
	return c.Send(CmdStart, params)
}

// Stop ends the matching session.
func (c *Client) Stop() (string, error) { return c.Send(CmdStop, nil) }

// SetGain changes the stream gain.
func (c *Client) SetGain(gain float64) (string, error) {
	return c.Send(CmdSetGain, map[string]any{"gain": gain})
}

// SetDeviceVolume sets the receiver's own volume, 0 to 1.
//
// The engine takes a percentage and a strictly increasing sequence, which it
// uses to discard a command that a later one has already replaced: dragging a
// slider produces many of these, and only the last one matters.
func (c *Client) SetDeviceVolume(volume float64) (string, error) {
	percent := int(math.Round(volume * 100))
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return c.Send(CmdSetDeviceVolume, map[string]any{
		"volume":   percent,
		"sequence": c.seq.Add(1),
	})
}

// SetEqualizer changes the equalizer on a live stream.
//
// The engine takes a strictly increasing sequence and discards a command a later
// one has already replaced, which is what makes dragging a band jump straight to
// the value the hand let go of.
func (c *Client) SetEqualizer(eq any) (string, error) {
	return c.Send(CmdSetEqualizer, map[string]any{
		"equalizer": eq,
		"sequence":  c.seq.Add(1),
	})
}

// Pair starts pairing with a peer, which may answer with pin_required.
func (c *Client) Pair(peer Peer) (string, error) {
	return c.Send(CmdPair, map[string]any{"peer": peer})
}

// PairPin completes pairing with the PIN the user typed.
func (c *Client) PairPin(pin string) (string, error) {
	return c.Send(CmdPairPin, map[string]any{"pin": pin})
}

// Close stops the engine by closing its stdin, which cancels owned work.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return nil
	}
	err := c.stdin.Close()
	c.stdin = nil
	return err
}

// Peer is one receiver.
type Peer struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// StreamOptions are the start parameters.
type StreamOptions struct {
	LatencyMs       int
	Gain            float64
	SampleRate      int
	Timing          string
	CaptureEndpoint string
	Equalizer       any
}

// DefaultStreamOptions returns the values the engine expects.
func DefaultStreamOptions() StreamOptions {
	return StreamOptions{LatencyMs: 500, Gain: 1.0, SampleRate: 44100, Timing: "ptp"}
}

func newSessionID() string {
	return fmt.Sprintf("ui-%d", time.Now().UnixNano())
}
