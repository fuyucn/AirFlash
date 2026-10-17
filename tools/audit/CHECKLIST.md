# What to look for, and where to look

Ordered by how much of this codebase's history each has caught.

1. **Per-thread platform state used from a goroutine.** Any Windows-only call
   (COM, registry, WASAPI) reached from more than one goroutine. The apartment,
   the module handle and the registry key are all per thread or per process
   depending on the API, and getting that wrong is silent.

2. **Shared state without a lock.** Fields read and written from the UI thread and
   from a worker. `go test -race` catches only what the tests happen to exercise,
   so list the `atomic.Pointer` fields by hand and ask what else touches the same
   value.

3. **A resource that is acquired and not released.** COM references, OS handles,
   goroutines blocked on a channel nobody sends to. Look for the acquire and find
   its matching release, or find the condition under which it is skipped.

4. **A default that is only right on one machine.** The default render endpoint,
   the interface index, the last-connected address. What happens when the machine
   has two sound cards, two adapters, or the remembered device is gone.

5. **A failure that is reported only by not happening.** `if err != nil { return nil }`
   and a call whose error is dropped. A tray app has no console, so anything that
   disappears does so silently.

6. **A UI control whose action depends on state the control does not show.** The
   Stats page's Play, the row that says Play for what is playing. Ask what the
   user can see that tells them what the button will do.

7. **A settings field that nothing reads.** A field is written by the UI and never
   read by the path it was meant to affect, so changing it does nothing.
