package ui

import "testing"

func TestGroundCSSNamesThePalettesOwnGround(t *testing.T) {
	// The window's background is taken from the palette, so a change to the
	// ground cannot leave the window showing a different colour from the page
	// drawn inside it - which is what a hand-written second copy does.
	got := GroundCSS()
	want := "light-dark(#F2F2F5, #161618)"
	if got != want {
		t.Errorf("ground = %q, want %q: the window would not match the page", got, want)
	}
}
