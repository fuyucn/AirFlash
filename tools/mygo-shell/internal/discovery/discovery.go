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
	// byAddress is the id each address was first folded under, which is what tells
	// two records of one device apart from records of two devices that happen to
	// share an id. It is a map rather than a scan of the ids so far, because a
	// browse holds a record per service type per device and the lookup happens
	// once per record.
	byAddress := map[string]string{}
	var order []string

	for _, rec := range ordered(records) {
		id := identity(rec)
		if id == "" {
			id = endpoint(rec)
		}
		// One address is one device. Two records that disagree about the device
		// id - one carrying it and one not, which is what a browse that catches
		// only some of a device's TXT records produces - are still one speaker,
		// and folding them apart leaves the same name listed twice with a Play
		// for each. The address is the one thing every record of a device agrees
		// on, so it decides when the ids do not.
		//
		// The stronger of the two ids wins. The id is the key every per-receiver
		// setting is stored under, so a speaker whose TXT arrives after the
		// record that only located it must not become a different speaker to the
		// settings: its address-keyed entry is upgraded to the name that has just
		// arrived.
		prev, seen := byAddress[rec.Address]
		switch {
		case !seen:
			byAddress[rec.Address] = id
		case prev == id:
			// Already folded under this id.
		case weaker(prev, id):
			// A real id arrived for a device that was only located before.
			if existing, have := byID[prev]; have {
				existing.id = id
				byID[id] = existing
				delete(byID, prev)
				for i, o := range order {
					if o == prev {
						order[i] = id
						break
					}
				}
			}
			byAddress[rec.Address] = id
		case !weaker(id, prev):
			// Two real ids for one address: the first stays, as before.
			id = prev
		}
		if _, ok := byID[id]; !ok {
			byID[id] = &device{id: id}
			order = append(order, id)
		}
		byID[id].records = append(byID[id].records, rec)
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

// trimInstance reduces an mDNS instance name to the name the user gave it.
//
// An instance name as a browse reports it is not the name: it carries a MAC
// prefix on RAOP ("42FB06B510BD@Living Room"), the service type it was
// advertised as ("._airplay._tcp"), the domain, and a trailing dot that the
// DNS layer leaves on the fully qualified name. All of that is protocol, not
// something the person typed into the Home app.
//
// The trailing dot is the one that is easy to miss: "Living Room._airplay._tcp.local"
// is not a suffix of "Living Room._airplay._tcp.local.", so a naive trim leaves
// it there and the panel shows a name ending in a dot.
func trimInstance(name string) string {
	// The trailing dot first, or nothing below matches a fully qualified name.
	name = strings.TrimSuffix(name, ".")
	if i := strings.Index(name, "@"); i > 0 {
		name = name[i+1:]
	}
	for _, suffix := range []string{"._airplay._tcp.local", "._raop._tcp.local", "._tcp.local", ".local"} {
		name = strings.TrimSuffix(name, suffix)
	}
	name = strings.TrimSuffix(name, ".")
	// DNS-SD presentation escaping last. A device the user named "Living Room"
	// or "Living Room (2)" is advertised on the wire with a backslash before
	// every space, dot and parenthesis, and miekg/dns keeps that escaping in the
	// name it hands back. The backslashes are protocol, not part of the name, so
	// they are dropped and the character after each one kept as itself.
	return strings.TrimSpace(unescapeDNSSD(name))
}

// unescapeDNSSD removes DNS-SD presentation escaping: a backslash followed by a
// character is that character literally. A backslash before a "." keeps the dot
// from being read as a label separator, and one before a space or parenthesis is
// how those are carried at all, so all of them collapse to the plain character.
//
// A decimal escape such as "\032" (a space by its ASCII code) is also valid
// DNS-SD and is decoded the same way.
func unescapeDNSSD(name string) string {
	if !strings.Contains(name, "\\") {
		return name
	}
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		if name[i] != '\\' || i+1 >= len(name) {
			out = append(out, name[i])
			continue
		}
		// A three-digit decimal escape is one character.
		if i+3 < len(name) && isDigit(name[i+1]) && isDigit(name[i+2]) && isDigit(name[i+3]) {
			if v, ok := parseDecimal(name[i+1 : i+4]); ok {
				out = append(out, byte(v))
				i += 3
				continue
			}
		}
		// Otherwise the escaped character stands for itself.
		i++
		out = append(out, name[i])
	}
	return string(out)
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// parseDecimal reads exactly three digits as a byte value.
func parseDecimal(s string) (int, bool) {
	v := 0
	for i := 0; i < 3; i++ {
		if !isDigit(s[i]) {
			return 0, false
		}
		v = v*10 + int(s[i]-'0')
	}
	return v, true
}

// identity is the device id, which is what tells two advertisements of one
// device apart from two devices.
// weaker reports whether id a is a fallback identity and b a real one.
//
// A fallback is the endpoint the record was reached at - "10.0.10.31:7000" -
// which says where the device is, not what it is. A real id is the device's own,
// normalised. Upgrading is what keeps a speaker's per-receiver settings attached
// to it when the record that names it arrives after the one that only locates
// it.
func weaker(a, b string) bool {
	return strings.Contains(a, ":") && !strings.Contains(b, ":")
}

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

// DisplayModel turns a receiver's internal model code into its product name.
//
// A HomePod advertises "AudioAccessory1,1" and an Apple TV "AppleTV14,1". Those
// are Apple's internal identifiers: they say what something is to Apple, and
// nothing to the person looking at a list of receivers. The panel shows the
// product name instead, because "HomePod" is the thing the user picked.
//
// The mapping follows pyatv's, which is what HomePodCast resolves device names
// through, so the shell and that app call the same hardware by the same name.
// A code that is not in the table is returned unchanged: an unknown model is
// better shown as itself than guessed at.
func DisplayModel(code string) string {
	if code == "" {
		return ""
	}
	if name, ok := modelNames[code]; ok {
		return name
	}
	return code
}

// modelNames is the internal code to product name table. The names are pyatv's,
// so the shell and HomePodCast call the same hardware by the same thing.
var modelNames = map[string]string{
	// HomePods. The user is on a first-generation one, which reports
	// AudioAccessory1,1.
	"AudioAccessory1,1":       "HomePod",
	"AudioAccessory1,2":       "HomePod",
	"AudioAccessory6,1":       "HomePod",
	"AudioAccessory5,1":       "HomePod mini",
	"AudioAccessorySingle5,1": "HomePod mini",

	// Apple TVs.
	"AppleTV1,1":  "Apple TV 2",
	"AppleTV2,1":  "Apple TV 3",
	"AppleTV3,1":  "Apple TV 4",
	"AppleTV3,2":  "Apple TV 4",
	"AppleTV5,3":  "Apple TV 4K",
	"AppleTV6,2":  "Apple TV 4K",
	"AppleTV11,1": "Apple TV 4K",
	"AppleTV14,1": "Apple TV 4K",

	// AirPort Express, which speaks AirPlay and is still worth playing to.
	"AirPort4,107":  "AirPort Express",
	"AirPort10,115": "AirPort Express",
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
