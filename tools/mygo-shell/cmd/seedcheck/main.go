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

	view := appui.New(appui.Deps{
		State:     engine.NewState(),
		Settings:  cfg,
		Life:      shell.New(),
		Receivers: func() []discovery.Receiver { return fake },
		OnPlay:    func(r discovery.Receiver) { fmt.Fprintf(os.Stderr, "play %s\n", r.Name) },
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
