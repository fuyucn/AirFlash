//go:build windows

package localsound

import (
	"testing"
	"unsafe"
)

// propVariant models the PROPVARIANT the property store returns, and its layout
// is only compiled on Windows, so the assertion about it lives here too.
//
// The union starts at offset 8 and its member must be pointer-sized. The member
// is typed *uint16 rather than the uintptr the SDK writes, which is the same
// width on every Windows target, so the layout is unchanged and go vet no longer
// has to reason about a conversion from uintptr.
func TestTheUnionFieldKeepsTheSDKsLayout(t *testing.T) {
	var v propVariant
	if got, want := unsafe.Sizeof(v), unsafe.Sizeof(uintptr(0))*2; got != want {
		t.Errorf("propVariant is %d bytes, want %d: four words of header plus one pointer", got, want)
	}
	if got := unsafe.Offsetof(v.pwszVal); got != 8 {
		t.Errorf("the union starts at offset %d, want 8", got)
	}
}
