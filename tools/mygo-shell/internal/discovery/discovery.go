// Package discovery finds AirPlay receivers on the local network.
//
// It browses the same two service types the C# host does, _airplay._tcp and
// _raop._tcp, and folds what it finds into receivers. A HomePod answers on
// both types, and a stereo pair answers twice more, so the folding is where
// the work is: records are matched by device id, and the halves of a pair by
// their stereo id (tsid).
package discovery

import (
	"sort"
	"strconv"
	"strings"
)

// Service types AirPlay receivers advertise.
const (
	ServiceAirPlay = "_airplay._tcp"
	ServiceRAOP    = "_raop._tcp"
	// DefaultPort is the control port AirPlay 2 uses.
	DefaultPort = 7000
)

// Record is one advertised service, before folding.
type Record struct {
	Instance string
	Type     string
	Host     string
	Address  string
	Port     int
	TXT      map[string]string
}

// Receiver is one device, or one stereo pair, the user can play to.
type Receiver struct {
	ID       string
	Name     string
	Model    string
	Address  string
	Port     int
	StereoID string
	Members  []Record
	Codecs   []int
	Online   bool
	// RecordKeys are the browse keys of the records this receiver was folded
	// from, which is how its presence is later judged. The keys are kept
	// rather than rebuilt, because a rebuild has to guess at the instance name
	// and gets it wrong: "_airplay._tcp|" does not match
	// "_airplay._tcp|Living Room._airplay._tcp.local|".
	RecordKeys []string
}

// IsGroup reports whether this receiver is a stereo pair.
func (r Receiver) IsGroup() bool {
	return len(r.Members) > 0 || strings.HasPrefix(r.ID, "stereo:")
}

// Complete reports whether a stereo pair has both halves. A pair with one half
// cannot be played to.
func (r Receiver) Complete() bool { return !r.IsGroup() || len(r.Members) == 2 }

// Host returns the address to connect to.
func (r Receiver) Host() string { return r.Address }

// Peers returns the endpoints to send to: both halves of a pair, else itself.
func (r Receiver) Peers() []Record {
	if r.IsGroup() && len(r.Members) > 0 {
		return r.Members
	}
	return []Record{{Host: r.Address, Address: r.Address, Port: r.Port}}
}

// device is one physical device's records, from both service types.
type device struct {
	id      string
	records []Record
}

// Fold turns advertised records into receivers. It is pure, so the folding
// can be tested without a network.
func Fold(records []Record) []Receiver {
	byID := map[string]*device{}
	var order []string

	for _, rec := range ordered(records) {
		id := identity(rec)
		if id == "" {
			id = endpoint(rec)
		}
		d, ok := byID[id]
		if !ok {
			d = &device{id: id}
			byID[id] = d
			order = append(order, id)
		}
		d.records = append(d.records, rec)
	}

	pairs := map[string][]*device{}
	var singles []*device

	for _, id := range order {
		d := byID[id]
		stereo := field(d.records, "tsid")
		if stereo == "" {
			singles = append(singles, d)
			continue
		}
		pairs[stereo] = append(pairs[stereo], d)
	}

	var out []Receiver
	for _, d := range singles {
		out = append(out, fromDevice(d.id, d.records))
	}

	var stereoIDs []string
	for id := range pairs {
		stereoIDs = append(stereoIDs, id)
	}
	sort.Strings(stereoIDs)

	for _, stereo := range stereoIDs {
		devices := pairs[stereo]
		rec := fromDevice("stereo:"+stereo, flatten(devices))
		rec.ID = "stereo:" + stereo
		rec.StereoID = stereo

		for _, d := range devices {
			one := fromDevice(d.id, d.records)
			rec.Members = append(rec.Members, Record{
				Instance: one.ID,
				Host:     one.Address,
				Address:  one.Address,
				Port:     one.Port,
				TXT:      firstTXT(d.records),
			})
		}
		// The leader goes first, so playback starts from it.
		sort.SliceStable(rec.Members, func(i, j int) bool {
			return isLeader(devices, rec.Members[i].Address) && !isLeader(devices, rec.Members[j].Address)
		})
		if len(devices) == 1 {
			rec.Name = strings.TrimSpace(rec.Name + " (pair incomplete)")
		}
		out = append(out, rec)
	}

	// A complete pair is the better choice, so it sorts before single devices.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsGroup() != out[j].IsGroup() {
			return out[i].IsGroup()
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func fromDevice(id string, records []Record) Receiver {
	best := pickControl(records)
	name := displayName(records)
	model := field(records, "model")
	if model == "" {
		model = field(records, "am")
	}
	r := Receiver{
		ID:      id,
		Name:    name,
		Model:   model,
		Address: best.Address,
		Port:    best.Port,
		Online:  true,
	}
	for _, rec := range records {
		r.RecordKeys = append(r.RecordKeys, BrowseKey(rec))
	}
	for _, rec := range records {
		if strings.HasPrefix(rec.Type, ServiceRAOP) {
			r.Codecs = parseCodecs(rec.TXT["cn"])
			break
		}
	}
	if r.Name == "" {
		r.Name = r.Address
	}
	if r.Port == 0 {
		r.Port = DefaultPort
	}
	return r
}

// pickControl chooses the record to send to. AirPlay 2's control port is the
// one to use, so an _airplay record wins over _raop.
func pickControl(records []Record) Record {
	for _, rec := range records {
		if strings.HasPrefix(rec.Type, ServiceAirPlay) && rec.Port > 0 {
			return rec
		}
	}
	for _, rec := range records {
		if rec.Port > 0 {
			return rec
		}
	}
	if len(records) > 0 {
		return records[0]
	}
	return Record{Port: DefaultPort}
}

// displayName prefers the AirPlay record's name, which is what the user set,
// over the RAOP instance name, which carries a MAC address.
func displayName(records []Record) string {
	for _, rec := range records {
		if strings.HasPrefix(rec.Type, ServiceAirPlay) && rec.Instance != "" {
			return trimInstance(rec.Instance)
		}
	}
	for _, rec := range records {
		if rec.Instance != "" {
			return trimInstance(rec.Instance)
		}
	}
	return ""
}

// trimInstance removes the MAC prefix and the service suffix mDNS appends.
func trimInstance(name string) string {
	if i := strings.Index(name, "@"); i > 0 {
		name = name[i+1:]
	}
	for _, suffix := range []string{"._airplay._tcp.local", "._raop._tcp.local", "._tcp.local", ".local"} {
		name = strings.TrimSuffix(name, suffix)
	}
	return strings.TrimSpace(name)
}

// identity is the device id, which is what tells two advertisements of one
// device apart from two devices.
func identity(rec Record) string {
	if id := strings.TrimSpace(rec.TXT["deviceid"]); id != "" {
		return Normalize(id)
	}
	if strings.HasPrefix(rec.Type, ServiceRAOP) {
		if at := strings.Index(rec.Instance, "@"); at > 0 {
			return Normalize(rec.Instance[:at])
		}
	}
	return ""
}

// Normalize reduces a device identity to the one form a device is known by.
//
// A HomePod does not advertise its MAC the same way on both service types. From
// the log of a real network:
//
//	_airplay:  deviceid=42:FB:06:B5:10:BD
//	_raop:     deviceid=42FB06B510BD
//
// Same address, same device, two identities. Comparing the raw text would fold
// one speaker into two receivers of the same name, so the identity is reduced
// to the twelve hex digits of a MAC, lower case and without separators, and
// anything else is left alone.
//
// This mirrors the desktop app's ReceiverIdentity.Normalize, so the shell and
// the engine agree on which records are the same device.
func Normalize(id string) string {
	text := strings.TrimSpace(id)
	mac := strings.NewReplacer(":", "", "-", "").Replace(text)
	if len(mac) != 12 || !isHex(mac) {
		return text
	}
	return strings.ToLower(mac)
}

// isHex reports whether every byte is a hex digit, which is what makes a
// twelve-character string a MAC rather than an arbitrary identity.
func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func endpoint(rec Record) string {
	return rec.Address + ":" + strconv.Itoa(rec.Port)
}

// BrowseKey is the key one record is stored under while browsing. Presence is
// judged by it, so the browse and the folding must both use this and nothing
// else.
func BrowseKey(rec Record) string {
	return rec.Type + "|" + rec.Instance + "|" + rec.Address
}

func field(records []Record, key string) string {
	for _, rec := range records {
		if v := strings.TrimSpace(rec.TXT[key]); v != "" {
			return v
		}
	}
	return ""
}

func firstTXT(records []Record) map[string]string {
	if len(records) == 0 {
		return nil
	}
	return records[0].TXT
}

func isLeader(devices []*device, host string) bool {
	for _, d := range devices {
		for _, rec := range d.records {
			if rec.Address == host && strings.TrimSpace(rec.TXT["igl"]) == "1" {
				return true
			}
		}
	}
	return false
}

func flatten(devices []*device) []Record {
	var out []Record
	for _, d := range devices {
		out = append(out, d.records...)
	}
	return out
}

// ordered puts AirPlay records first, then sorts deterministically, so folding
// gives the same result for the same set of records in any order.
func ordered(records []Record) []Record {
	out := append([]Record(nil), records...)
	sort.SliceStable(out, func(i, j int) bool {
		ai := strings.HasPrefix(out[i].Type, ServiceAirPlay)
		aj := strings.HasPrefix(out[j].Type, ServiceAirPlay)
		if ai != aj {
			return ai
		}
		if out[i].Instance != out[j].Instance {
			return out[i].Instance < out[j].Instance
		}
		if out[i].Address != out[j].Address {
			return out[i].Address < out[j].Address
		}
		return out[i].Port < out[j].Port
	})
	return out
}

func parseCodecs(s string) []int {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []int
	for _, part := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			out = append(out, n)
		}
	}
	return out
}
