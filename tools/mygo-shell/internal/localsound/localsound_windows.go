//go:build windows

package localsound

// Silencing the PC's own speakers, on Windows.
//
// This is the Core Audio path the desktop host takes: IMMDeviceEnumerator finds
// the render endpoints, and each one's IAudioEndpointVolume carries the mute.
// Both are COM objects that already exist on every Windows machine, so nothing
// is registered here - the classes are only asked for.
//
// Each interface's vtable is declared as a struct of its own methods, in the
// order the Windows SDK fixes them, because a slot taken by number is a number
// that can be silently wrong. The structs carry IUnknown's three first, so a
// pointer to one of them is the object itself.
//
// Nothing here can be exercised anywhere but a Windows machine: it is written
// to be read, and the build's own cross-compilation is what checks it.

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The classes and interfaces, by the strings the SDK and the registry use.
var (
	clsidMMDeviceEnumerator = guidx("BCDE0395-E52F-467C-8E3D-C4579291692E")
	iidIMMDeviceEnumerator  = guidx("A95664D2-9614-4F35-A746-DE8DB63617E6")
	iidIAudioEndpointVolume = guidx("5CDF2C82-841E-4546-9722-0CF74078229A")
	iidIMMDeviceCollection  = guidx("0BD7A1BE-7A1A-44DB-8397-CC5392387B5E")
	// PKEY_Device_FriendlyName: the device's own name, as Windows names it.
	guidFormatID = guidx("A45C254E-DF1C-4EFD-8020-67D146A850E0")
)

// EDataFlow's two directions, and ERole's console. Together, "render + console"
// means the device the console audio is played through - what a user calls their
// speakers.
const (
	dataFlowRender  = 1
	dataFlowCapture = 0
	roleConsole     = 0
)

// DEVICE_STATE_ACTIVE.
const deviceStateActive = 0x00000001

// CLSCTX_INPROC_SERVER, the only context the audio service runs in.
const clsctxInprocServer = 1

// hresult turns a Windows status code into an error when it is a failure.
//
// S_FALSE is not a failure, and COM objects sometimes answer with it when there
// is nothing to do: it is read here rather than reported as one.
func hresult(hr uintptr) error {
	if hr == 0 || hr == 1 {
		return nil
	}
	return fmt.Errorf("COM returned 0x%08x", uint32(hr))
}

// utf16String reads a null-terminated wide string, which is how Windows returns
// device names.
func utf16String(p *uint16) string {
	return windows.UTF16PtrToString(p)
}

const (
	// COINIT_MULTITHREADED: nothing here needs a single-threaded apartment, and
	// a Go runtime's threads are not the process's main one.
	coinitMultithreaded = 0
	// RPC_E_CHANGED_MODE: COM was already initialised on this thread with a
	// different model. The objects used here work in either, so it is not a
	// failure for this package.
	rpcEChangedMode = 0x80010106
)

// windowsGetCurrentThreadID is kernel32's GetCurrentThreadId, which returns the
// id of the thread the caller is running on. It is the key an apartment is filed
// under, because an apartment belongs to a thread and not to a goroutine.
func windowsGetCurrentThreadID() uint32 {
	r, _, _ := procGetCurrentThreadID.Call()
	return uint32(r)
}

func coInitializeEx(reserved, coinit uintptr) error {
	hr, _, _ := procCoInitializeEx.Call(reserved, coinit)
	if hr == rpcEChangedMode {
		return nil
	}
	return hresult(hr)
}

// createInstance makes one COM object.
func createInstance(clsid, iid *guid) (unsafe.Pointer, error) {
	var out unsafe.Pointer
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(clsid)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	if err := hresult(hr); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, errors.New("COM object not created")
	}
	return out, nil
}

// queryInterface asks an object for one of its other interfaces.
func queryInterface(p unsafe.Pointer, iid *guid) (unsafe.Pointer, error) {
	vt := (*unknownVtbl)(vtptr(p))
	var out unsafe.Pointer
	hr, _, _ := syscall.SyscallN(vt.queryInterface, uintptr(p),
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	if err := hresult(hr); err != nil {
		return nil, err
	}
	return out, nil
}

// propertyKey is the key a property is read by: a Guid and an integer, which is
// how the SDK names them.
type propertyKey struct {
	FormatID guid
	ID       uint32
}

// propVariant is the PROPVARIANT a property store returns.
//
// Only the wide-string case is used, and only for the friendly name. The
// structure is a 16-bit type, three reserved words, then a union that starts at
// offset 8 - so a string lands there rather than at the beginning.
//
// The union member is a *uint16 rather than the uintptr the SDK writes it as.
// Both are the same width, so the layout is unchanged, but a typed pointer is
// what the value is: it can be read and freed without converting it back, and a
// conversion from uintptr is what go vet cannot prove is safe.
type propVariant struct {
	vt        uint16
	reserved1 uint16
	reserved2 uint16
	reserved3 uint16
	pwszVal   *uint16
}

var (
	// apartments holds the goroutine ids that have joined a COM apartment.
	//
	// CoInitializeEx is per thread, and a Go goroutine is not pinned to one: the
	// same goroutine can be resumed on a different OS thread, which has its own
	// apartment. So the check is "has this goroutine initialised on whatever
	// thread it is on now", keyed by the pair rather than by either one alone.
	//
	// Initialising once for the whole process - which is what this did before -
	// leaves every call after the first on a thread that never joined an
	// apartment. The mute is called from the engine's event goroutine and from the
	// UI thread, and one of the two was running inside an apartment the other had
	// opened, which is undefined on Windows: it fails with CO_E_NOTINITIALIZED,
	// and the silence silently does not happen.
	apartments sync.Map
)

// withCom runs one COM call inside a COM apartment on the calling goroutine's
// current thread, joining one first if that thread has not.
//
// An error from joining is returned rather than ignored, because a call made
// outside an apartment is the failure this exists to prevent: it does not
// announce itself, the objects simply answer CO_E_NOTINITIALIZED and the silence
// quietly does not happen.
func withCom(fn func() error) error {
	if err := joinApartment(); err != nil {
		return err
	}
	return fn()
}

// apartmentKey identifies a goroutine on the thread it is running on.
//
// The runtime does not expose a goroutine id, and reading it out of a stack is a
// hack that breaks on any release. What is stable is the thread id, which is what
// the apartment actually belongs to: a goroutine that moves to another thread
// finds that thread uninitialised and joins it there, which is correct and costs
// one extra call per thread rather than one per call.
func apartmentKey() uint64 {
	return uint64(windowsGetCurrentThreadID())
}

// joinApartment joins a COM apartment on the current thread if it has not.
//
// No matching CoUninitialize: the runtime may move a goroutine to another thread
// and back, so a balanced pair would leave a thread uninitialised on a later
// call and reintroduce the failure this prevents. One initialisation per thread
// is a bounded amount of state - threads are created by the Go runtime as it
// needs them, not per call - and it is released when the process exits.
func joinApartment() error {
	key := apartmentKey()
	if _, ok := apartments.Load(key); ok {
		return nil
	}
	if err := coInitializeEx(0, coinitMultithreaded); err != nil {
		return err
	}
	apartments.Store(key, struct{}{})
	return nil
}

// enumerator builds the device enumerator.
func enumerator() (unsafe.Pointer, error) {
	return createInstance(&clsidMMDeviceEnumerator, &iidIMMDeviceEnumerator)
}

// endpoints lists every live endpoint, both directions.
//
// Both directions, because the capture endpoint the engine records from and the
// render endpoint the local speakers are on are the same list of hardware: a
// machine with two sound cards has two of each, and choosing one to capture from
// is choosing which sound card the PC's audio comes out of.
func endpoints() []Endpoint {
	var out []Endpoint
	_ = withCom(func() error {
		enum, err := enumerator()
		if err != nil {
			return err
		}
		defer release(enum)
		vt := (*deviceEnumeratorVtbl)(vtptr(enum))

		// EDataFlow's capture, then render, so a card's two endpoints land
		// together in the list.
		for _, flow := range []int{dataFlowCapture, dataFlowRender} {
			var collection unsafe.Pointer
			hr, _, _ := syscall.SyscallN(vt.enumAudioEndpoints, uintptr(enum),
				uintptr(flow), deviceStateActive, uintptr(unsafe.Pointer(&collection)))
			if hresult(hr) != nil || collection == nil {
				continue
			}

			count := 0
			vtc := (*deviceCollectionVtbl)(vtptr(collection))
			hr, _, _ = syscall.SyscallN(vtc.getCount, uintptr(collection),
				uintptr(unsafe.Pointer(&count)))
			if hresult(hr) != nil {
				release(collection)
				continue
			}

			for i := 0; i < count; i++ {
				var device unsafe.Pointer
				hr, _, _ = syscall.SyscallN(vtc.item, uintptr(collection),
					uintptr(i), uintptr(unsafe.Pointer(&device)))
				if hresult(hr) != nil || device == nil {
					continue
				}
				id, name := identify(device)
				release(device)
				if id == "" {
					continue
				}
				out = append(out, Endpoint{ID: id, Name: name, Capture: flow == dataFlowCapture})
			}
			release(collection)
		}
		return nil
	})
	return out
}

// identify reads a device's id and its name.
func identify(device unsafe.Pointer) (id, name string) {
	vtd := (*deviceVtbl)(vtptr(device))

	var wide *uint16
	hr, _, _ := syscall.SyscallN(vtd.getID, uintptr(device), uintptr(unsafe.Pointer(&wide)))
	if hresult(hr) == nil && wide != nil {
		id = utf16String(wide)
		procCoTaskMemFree.Call(uintptr(unsafe.Pointer(wide)))
	}
	if id == "" {
		return "", ""
	}

	var store unsafe.Pointer
	hr, _, _ = syscall.SyscallN(vtd.openPropertyStore, uintptr(device),
		deviceStateActive, uintptr(unsafe.Pointer(&store)))
	if hresult(hr) != nil || store == nil {
		return id, ""
	}
	defer release(store)

	key := propertyKey{FormatID: guidFormatID, ID: 14}
	var value propVariant
	vts := (*propertyStoreVtbl)(vtptr(store))
	hr, _, _ = syscall.SyscallN(vts.getValue, uintptr(store),
		uintptr(unsafe.Pointer(&key)), uintptr(unsafe.Pointer(&value)))
	if hresult(hr) != nil {
		return id, ""
	}
	// VT_LPWSTR: the string itself is a pointer the caller owns.
	if value.vt == 31 && value.pwszVal != nil {
		name = utf16String(value.pwszVal)
		procCoTaskMemFree.Call(uintptr(unsafe.Pointer(value.pwszVal)))
	}
	if name == "" {
		// The id is what the endpoint is known by, so a name that could not be
		// read still leaves a row the user can pick.
		name = id
	}
	return id, name
}

// volumeOf is one endpoint's own volume control, by its id.
func volumeOf(id string) (unsafe.Pointer, error) {
	enum, err := enumerator()
	if err != nil {
		return nil, err
	}
	defer release(enum)
	vte := (*deviceEnumeratorVtbl)(vtptr(enum))

	wide, err := syscall.UTF16PtrFromString(id)
	if err != nil {
		return nil, err
	}
	var device unsafe.Pointer
	hr, _, _ := syscall.SyscallN(vte.getDevice, uintptr(enum),
		uintptr(unsafe.Pointer(wide)), uintptr(unsafe.Pointer(&device)))
	// A device that has gone answers "not found", which is a "nothing to
	// restore" rather than a failure worth reporting.
	if hresult(hr) != nil || device == nil {
		return nil, nil
	}
	defer release(device)

	vol, err := queryInterface(device, &iidIAudioEndpointVolume)
	if err != nil || vol == nil {
		return nil, err
	}
	return vol, nil
}

// muteEndpoint silences one endpoint, remembering what it was.
func muteEndpoint(id string) error {
	return withCom(func() error {
		vol, err := volumeOf(id)
		if err != nil {
			return err
		}
		if vol == nil {
			return fmt.Errorf("the audio endpoint %s is no longer present", id)
		}
		defer release(vol)

		before, ok := readMute(vol)
		remember(id, remembered{wasMuted: before, read: ok})
		return setMute(vol, true)
	})
}

// restore returns every endpoint this silenced to what it was.
func restore() error {
	pending := takeForRestore()
	return withCom(func() error {
		var failures []string
		for id, was := range pending {
			if !was.read {
				// Its state was never known, so restoring would be this app
				// changing a setting the user made.
				continue
			}
			vol, err := volumeOf(id)
			if err != nil || vol == nil {
				if err != nil {
					failures = append(failures, fmt.Sprintf("%s: %v", id, err))
				}
				continue
			}
			err = setMute(vol, was.wasMuted)
			release(vol)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", id, err))
			}
		}
		if len(failures) > 0 {
			return errors.New(strings.Join(failures, "; "))
		}
		return nil
	})
}

// readMute reads an endpoint's mute, and reports whether it could be read.
func readMute(vol unsafe.Pointer) (muted, ok bool) {
	vt := (*volumeVtbl)(vtptr(vol))
	var onto uint32
	hr, _, _ := syscall.SyscallN(vt.getMute, uintptr(vol), uintptr(unsafe.Pointer(&onto)))
	if hresult(hr) != nil {
		return false, false
	}
	return onto != 0, true
}

func setMute(vol unsafe.Pointer, muted bool) error {
	vt := (*volumeVtbl)(vtptr(vol))
	var onto uint32
	if muted {
		onto = 1
	}
	// The second argument is an event context, which only matters to a
	// notification the shell does not register for: it is left null.
	hr, _, _ := syscall.SyscallN(vt.setMute, uintptr(vol),
		uintptr(onto), uintptr(0))
	return hresult(hr)
}
