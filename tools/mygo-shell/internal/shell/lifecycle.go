// Package shell owns the app's lifecycle: a resident tray, an engine that
// keeps playing whether or not a window exists, and a window that is created
// only while the user is looking at it.
package shell

import (
	"sync"
)

// Surface is how the window is drawn.
type Surface string

const (
	// SurfaceNative draws the window with MyGo's own renderer: no webview, no
	// extra processes, about 53 MB resident.
	SurfaceNative Surface = "native"
	// SurfaceWeb is a webview window. It costs a browser process tree while it
	// is open, so it is destroyed rather than hidden when the user is done.
	SurfaceWeb Surface = "webview"
)

// Phase is where the window currently is.
type Phase string

const (
	// PhaseClosed means no window exists and none of its memory is held.
	PhaseClosed Phase = "closed"
	// PhaseOpen means the window exists and is visible.
	PhaseOpen Phase = "open"
	// PhaseClosing is a transient state while the surface is torn down.
	PhaseClosing Phase = "closing"
)

// Lifecycle tracks the window's state and enforces the rule that matters:
// closing the window must free its surface, and must not touch the engine.
type Lifecycle struct {
	mu      sync.Mutex
	phase   Phase
	surface Surface
	opens   int
	closes  int
	// onOpen and onClose are called outside the lock.
	onOpen  func(Surface)
	onClose func(Surface)
}

// New returns a lifecycle with no window open.
func New() *Lifecycle {
	return &Lifecycle{phase: PhaseClosed, surface: SurfaceNative}
}

// OnOpen registers a hook run when a window is created.
func (l *Lifecycle) OnOpen(fn func(Surface)) {
	l.mu.Lock()
	l.onOpen = fn
	l.mu.Unlock()
}

// OnClose registers a hook run after a window is destroyed. It is where the
// shell stops anything that only exists to serve a visible window, such as a
// metric sampling loop, and never where it stops the engine.
func (l *Lifecycle) OnClose(fn func(Surface)) {
	l.mu.Lock()
	l.onClose = fn
	l.mu.Unlock()
}

// Phase reports the current window state.
func (l *Lifecycle) Phase() Phase {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.phase
}

// Surface reports how the window is drawn.
func (l *Lifecycle) Surface() Surface {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.surface
}

// Counts reports how many times a window has opened and closed, which is how
// a test proves that closing really releases the surface.
func (l *Lifecycle) Counts() (opens, closes int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.opens, l.closes
}

// SetSurface chooses how the window is drawn. It applies to the next open.
func (l *Lifecycle) SetSurface(s Surface) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s != SurfaceWeb {
		s = SurfaceNative
	}
	l.surface = s
}

// Open marks a window as created and returns whether it actually opened.
// A second open while one is already open is refused, not counted.
func (l *Lifecycle) Open() bool {
	l.mu.Lock()
	if l.phase == PhaseOpen {
		l.mu.Unlock()
		return false
	}
	l.phase = PhaseOpen
	l.opens++
	surface := l.surface
	fn := l.onOpen
	l.mu.Unlock()

	if fn != nil {
		fn(surface)
	}
	return true
}

// Close marks the window as destroyed and returns whether it actually closed.
// It is idempotent, because a window can be closed by the user and by a quit
// in the same moment.
func (l *Lifecycle) Close() bool {
	l.mu.Lock()
	if l.phase != PhaseOpen {
		l.mu.Unlock()
		return false
	}
	l.phase = PhaseClosed
	l.closes++
	surface := l.surface
	fn := l.onClose
	l.mu.Unlock()

	if fn != nil {
		fn(surface)
	}
	return true
}

// Resident reports whether the app should keep running with no window. A
// tray app does: the engine keeps streaming while the window is gone.
func (l *Lifecycle) Resident() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.phase == PhaseClosed
}
