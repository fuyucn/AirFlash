package discovery

import "testing"

func TestTheDNSSDEScapingIsRemoved(t *testing.T) {
	// miekg/dns UnpackDomainName keeps the DNS-SD presentation escaping: a space
	// becomes "\ ", a "(" becomes "\(". hashicorp/mdns hands that straight back
	// as the instance name, so a device called "Living Room" arrives as
	// "Living\ Room._airplay._tcp.local." and a pair called "Living Room (2)"
	// arrives with "\ " and "\(" and "\)" in it. The backslashes are wire
	// protocol, not part of the name the user typed.
	cases := map[string]string{
		`Living\ Room._airplay._tcp.local.`:           "Living Room",
		`Living\ Room\ \(2\)._airplay._tcp.local.`:    "Living Room (2)",
		`42FB06B510BD@Living\ Room._raop._tcp.local.`: "Living Room",
		`Studio._airplay._tcp.local.`:                 "Studio",
	}
	for in, want := range cases {
		if got := trimInstance(in); got != want {
			t.Errorf("trimInstance(%q) = %q, want %q", in, got, want)
		}
	}
}
