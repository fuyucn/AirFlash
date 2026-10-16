package discovery

import (
	"context"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/mdns"
)

// quiet is the logger the mDNS library writes its own diagnostics to. It is
// chatty about IPv6 and about retries that are normal on a network without it,
// and those lines are not the app's to show. Real failures come back as errors
// from Browse instead.
var quiet = log.New(io.Discard, "", 0)

// Browse listens for AirPlay receivers and reports the folded list whenever it
// changes. It runs until ctx is cancelled.
//
// mDNS has no "the list is complete" moment, so results arrive over time and
// the callback is called as they settle. A short settle window keeps the list
// from flickering while a device's records arrive one at a time.
func Browse(ctx context.Context, onChange func([]Receiver), onError func(error)) error {
	entries := make(chan *mdns.ServiceEntry, 64)

	params := &mdns.QueryParam{
		Service:     ServiceAirPlay,
		Domain:      "local",
		Timeout:     3 * time.Second,
		Entries:     entries,
		DisableIPv6: true,
		Logger:      quiet,
	}

	// One collector per service type feeds the same set of records.
	var mu sync.Mutex
	seen := map[string]Record{}
	var settle *time.Timer

	publish := func() {
		mu.Lock()
		list := make([]Record, 0, len(seen))
		for _, r := range seen {
			list = append(list, r)
		}
		mu.Unlock()

		folded := Fold(list)
		if onChange != nil {
			onChange(folded)
		}
	}

	// Debounce: mDNS answers arrive in bursts, and folding on every packet
	// would redraw the list several times per device.
	schedule := func() {
		mu.Lock()
		defer mu.Unlock()
		if settle != nil {
			settle.Stop()
		}
		settle = time.AfterFunc(400*time.Millisecond, publish)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case entry, ok := <-entries:
				if !ok {
					return
				}
				rec := recordFrom(entry)
				if rec.Address == "" {
					continue
				}
				mu.Lock()
				seen[rec.Type+"|"+rec.Instance+"|"+rec.Address] = rec
				mu.Unlock()
				schedule()
			}
		}
	}()

	for _, service := range []string{ServiceAirPlay, ServiceRAOP} {
		svc := service
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				p := *params
				p.Service = svc
				if err := mdns.QueryContext(ctx, &p); err != nil {
					if ctx.Err() != nil {
						return
					}
					if onError != nil {
						onError(err)
					}
					// mDNS is best effort: retry rather than give up.
					select {
					case <-ctx.Done():
						return
					case <-time.After(2 * time.Second):
					}
					continue
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Second):
				}
			}
		}()
	}

	<-ctx.Done()
	mu.Lock()
	if settle != nil {
		settle.Stop()
	}
	mu.Unlock()
	close(entries)
	wg.Wait()
	return ctx.Err()
}

// recordFrom converts one mDNS answer into a record, reading the TXT fields
// into a map so the folding can look them up by name.
func recordFrom(entry *mdns.ServiceEntry) Record {
	if entry == nil {
		return Record{}
	}
	address := ""
	if entry.AddrV4 != nil {
		address = entry.AddrV4.String()
	}
	if address == "" {
		address = strings.TrimSuffix(entry.Host, ".")
	}
	txt := map[string]string{}
	for _, f := range entry.InfoFields {
		if eq := strings.Index(f, "="); eq > 0 {
			txt[strings.ToLower(f[:eq])] = f[eq+1:]
		} else if f != "" {
			txt[strings.ToLower(f)] = ""
		}
	}
	return Record{
		Instance: entry.Name,
		Type:     serviceTypeOf(entry.Name),
		Host:     entry.Host,
		Address:  address,
		Port:     entry.Port,
		TXT:      txt,
	}
}

// serviceTypeOf recovers which service a record came from. hashicorp/mdns
// reports the instance name, which carries the type as its suffix.
func serviceTypeOf(name string) string {
	switch {
	case strings.Contains(name, ServiceAirPlay):
		return ServiceAirPlay
	case strings.Contains(name, ServiceRAOP):
		return ServiceRAOP
	}
	return ServiceAirPlay
}
