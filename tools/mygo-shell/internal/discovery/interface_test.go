package discovery

import (
	"net"
	"testing"
)

// The listing is only ever read from the machine the app runs on, so it cannot
// be driven from a network with a known shape. What it can be checked against is
// a set of adapters that stands in for one: a loopback, an adapter that is down,
// and one that is up.

func TestALoopbackIsNeverOfferedAsAnAdapter(t *testing.T) {
	// The machine's own traffic is not where a speaker advertises itself, so
	// offering the loopback would be offering a browse that finds nothing.
	adapters := interfacesOn([]net.Interface{
		{Index: 1, Name: "lo", Flags: net.FlagUp | net.FlagLoopback},
	}, addr("127.0.0.1"))
	for _, a := range adapters {
		t.Errorf("adapter %q offered: the loopback cannot carry mDNS to a receiver", a.Name)
	}
}

func TestAnAdapterThatIsDownIsNotOffered(t *testing.T) {
	// A disabled adapter cannot be browsed on. The flag is read here rather than
	// by calling Addrs, which on a real machine is the other place the same
	// thing shows up.
	adapters := interfacesOn([]net.Interface{
		{Index: 7, Name: "Ethernet", Flags: 0},
	}, addr("10.0.10.7"))
	for _, a := range adapters {
		t.Errorf("adapter %q offered although it is down", a.Name)
	}
}

func TestAnAdapterThatIsUpIsOffered(t *testing.T) {
	adapters := interfacesOn([]net.Interface{
		{Index: 4, Name: "Wi-Fi", Flags: net.FlagUp},
	}, addr("10.0.10.5"))
	if len(adapters) != 1 {
		t.Fatalf("%d adapters offered, want 1", len(adapters))
	}
	if adapters[0].Name != "Wi-Fi" {
		t.Errorf("adapter = %q, want Wi-Fi", adapters[0].Name)
	}
}

func TestTheListingIsStableAcrossCalls(t *testing.T) {
	// The setting is stored as the adapter's index, which the user chooses from
	// this list. A list whose order changed between two calls would leave a
	// choice pointing at another adapter.
	one := interfacesOn([]net.Interface{
		{Index: 2, Name: "Ethernet 2", Flags: net.FlagUp},
		{Index: 1, Name: "Wi-Fi", Flags: net.FlagUp},
	}, addr("10.0.10.1", "10.0.10.2"))
	two := interfacesOn([]net.Interface{
		{Index: 1, Name: "Wi-Fi", Flags: net.FlagUp},
		{Index: 2, Name: "Ethernet 2", Flags: net.FlagUp},
	}, addr("10.0.10.1", "10.0.10.2"))
	if len(one) != len(two) {
		t.Fatalf("two calls gave %d and %d adapters", len(one), len(two))
	}
	for i := range one {
		if one[i].Index != two[i].Index {
			t.Errorf("position %d held adapter %d then %d: the order must not depend on the input's",
				i, one[i].Index, two[i].Index)
		}
	}
	if len(one) > 0 && one[0].Index != 1 {
		t.Errorf("first adapter was %d, want the lowest index first", one[0].Index)
	}
}

func TestAnEmptyListingOffersNothing(t *testing.T) {
	// A machine whose adapters cannot be read offers nothing, and the picker
	// then falls back to "automatic", which is the system's own choice and still
	// works on a machine with a single adapter.
	if got := interfacesOn(nil, func(net.Interface) []net.Addr { return nil }); len(got) != 0 {
		t.Errorf("an empty listing returned %d adapters, want none", len(got))
	}
}

// addr stands in for an adapter's own Addrs, giving every adapter the addresses
// named. The machine the test runs on has adapters the test did not choose, and
// they are not what is being looked at.
func addr(ips ...string) func(net.Interface) []net.Addr {
	return func(net.Interface) []net.Addr {
		out := []net.Addr{}
		for _, ip := range ips {
			if v4 := net.ParseIP(ip).To4(); v4 != nil {
				out = append(out, &net.IPNet{IP: v4})
			}
		}
		return out
	}
}
