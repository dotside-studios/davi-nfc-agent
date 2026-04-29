//go:build windows

package tls

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// NotifyAddrChange (iphlpapi.dll) signals an event whenever a network
// address on the host changes. It's one-shot: after firing we re-arm by
// calling it again.
//
// Documented at:
//   https://learn.microsoft.com/en-us/windows/win32/api/iphlpapi/nf-iphlpapi-notifyaddrchange
var (
	iphlpapi             = windows.NewLazySystemDLL("iphlpapi.dll")
	procNotifyAddrChange = iphlpapi.NewProc("NotifyAddrChange")
)

const (
	winErrIOPending uint32 = 997 // ERROR_IO_PENDING
	winWaitObject0  uint32 = 0   // WAIT_OBJECT_0
	winWaitTimeout  uint32 = 258 // WAIT_TIMEOUT
)

// watchAddrChanges arms NotifyAddrChange in a loop. Between arming and
// the event firing we WaitForSingleObject in 1-second slices so we can
// observe the stop signal without depending on Win32 cancellation APIs
// (CancelIPChangeNotify is finicky and not available pre-Vista).
func watchAddrChanges(stop <-chan struct{}, notify func()) {
	for {
		select {
		case <-stop:
			return
		default:
		}

		event, err := windows.CreateEvent(nil, 0, 0, nil)
		if err != nil {
			return
		}

		var handle windows.Handle
		var overlapped windows.Overlapped
		overlapped.HEvent = event

		ret, _, _ := procNotifyAddrChange.Call(
			uintptr(unsafe.Pointer(&handle)),
			uintptr(unsafe.Pointer(&overlapped)),
		)
		// 0 = signalled immediately; ERROR_IO_PENDING = will signal later.
		// Anything else is an error.
		if uint32(ret) != 0 && uint32(ret) != winErrIOPending {
			windows.CloseHandle(event)
			return
		}

		// Wait in slices so we can also notice stop closing.
		fired := false
		for !fired {
			r, err := windows.WaitForSingleObject(event, 1000)
			if err != nil {
				windows.CloseHandle(event)
				return
			}
			switch r {
			case winWaitObject0:
				fired = true
			case winWaitTimeout:
				select {
				case <-stop:
					windows.CloseHandle(event)
					return
				default:
				}
			default:
				windows.CloseHandle(event)
				return
			}
		}
		windows.CloseHandle(event)
		notify()
	}
}
