package trayicon

import (
	"bytes"
	"image/png"
	"testing"
)

func TestTheIconIsARealPNGOfTheRightSize(t *testing.T) {
	// The tray takes PNG bytes. A nil or empty icon leaves nothing in the
	// notification area: the app runs and there is no way to click it, which is
	// exactly what the user reported - no logo.
	data := PNG()
	if len(data) == 0 {
		t.Fatal("the icon is empty, so the tray would show nothing")
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("the icon is not a PNG, which is what a tray requires")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the icon does not decode: %v", err)
	}
	if got := img.Bounds().Dx(); got != Size {
		t.Errorf("width = %d, want %d", got, Size)
	}
	if got := img.Bounds().Dy(); got != Size {
		t.Errorf("height = %d, want %d", got, Size)
	}
}

func TestTheIconIsNotBlank(t *testing.T) {
	// A tray icon that is all one colour is invisible: nothing is drawn into it.
	img := draw()
	dark, light := 0, 0
	for y := 0; y < Size; y++ {
		for x := 0; x < Size; x++ {
			lum := img.NRGBAAt(x, y)
			if lum.R < 128 {
				dark++
			} else {
				light++
			}
		}
	}
	if dark == 0 {
		t.Error("the icon has no mark in it: it is all background")
	}
	if light == 0 {
		t.Error("the icon has no background: it is all mark, which is a solid square")
	}
}

func TestTheIconIsStable(t *testing.T) {
	// The same bytes every build: a tray icon that changes between runs is a
	// bug, and a test that only checks "some bytes" would not catch one.
	first := PNG()
	for i := 0; i < 3; i++ {
		if !bytes.Equal(PNG(), first) {
			t.Fatal("the icon changed between calls; it must be drawn deterministically")
		}
	}
}
