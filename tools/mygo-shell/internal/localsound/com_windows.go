//go:build windows

package localsound

// The COM plumbing the Core Audio wrapper is built on: the vtable shapes of the
// five interfaces it calls, and the entry points it calls them through.
//
// They live apart from the endpoints and volume logic because they are the part
// that has to match the Windows SDK exactly - a vtable slot is a number that can
// be silently wrong - and reading them is easier when the code that uses them is
// not interleaved. Nothing here is specific to audio: these are the shapes
// IMMDeviceEnumerator, IMMDevice, IMMDeviceCollection, IPropertyStore and
// IAudioEndpointVolume all share.

import (
	"syscall"
	"unsafe"
)

// The two libraries the entry points come from. ole32 carries COM itself;
// kernel32 carries the thread id an apartment is filed under.
var (
	ole32                  = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx     = ole32.NewProc("CoInitializeEx")
	procCoCreateInstance   = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree      = ole32.NewProc("CoTaskMemFree")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentThreadID = kernel32.NewProc("GetCurrentThreadId")
)

// vtptr returns the vtable sitting at the head of a COM object, which is what the
// structs below are declared to line up with.
func vtptr(p unsafe.Pointer) unsafe.Pointer { return *(*unsafe.Pointer)(p) }

// release drops one reference to a COM object.
func release(p unsafe.Pointer) {
	if p == nil {
		return
	}
	vt := (*unknownVtbl)(vtptr(p))
	syscall.SyscallN(vt.release, uintptr(p))
}

// The first three methods every COM interface has. Named so the interfaces
// below read as "IUnknown's, then mine".
type unknownVtbl struct {
	queryInterface uintptr
	addRef         uintptr
	release        uintptr
}

// deviceEnumeratorVtbl is IMMDeviceEnumerator.
type deviceEnumeratorVtbl struct {
	unknownVtbl
	// enumAudioEndpoints lists the endpoints of one flow. Its second argument is
	// a device-state mask, and DEVICE_STATE_ACTIVE is the only state worth
	// listing: an unplugged speaker is not a choice.
	enumAudioEndpoints      uintptr
	getDefaultAudioEndpoint uintptr
	getDevice               uintptr
	registerNotification    uintptr
	unregisterNotification  uintptr
}

// deviceCollectionVtbl is IMMDeviceCollection.
type deviceCollectionVtbl struct {
	unknownVtbl
	getCount uintptr
	item     uintptr
}

// deviceVtbl is IMMDevice.
type deviceVtbl struct {
	unknownVtbl
	activate          uintptr
	openPropertyStore uintptr
	getID             uintptr
	getState          uintptr
}

// propertyStoreVtbl is IPropertyStore.
type propertyStoreVtbl struct {
	unknownVtbl
	getCount uintptr
	getAt    uintptr
	getValue uintptr
}

// volumeVtbl is IAudioEndpointVolume.
type volumeVtbl struct {
	unknownVtbl
	registerControlChangeNotify   uintptr
	unregisterControlChangeNotify uintptr
	getChannelCount               uintptr
	setMasterVolumeLevel          uintptr
	setMasterVolumeLevelScalar    uintptr
	getMasterVolumeLevel          uintptr
	getMasterVolumeLevelScalar    uintptr
	setChannelVolumeLevel         uintptr
	setChannelVolumeLevelScalar   uintptr
	getChannelVolumeLevel         uintptr
	getChannelVolumeLevelScalar   uintptr
	setMute                       uintptr
	getMute                       uintptr
	getVolumeRange                uintptr
}
