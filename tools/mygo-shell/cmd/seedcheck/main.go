package main

// Command seedcheck draws the shell with a synthetic stereo pair, so the
// Receivers page can be looked at without a HomePod on the network. It is a
// development aid, not part of the app.
import (
	"fmt"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/discovery"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/engine"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/settings"
	"github.com/fuyucn/airflash/tools/mygo-shell/internal/shell"
	appui "github.com/fuyucn/airflash/tools/mygo-shell/ui"
)

func main() {
	cfg := settings.Open("/tmp/seed-settings.json")

	fake := discovery.Fold([]discovery.Record{
		{Instance: "Living Room", Type: discovery.ServiceAirPlay, Address: "10.0.10.31", Port: 7000,
			TXT: map[string]string{"deviceid": "AA:BB:CC:DD:EE:FF", "tsid": "PAIR1", "model": "HomePod1,1"}},
		{Instance: "AA:BB:CC:DD:EE:FF@Living Room", Type: discovery.ServiceRAOP, Address: "10.0.10.31", Port: 50123,
			TXT: map[string]string{"deviceid": "AA:BB:CC:DD:EE:FF", "tsid": "PAIR1", "cn": "0,1", "am": "HomePod1,1"}},
		{Instance: "Living Room", Type: discovery.ServiceAirPlay, Address: "10.0.10.32", Port: 7000,
			TXT: map[string]string{"deviceid": "11:22:33:44:55:66", "tsid": "PAIR1", "model": "HomePod1,1", "igl": "1"}},
		{Instance: "11:22:33:44:55:66@Living Room", Type: discovery.ServiceRAOP, Address: "10.0.10.32", Port: 50124,
			TXT: map[string]string{"deviceid": "11:22:33:44:55:66", "tsid": "PAIR1", "cn": "0,1", "am": "HomePod1,1", "igl": "1"}},
	})
	fmt.Fprintf(os.Stderr, "seeded %d receiver(s)\n", len(fake))

	// Feed the view a running session with a latency series that jitters the
	// way a real network does, so the chart can be looked at without a HomePod.
	st := engine.NewState()
	st.Apply(engine.Event{Event: "streaming", Raw: []byte(
		`{"event":"streaming","requested_latency_ms":500,"sample_rate":44100,"clock_id":"clk","members":1}`)})

	seed := 7
	for i := 0; i < 90; i++ {
		seed = (seed*1103515245 + 12345) & 0x7fffffff
		jitter := float64(seed%90) - 40 // receiver buffer wobble
		rtt := 18.0 + float64(seed%55)  // the network's round trip
		buffer := 470.0 + jitter
		body := fmt.Sprintf(`{"event":"transport_metrics","session_uptime_ms":%d,
		  "sender_late_recoveries":0,"skipped_packets":0,"max_send_lateness_us":900,
		  "members":[{"host":"10.0.10.31",
		    "media":{"packets_sent":%d,"media_send_dropped":0,"retransmit_requests":%d,
		      "retransmits_sent":%d,"retransmit_missing":0,"retransmit_expired":0,
		      "retransmit_queue_drops":0,"receiver_latency_ms":%.1f,
		      "receiver_latency_estimated":false,"media_backlog_packets":2,
		      "media_backlog_limit_packets":47,"audio_send_buffer_bytes":65536},
		    "health":{"feedback_rtt_ms":%.1f,"feedback_delayed":false}}]}`,
			i*1000, i*125, i/10, i/10, buffer, rtt)
		st.Apply(engine.Event{Event: "transport_metrics", Raw: []byte(body)})
	}

	view := appui.New(appui.Deps{
		State:     st,
		Settings:  cfg,
		Life:      shell.New(),
		Receivers: func() []discovery.Receiver { return fake },
		OnPlay:    func(r discovery.Receiver) { fmt.Fprintf(os.Stderr, "play %s\n", r.Name) },
		AdaptedMs: func() int { return 500 },
	})

	if len(os.Args) > 1 {
		view.SetPage(os.Args[1])
	}

	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title: "AirFlash", Width: 980, Height: 680,
			BackgroundColor: "light-dark(#F2F2F7, #1C1C1E)",
			Content:         ui.View(view.View),
		})
	})
	if err := mygo.App.Run(); err != nil {
		panic(err)
	}
}
