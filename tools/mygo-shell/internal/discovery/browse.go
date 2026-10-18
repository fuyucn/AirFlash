package discovery

import (
	"context"
	"io"
	"log"
	"net"
	"sort"
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
func Browse(ctx context.Context, onInterface int, onChange func([]Receiver), onError func(error)) error {
	entries := make(chan *mdns.ServiceEntry, 64)

	// One adapter, or the system's own choice when none is named: a machine with
	// a VPN up can find its receivers only on one of its adapters, and which one
	// is not something a scan on all of them can answer.
	params := &mdns.QueryParam{
		Service:     ServiceAirPlay,
		Domain:      "local",
		Timeout:     3 * time.Second,
		Entries:     entries,
		DisableIPv6: true,
		Logger:      quiet,
	}
	if iface, err := net.InterfaceByIndex(onInterface); err == nil {
		params.Interface = iface
	}

	// One collector per service type feeds the same set of records.
	var mu sync.Mutex
	seen := map[string]Record{}
	// lastSeen is when each record was last announced. mDNS has no "a record
	// went away" event, so this is how a device that stopped answering is told
	// apart from one that is still there.
	lastSeen := map[string]time.Time{}
	var settle *time.Timer

	publish := func() {
		mu.Lock()
		list := make([]Record, 0, len(seen))
		for _, r := range seen {
			list = append(list, r)
		}
		seenAt := make(map[string]time.Time, len(lastSeen))
		for k, v := range lastSeen {
			seenAt[k] = v
		}
		mu.Unlock()

		folded := Fold(list)
		markPresence(folded, seenAt, time.Now())
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
				// Not an AirPlay service, so not a receiver: a Spotify Connect
				// speaker or a smart-home hub that answered on the same channel.
				// And a record with no address cannot be reached at all. Both
				// are dropped for the same reason: neither is something to list.
				if rec.Type == "" || rec.Address == "" {
					continue
				}
				mu.Lock()
				key := BrowseKey(rec)
				seen[key] = rec
				lastSeen[key] = time.Now()
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
// reports the instance name, which carries the type as its suffix, or "" when it
// carries neither.
//
// Returning "" rather than defaulting to AirPlay is the whole point: a browse for
// _airplay._tcp can pick up records for other services - a Spotify Connect
// speaker, an Aqara hub, a HomeKit accessory - because they answer on the same
// multicast channel. None of those is a receiver this app can play to. Marking
// them AirPlay folds them into the list as devices with a Play button that
// connects to nothing, so they are dropped instead.
func serviceTypeOf(name string) string {
	switch {
	case strings.Contains(name, ServiceAirPlay):
		return ServiceAirPlay
	case strings.Contains(name, ServiceRAOP):
		return ServiceRAOP
	}
	return ""
}

// PresenceTimeout is how long a receiver may be silent before it is reported as
// gone.
//
// mDNS answers are on demand, not a heartbeat: the browse re-queries every
// second and a device that is present answers, so several missed rounds means
// the device has gone rather than that it was busy. It is generous enough to
// survive a busy Wi-Fi channel, because reporting a speaker that is really
// still there as gone is worse than being a second late.
const PresenceTimeout = 12 * time.Second

// markPresence sets each receiver's Online flag from when its records were last
// announced.
//
// A receiver is present when any of the records it was folded from was
// announced within PresenceTimeout. A device answers on whichever service type
// answers first, so requiring every record would call a speaker gone because
// one of its two adverts missed a round.
//
// The keys are the ones the fold recorded, rather than rebuilt: the browse's key
// carries the instance name, so a rebuild from the address alone produces
// "_airplay._tcp||10.0.10.31" and matches nothing, which would make every
// receiver read as offline.
func markPresence(receivers []Receiver, seenAt map[string]time.Time, now time.Time) {
	for i := range receivers {
		receivers[i].Online = false
		for _, key := range receivers[i].RecordKeys {
			if at, ok := seenAt[key]; ok && now.Sub(at) < PresenceTimeout {
				receivers[i].Online = true
				break
			}
		}
	}
}

// Interface is one network adapter the browse could use.
type Interface struct {
	// Index is the system's own id for it, which is what an interface is
	// identified by: a name can be duplicated across a VPN's virtual adapters.
	Index int
	Name  string
	// Addresses are the IPv4 addresses it holds, which is what says whether it
	// could reach a receiver at all.
	Addresses []string
}

// Interfaces lists the adapters a browse could use, in a stable order.
//
// An adapter with no IPv4 address is left out: it cannot carry mDNS to a
// receiver, and offering it would be offering a choice that does nothing. A
// loopback is left out for the same reason - the machine's own traffic is not
// where a speaker advertises itself.
func Interfaces() []Interface {
	adapters, err := net.Interfaces()
	if err != nil {
		return nil
	}
	// An adapter with no addresses of its own is not offered: the flag that
	// matters is whether it can carry a browse, and one that cannot is a choice
	// that does nothing. Addrs returns an error alongside, which is why the
	// function reads both and keeps the addresses.
	return interfacesOn(adapters, func(a net.Interface) []net.Addr {
		addrs, _ := a.Addrs()
		return addrs
	})
}

// interfacesOn is the listing over a given set of adapters.
//
// The addresses are read through a function rather than by calling the
// adapter's own, so a test can name a network without holding one: the rule
// worth asserting is which adapters are offered, and that is decided from flags
// that a test can set on a struct it built itself.
func interfacesOn(adapters []net.Interface, addrsOf func(net.Interface) []net.Addr) []Interface {
	found := []Interface{}
	for _, a := range adapters {
		if a.Flags&net.FlagUp == 0 || a.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses := []string{}
		for _, addr := range addrsOf(a) {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if v4 := ip.To4(); v4 != nil {
				addresses = append(addresses, v4.String())
			}
		}
		if len(addresses) == 0 {
			continue
		}
		found = append(found, Interface{Index: a.Index, Name: a.Name, Addresses: addresses})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Index < found[j].Index })
	return found
}
