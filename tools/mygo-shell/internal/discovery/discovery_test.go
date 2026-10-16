package discovery

import (
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
