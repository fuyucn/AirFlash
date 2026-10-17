package discovery

import (
	"time"

	"strings"
	"testing"
)

// homepod is one HomePod's records: the device answers on both service types.
func homepod(deviceID, name, address string, extra map[string]string) []Record {
	airplay := map[string]string{"deviceid": deviceID}
	raop := map[string]string{"deviceid": deviceID, "cn": "0,1,2", "am": "HomePod1,1"}
	for k, v := range extra {
		airplay[k] = v
		raop[k] = v
	}
	return []Record{
		{Instance: name, Type: ServiceAirPlay, Address: address, Port: 7000, TXT: airplay},
		{Instance: deviceID + "@" + name, Type: ServiceRAOP, Address: address, Port: 50123, TXT: raop},
	}
}

func TestBothServiceTypesFoldIntoOneReceiver(t *testing.T) {
	recs := homepod("AA:BB:CC:DD:EE:FF", "Living Room", "10.0.10.31", nil)
	got := Fold(recs)

	if len(got) != 1 {
		t.Fatalf("folded %d receivers, want 1: a device advertising twice is one device", len(got))
	}
	if got[0].Name != "Living Room" {
		t.Errorf("name = %q, want %q", got[0].Name, "Living Room")
	}
	// AirPlay 2's control port is the one to use.
	if got[0].Port != 7000 {
		t.Errorf("port = %d, want 7000", got[0].Port)
	}
	if got[0].Address != "10.0.10.31" {
		t.Errorf("address = %q, want 10.0.10.31", got[0].Address)
	}
}

func TestCodecsComeFromTheRAOPRecord(t *testing.T) {
	got := Fold(homepod("AA:BB:CC:DD:EE:FF", "Living Room", "10.0.10.31", nil))
	if len(got) != 1 {
		t.Fatalf("got %d receivers, want 1", len(got))
	}
	// PCM (0) and ALAC (1) are what the engine can send.
	if len(got[0].Codecs) == 0 {
		t.Fatal("no codecs were read from cn")
	}
	var hasPCM, hasALAC bool
	for _, c := range got[0].Codecs {
		if c == 0 {
			hasPCM = true
		}
		if c == 1 {
			hasALAC = true
		}
	}
	if !hasPCM || !hasALAC {
		t.Errorf("codecs = %v, want PCM (0) and ALAC (1)", got[0].Codecs)
	}
}

func TestAStereoPairFoldsIntoOneReceiverWithTwoMembers(t *testing.T) {
	recs := homepod("AA:AA:AA:AA:AA:01", "Living Room Left", "10.0.10.31", map[string]string{"tsid": "PAIR1"})
	recs = append(recs, homepod("AA:AA:AA:AA:AA:02", "Living Room Right", "10.0.10.32", map[string]string{"tsid": "PAIR1"})...)

	got := Fold(recs)
	if len(got) != 1 {
		t.Fatalf("folded %d receivers, want 1 pair: %+v", len(got), names(got))
	}
	pair := got[0]
	if !pair.IsGroup() {
		t.Error("the receiver should be a stereo pair")
	}
	if len(pair.Members) != 2 {
		t.Fatalf("pair has %d members, want 2", len(pair.Members))
	}
	if !pair.Complete() {
		t.Error("a pair with both halves should be complete")
	}
	// Both halves must be addressable, or only one speaker would play.
	hosts := map[string]bool{}
	for _, m := range pair.Members {
		hosts[m.Address] = true
	}
	if !hosts["10.0.10.31"] || !hosts["10.0.10.32"] {
		t.Errorf("pair members = %v, want both 10.0.10.31 and 10.0.10.32", hosts)
	}
}

func TestTheLeaderOfAPairComesFirst(t *testing.T) {
	// A pair only plays correctly when playback starts from its leader.
	recs := homepod("AA:AA:AA:AA:AA:01", "Right", "10.0.10.32", map[string]string{"tsid": "P", "igl": "1"})
	recs = append(recs, homepod("AA:AA:AA:AA:AA:02", "Left", "10.0.10.31", map[string]string{"tsid": "P"})...)

	got := Fold(recs)
	if len(got) != 1 || len(got[0].Members) != 2 {
		t.Fatalf("expected one pair with two members, got %+v", names(got))
	}
	if got[0].Members[0].Address != "10.0.10.32" {
		t.Errorf("first member = %q, want the leader 10.0.10.32", got[0].Members[0].Address)
	}
}

func TestAnIncompletePairIsMarkedAndNotComplete(t *testing.T) {
	// One half answered. Playing to it would sound wrong, so it must be
	// reported as incomplete rather than presented as a working pair.
	recs := homepod("AA:AA:AA:AA:AA:01", "Living Room Left", "10.0.10.31", map[string]string{"tsid": "PAIR1"})
	got := Fold(recs)

	if len(got) != 1 {
		t.Fatalf("got %d receivers, want 1", len(got))
	}
	if got[0].Complete() {
		t.Error("a pair with one half must not be complete")
	}
	if !strings.Contains(got[0].Name, "incomplete") {
		t.Errorf("name = %q, want it to say the pair is incomplete", got[0].Name)
	}
}

func TestTwoDevicesStaySeparate(t *testing.T) {
	recs := homepod("AA:BB:CC:DD:EE:FF", "Living Room", "10.0.10.31", nil)
	recs = append(recs, homepod("11:22:33:44:55:66", "Kitchen", "10.0.10.32", nil)...)

	got := Fold(recs)
	if len(got) != 2 {
		t.Fatalf("folded %d receivers, want 2: %v", len(got), names(got))
	}
}

func TestFoldIsOrderIndependent(t *testing.T) {
	// mDNS answers arrive in any order; the result must not depend on it.
	forward := homepod("AA:AA:AA:AA:AA:01", "Left", "10.0.10.31", map[string]string{"tsid": "P"})
	forward = append(forward, homepod("AA:AA:AA:AA:AA:02", "Right", "10.0.10.32", map[string]string{"tsid": "P"})...)

	reversed := make([]Record, len(forward))
	for i, r := range forward {
		reversed[len(forward)-1-i] = r
	}

	a := Fold(forward)
	b := Fold(reversed)
	if len(a) != len(b) {
		t.Fatalf("forward folded %d, reversed folded %d", len(a), len(b))
	}
	if a[0].ID != b[0].ID {
		t.Errorf("ids differ: %q vs %q", a[0].ID, b[0].ID)
	}
	if len(a[0].Members) != len(b[0].Members) {
		t.Errorf("member counts differ: %d vs %d", len(a[0].Members), len(b[0].Members))
	}
}

func TestARecordWithoutADeviceIDFallsBackToItsEndpoint(t *testing.T) {
	got := Fold([]Record{
		{Instance: "Mystery", Type: ServiceAirPlay, Address: "10.0.0.9", Port: 7000, TXT: map[string]string{}},
	})
	if len(got) != 1 {
		t.Fatalf("got %d receivers, want 1", len(got))
	}
	if got[0].ID != "10.0.0.9:7000" {
		t.Errorf("id = %q, want the endpoint 10.0.0.9:7000", got[0].ID)
	}
	if got[0].Port != 7000 {
		t.Errorf("port = %d, want 7000", got[0].Port)
	}
}

func TestADeviceWithNoNameFallsBackToItsAddress(t *testing.T) {
	got := Fold([]Record{
		{Instance: "", Type: ServiceAirPlay, Address: "10.0.0.9", Port: 7000, TXT: map[string]string{"deviceid": "X"}},
	})
	if got[0].Name != "10.0.0.9" {
		t.Errorf("name = %q, want the address", got[0].Name)
	}
}

func TestPortDefaultsWhenNoneIsAdvertised(t *testing.T) {
	got := Fold([]Record{
		{Instance: "NoPort", Type: ServiceAirPlay, Address: "10.0.0.9", Port: 0, TXT: map[string]string{"deviceid": "X"}},
	})
	if got[0].Port != DefaultPort {
		t.Errorf("port = %d, want the default %d", got[0].Port, DefaultPort)
	}
}

func TestNoRecordsFoldsToNothing(t *testing.T) {
	if got := Fold(nil); len(got) != 0 {
		t.Errorf("folded %d receivers from no records", len(got))
	}
}

func names(rs []Receiver) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Name)
	}
	return out
}

// TestOneDeviceIsOneReceiverWhateverItsDeviceIdLooksLike is the bug a real
// network exposed: a HomePod advertises its MAC differently on the two service
// types, and comparing the raw text split one speaker into two receivers of the
// same name.
//
// From the log of a user's network:
//
//	_airplay:  deviceid=42:FB:06:B5:10:BD
//	_raop:     deviceid=42FB06B510BD
func TestOneDeviceIsOneReceiverWhateverItsDeviceIdLooksLike(t *testing.T) {
	records := []Record{
		{Instance: "Living Room._airplay._tcp.local", Type: ServiceAirPlay, Address: "10.0.10.31", Port: 7000,
			TXT: map[string]string{"deviceid": "42:FB:06:B5:10:BD", "model": "AudioAccessory1,1"}},
		{Instance: "42FB06B510BD@Living Room._raop._tcp.local", Type: ServiceRAOP, Address: "10.0.10.31", Port: 50123,
			TXT: map[string]string{"deviceid": "42FB06B510BD", "cn": "0,1", "am": "AudioAccessory1,1"}},
	}

	got := Fold(records)
	if len(got) != 1 {
		t.Fatalf("folded %d receivers (%s), want 1: one device is one receiver",
			len(got), names(got))
	}
	if got[0].Name != "Living Room" {
		t.Errorf("name = %q, want the advertised name %q", got[0].Name, "Living Room")
	}
	// The name comes from the _airplay advert, never from the raop instance,
	// which carries the MAC in front of it.
	if strings.Contains(got[0].Name, "@") || strings.Contains(got[0].Name, "42FB06B510BD") {
		t.Errorf("name = %q, want the device's name rather than its raop instance", got[0].Name)
	}
}

func TestNormalizeReducesOnlyMACs(t *testing.T) {
	// Anything that is not a twelve-digit hex string is left alone, so a pair
	// id or an endpoint is not mangled into something else.
	for _, tc := range []struct{ in, want string }{
		{"42:FB:06:B5:10:BD", "42fb06b510bd"},
		{"42FB06B510BD", "42fb06b510bd"},
		{"42:FB:06:B5:10:BD", "42fb06b510bd"},
		{"42-FB-06-B5-10-BD", "42fb06b510bd"},
		{"PAIR1", "PAIR1"},
		{"", ""},
		{"10.0.10.31", "10.0.10.31"},
	} {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAReplyThatStopsIsReportedOffline(t *testing.T) {
	// mDNS has no "a record went away" event, so a device that leaves stays in
	// the browse's table forever. Reporting it as still online would send the
	// user to a speaker that is no longer there.
	records := []Record{
		{Instance: "Living Room._airplay._tcp.local", Type: ServiceAirPlay, Address: "10.0.10.31", Port: 7000,
			TXT: map[string]string{"deviceid": "42:FB:06:B5:10:BD", "model": "AudioAccessory1,1"}},
	}
	folded := Fold(records)

	now := time.Now()
	fresh := map[string]time.Time{}
	for _, rec := range records {
		fresh[BrowseKey(rec)] = now
	}
	markPresence(folded, fresh, now)
	if !folded[0].Online {
		t.Error("a receiver that just answered is reported offline")
	}

	stale := map[string]time.Time{}
	for _, rec := range records {
		stale[BrowseKey(rec)] = now.Add(-2 * PresenceTimeout)
	}
	markPresence(folded, stale, now)
	if folded[0].Online {
		t.Error("a receiver that has stopped answering is still reported online")
	}
}

func TestPresenceIsJudgedOnTheKeysTheFoldRecorded(t *testing.T) {
	// The browse's key includes the instance name, so the fold has to keep the
	// key whole rather than rebuild one from the address: a rebuild produces
	// "_airplay._tcp||10.0.10.31", which matches nothing, and every receiver
	// would read as offline.
	records := []Record{
		{Instance: "Living Room._airplay._tcp.local", Type: ServiceAirPlay, Address: "10.0.10.31", Port: 7000,
			TXT: map[string]string{"deviceid": "42:FB:06:B5:10:BD", "model": "AudioAccessory1,1"}},
	}
	folded := Fold(records)
	if len(folded[0].RecordKeys) == 0 {
		t.Fatal("the fold recorded no browse keys, so presence can never be judged")
	}
	if folded[0].RecordKeys[0] != BrowseKey(records[0]) {
		t.Errorf("recorded key %q, want the browse's own key %q",
			folded[0].RecordKeys[0], BrowseKey(records[0]))
	}
}

func TestDisplayModelNamesAHomePod(t *testing.T) {
	// A first-generation HomePod, which is what the user has, reports
	// AudioAccessory1,1. Showing that string is showing Apple's internal code,
	// not the thing on the shelf.
	if got := DisplayModel("AudioAccessory1,1"); got != "HomePod" {
		t.Errorf("DisplayModel(AudioAccessory1,1) = %q, want %q", got, "HomePod")
	}
	for code, want := range map[string]string{
		"AudioAccessory1,2":       "HomePod",
		"AudioAccessory6,1":       "HomePod",
		"AudioAccessory5,1":       "HomePod mini",
		"AudioAccessorySingle5,1": "HomePod mini",
		"AppleTV14,1":             "Apple TV 4K",
		"AirPort4,107":            "AirPort Express",
	} {
		if got := DisplayModel(code); got != want {
			t.Errorf("DisplayModel(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestDisplayModelLeavesWhatItDoesNotKnowAlone(t *testing.T) {
	// An unknown model is shown as itself rather than guessed at, so a device
	// released from now on still identifies itself.
	for _, code := range []string{"SomeFutureModel9,9", "Mac17,2", "iPad14,1"} {
		if got := DisplayModel(code); got != code {
			t.Errorf("DisplayModel(%q) = %q, want the code unchanged", code, got)
		}
	}
	if got := DisplayModel(""); got != "" {
		t.Errorf("no code = %q, want nothing", got)
	}
}

func TestTheTrailingDotOnAnInstanceNameIsRemoved(t *testing.T) {
	// hashicorp/mdns reports the fully qualified name, which ends in a dot. A
	// suffix trim that does not account for it leaves the dot on, and the panel
	// shows "Living Room._airplay._tcp.local." as the device's name.
	got := Fold([]Record{
		{Instance: "Living Room._airplay._tcp.local.", Type: ServiceAirPlay,
			Address: "10.0.10.31", Port: 7000, TXT: map[string]string{"deviceid": "42FB06B510BD"}},
	})
	if len(got) != 1 {
		t.Fatalf("%d receivers, want 1", len(got))
	}
	if got[0].Name != "Living Room" {
		t.Errorf("name = %q, want Living Room", got[0].Name)
	}
}

func TestTwoRecordsOfOneDeviceThatDisagreeOnTheIdStillFold(t *testing.T) {
	// One device is one receiver. When its two records carry different device
	// ids - a browse that caught only some of a device's TXT, or a device that
	// advertises a MAC with separators on one service and without on the other -
	// folding them apart lists the same speaker twice, each with its own Play.
	// The address is what every record of a device agrees on, so it decides.
	got := Fold([]Record{
		{Instance: "Living Room._airplay._tcp.local.", Type: ServiceAirPlay,
			Address: "10.0.10.31", Port: 7000, TXT: map[string]string{"deviceid": "42FB:06B5:10BD"}},
		{Instance: "x@Living Room._raop._tcp.local.", Type: ServiceRAOP,
			Address: "10.0.10.31", Port: 7000, TXT: map[string]string{"deviceid": "AABBCCDDEEFF"}},
	})
	if len(got) != 1 {
		t.Fatalf("%d receivers, want 1: the same address is one device", len(got))
	}
	if got[0].Name != "Living Room" {
		t.Errorf("name = %q, want Living Room", got[0].Name)
	}
}

func TestDevicesAtDifferentAddressesStaySeparateReceivers(t *testing.T) {
	// The address-based fallback must not collapse two speakers into one: they
	// are at different addresses, which is what the fallback keys on.
	got := Fold([]Record{
		{Instance: "Living Room._airplay._tcp.local.", Type: ServiceAirPlay,
			Address: "10.0.10.31", Port: 7000, TXT: map[string]string{"deviceid": "42FB06B510BD"}},
		{Instance: "Study._airplay._tcp.local.", Type: ServiceAirPlay,
			Address: "10.0.10.44", Port: 7000, TXT: map[string]string{"deviceid": "7E5B712836F4"}},
	})
	if len(got) != 2 {
		t.Errorf("%d receivers, want 2", len(got))
	}
}
