package engine

// The engine is a console-subsystem Rust binary, and the shell is a GUI app.
// On Windows, launching a console child from a GUI parent gives the child a
// console window of its own, which flashes open every time a stream starts and
// stays for as long as the engine runs. A tray app has no console and must not
// open one, so the child is told not to.
import "os/exec"

// hideConsole returns the platform attributes that start a child with no
// window, or nil where the platform needs nothing.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = consoleAttr()
}
