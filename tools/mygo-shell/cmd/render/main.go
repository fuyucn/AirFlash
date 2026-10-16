package main

// Command render draws the shell's surfaces to PNG files, without a window and
// without a HomePod. It is a development aid: looking at the running app is the
// only way to judge a layout, and this is the cheapest way to get a picture.
//
//	go run ./cmd/render /tmp/shots
import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/discovery"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/i18n"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/settings"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/shell"
	appui "github.com/fuyucn/airflash/tools/mygo-shell/ui"
)

func main() {
	out := "/tmp/shots"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		panic(err)
	}

	cfg := settings.Open(filepath.Join(out, "settings.json"))

	// The same shape the user's network has: a lone HomePod, a Mac, and an
	// Apple TV. A device advertises tsid only when it is half of a pair, so a
	// standalone one has none - which is what the log from their machine shows.
	receivers := discovery.Fold([]discovery.Record{
		{Instance: "Living Room", Type: discovery.ServiceAirPlay, Address: "10.0.10.31", Port: 7000,
			TXT: map[string]string{"deviceid": "42FB06B510BD", "model": "AudioAccessory1,1"}},
		{Instance: "42FB06B510BD@Living Room", Type: discovery.ServiceRAOP, Address: "10.0.10.31", Port: 7000,
			TXT: map[string]string{"deviceid": "42FB06B510BD", "cn": "0,1", "am": "AudioAccessory1,1"}},
		{Instance: "yfu's MacBook Pro", Type: discovery.ServiceAirPlay, Address: "10.0.10.120", Port: 7000,
			TXT: map[string]string{"deviceid": "7AC8C89C0094", "model": "Mac17,2"}},
		{Instance: "7AC8C89C0094@yfu's MacBook Pro", Type: discovery.ServiceRAOP, Address: "10.0.10.120", Port: 7000,
			TXT: map[string]string{"deviceid": "7AC8C89C0094", "cn": "0,1", "am": "Mac17,2"}},
		{Instance: "Living Room (2)", Type: discovery.ServiceAirPlay, Address: "10.0.10.30", Port: 7000,
			TXT: map[string]string{"deviceid": "7E5B712836F4", "model": "AppleTV14,1"}},
		{Instance: "7E5B712836F4@Living Room (2)", Type: discovery.ServiceRAOP, Address: "10.0.10.30", Port: 7000,
			TXT: map[string]string{"deviceid": "7E5B712836F4", "cn": "0,1", "am": "AppleTV14,1"}},
	})
	// One of them has stopped answering, which the panel has to show rather
	// than offer a Play that would time out.
	receivers[len(receivers)-1].Online = false
	fmt.Fprintf(os.Stderr, "receivers: %d\n", len(receivers))

	for _, lang := range []string{"en", "zh"} {
		for _, state := range []struct {
			name    string
			st      *engine.State
			playing bool
		}{
			{"idle", idle(), false},
			{"streaming", streaming(), true},
		} {
			// The panel learns which receiver is playing from the app, not from the
			// engine, so the shot has to say it the way the app would.
			playingID := ""
			if state.playing && len(receivers) > 0 {
				playingID = receivers[0].ID
			}
			// Both languages, so the tables can be looked at: a missing key shows as
			// English, which is what a translation that falls behind looks like.
			cfg.Update(func(s *settings.Settings) { s.Language = lang })
			words := i18n.For(i18n.Language(lang))

			panel := appui.NewPanel(appui.Deps{
				Words:     words,
				State:     state.st,
				Settings:  cfg,
				Life:      shell.New(),
				Receivers: func() []discovery.Receiver { return receivers },
				PlayingReceiver: func() *discovery.Receiver {
					if playingID == "" {
						return nil
					}
					for _, r := range receivers {
						if r.ID == playingID {
							found := r
							return &found
						}
					}
					return nil
				},
				OnPlay: func(r discovery.Receiver) {},
				OnStop: func() {},
				OnQuit: func() {},
			})
			// The panel is a flyout: the platform draws the surface, so the shot is
			// only as tall as the content asks for.
			write(filepath.Join(out, fmt.Sprintf("panel-%s-%s.png", lang, state.name)), panel.View,
				appui.PanelWidth(), panel.Height(), 2)

			view := appui.New(appui.Deps{
				Words:     words,
				State:     state.st,
				Settings:  cfg,
				Life:      shell.New(),
				Receivers: func() []discovery.Receiver { return receivers },
				OnPlay:    func(r discovery.Receiver) {},
				AdaptedMs: func() int { return 500 },
			})
			for _, page := range []string{"stats", "receivers", "settings"} {
				view.SetPage(page)
				// A tall window, so the whole page is in the shot rather than only
				// what fits in a 640 DIP viewport: the diagnostics and capture
				// sections sit below the fold.
				height := 640
				if page == "stats" {
					height = 1180
				}
				write(filepath.Join(out, fmt.Sprintf("window-%s-%s-%s.png", lang, state.name, page)), view.View, 880, height, 2)
			}
		}
	}
}

// idle is the app before anything is playing.
func idle() *engine.State { return engine.NewState() }

// streaming is a session with a few seconds of readings, so the chart and the
// counters have something to say.
func streaming() *engine.State {
	st := engine.NewState()
	st.Apply(engine.Event{Event: "streaming", Raw: []byte(
		`{"event":"streaming","requested_latency_ms":500,"sample_rate":44100,"clock_id":"clk","members":1}`)})
	seed := 7
	rnd := func() float64 {
		seed = (seed*1103515245 + 12345) & 0x7fffffff
		return float64(seed)/float64(0x7fffffff) - 0.5
	}
	walk := 0.0
	// The counters are cumulative, as the engine sends them: a session that
	// stuttered twice, with the ping rising around the gaps.
	skipped, recoveries, expired := 0, 0, 0
	for i := 0; i < 90; i++ {
		walk = walk*0.85 + rnd()*14
		buffer := 480 + walk
		rtt := 22 + rnd()*9
		if i == 34 || i == 61 {
			skipped += 6
			recoveries++
			expired += 2
			rtt += 55
		}
		body := fmt.Sprintf(`{"event":"transport_metrics","session_uptime_ms":%d,
		  "sender_late_recoveries":%d,"skipped_packets":%d,"max_send_lateness_us":900,
		  "members":[{"host":"10.0.10.31",
		    "media":{"packets_sent":%d,"media_send_dropped":0,"retransmit_requests":%d,
		      "retransmits_sent":%d,"retransmit_missing":0,"retransmit_expired":%d,
		      "retransmit_queue_drops":0,"receiver_latency_ms":%.1f,
		      "receiver_latency_estimated":false,"media_backlog_packets":2,
		      "media_backlog_limit_packets":47,"audio_send_buffer_bytes":65536},
		    "health":{"feedback_rtt_ms":%.1f,"feedback_delayed":false}}]}`,
			i*1000, recoveries, skipped, i*125, i/10, i/10, expired, buffer, rtt)
		st.Apply(engine.Event{Event: "transport_metrics", Raw: []byte(body)})
	}

	// The PC then stops making sound: the capture stamp stands still, and the
	// network numbers above stay perfect throughout.
	for i := 0; i < 12; i++ {
		st.Apply(engine.Event{Event: "capture_metrics", Raw: []byte(
			`{"event":"capture_metrics","metrics":{"dropped_frames":0,
			 "underrun_packets":0,"capture_frames":4410,"max_queue_age_ms":3.5,
			 "capture_to_send_p95_ms":9.2,"last_audio_qpc_ns":999999,
			 "input_rate":44100}}`)})
	}
	return st
}

// write renders one frame to a PNG at the given size and scale.
func write(path string, view func(*ui.Context), width, height int, scale float32) {
	img := ui.Render(view, width, height, scale)
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", path)
}
