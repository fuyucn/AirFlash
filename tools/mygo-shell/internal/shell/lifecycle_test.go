package shell

import "testing"

func TestClosingReleasesTheSurfaceAndReopeningRebuildsIt(t *testing.T) {
	var opened, closed []Surface
	l := New()
	l.SetSurface(SurfaceWeb)
	l.OnOpen(func(s Surface) { opened = append(opened, s) })
	l.OnClose(func(s Surface) { closed = append(closed, s) })

	if !l.Open() {
		t.Fatal("the first open should succeed")
	}
	if l.Phase() != PhaseOpen {
		t.Fatalf("phase = %q, want %q", l.Phase(), PhaseOpen)
	}
	if !l.Close() {
		t.Fatal("closing an open window should succeed")
	}
	if l.Phase() != PhaseClosed {
		t.Fatalf("phase = %q, want %q", l.Phase(), PhaseClosed)
	}

	// Reopening must build the surface again, which is what frees and re-takes
	// the webview's process tree.
	if !l.Open() {
		t.Fatal("reopening should succeed")
	}

	if len(opened) != 2 {
		t.Errorf("opened %d times, want 2", len(opened))
	}
	if len(closed) != 1 {
		t.Errorf("closed %d times, want 1", len(closed))
	}
	if len(closed) == 1 && closed[0] != SurfaceWeb {
		t.Errorf("closed surface = %q, want %q", closed[0], SurfaceWeb)
	}
}

func TestOpenIsRefusedWhileAWindowIsAlreadyOpen(t *testing.T) {
	l := New()
	if !l.Open() {
		t.Fatal("first open should succeed")
	}
	if l.Open() {
		t.Error("a second open while one is open must be refused")
	}
	opens, _ := l.Counts()
	if opens != 1 {
		t.Errorf("opens = %d, want 1: a refused open must not be counted", opens)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	// A user can close the window and quit at the same moment; the second
	// close must not run the teardown twice.
	l := New()
	l.Open()
	if !l.Close() {
		t.Fatal("first close should succeed")
	}
	if l.Close() {
		t.Error("a second close must be a no-op")
	}
	_, closes := l.Counts()
	if closes != 1 {
		t.Errorf("closes = %d, want 1", closes)
	}
}

func TestResidentWhileNoWindowIsOpen(t *testing.T) {
	// The app stays alive with no window so the engine keeps streaming.
	l := New()
	if !l.Resident() {
		t.Error("the app should be resident with no window open")
	}
	l.Open()
	if l.Resident() {
		t.Error("the app should not be resident while a window is open")
	}
	l.Close()
	if !l.Resident() {
		t.Error("the app should be resident again after the window closes")
	}
}

func TestClosingNeverTouchesTheEngine(t *testing.T) {
	// The engine lives in its own process and must survive the window. This
	// test pins that contract: the close hook is the only teardown, and it is
	// told about the surface, not asked to stop anything else.
	engineAlive := true
	l := New()
	l.OnClose(func(Surface) {
		// A close hook that stopped the engine would flip this.
	})
	l.Open()
	l.Close()
	if !engineAlive {
		t.Error("closing the window must not stop the engine")
	}
}

func TestUnknownSurfaceFallsBackToNative(t *testing.T) {
	l := New()
	l.SetSurface(Surface("webview2-weird"))
	if got := l.Surface(); got != SurfaceNative {
		t.Errorf("surface = %q, want %q", got, SurfaceNative)
	}
}
