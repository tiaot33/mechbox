//go:build !server

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func removeFromSystem() error { return nil }

func discard(path string) error {
	recycleMu.Lock()
	defer recycleMu.Unlock()
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		done <- recycle(path)
	}()
	return <-done
}

var (
	clsidFileOperation = windows.GUID{Data1: 0x3ad05575, Data2: 0x8857, Data3: 0x4850, Data4: [8]byte{0x92, 0x77, 0x11, 0xb8, 0x5b, 0xdb, 0x8e, 0x09}}
	iidFileOperation   = windows.GUID{Data1: 0x947aab5f, Data2: 0x0a5c, Data3: 0x4c13, Data4: [8]byte{0xb4, 0xd6, 0x4b, 0xf7, 0x83, 0x6f, 0xc9, 0xf8}}
	iidShellItem       = windows.GUID{Data1: 0x43826d1e, Data2: 0xe718, Data3: 0x42ee, Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe}}
	iidProgressSink    = windows.GUID{Data1: 0x04b0f1a7, Data2: 0x9490, Data3: 0x44bc, Data4: [8]byte{0x96, 0xe1, 0x42, 0x96, 0xa3, 0x12, 0x52, 0xe2}}
	iidUnknown         = windows.GUID{Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}

	procCoCreateInstance            = windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance")
	procSHCreateItemFromParsingName = windows.NewLazySystemDLL("shell32.dll").NewProc("SHCreateItemFromParsingName")

	recycleMu   sync.Mutex
	recycleSink progressSink
	sinkVtbl    = sync.OnceValue(newSinkVtbl)
)

const (
	clsctxAll                  = 0x17
	fofNoUI                    = 0x614
	fofAllowUndo               = 0x40
	fofxRecycleOnDelete        = 0x80000
	fofxEarlyFailure           = 0x100000
	tsfDeleteRecycleIfPossible = 0x80
	sFalse                     = 1
	eAbort                     = 0x80004004
	eNoInterface               = 0x80004002

	methodRelease                 = 2
	methodSetOperationFlags       = 5
	methodDeleteItem              = 18
	methodPerformOperations       = 21
	methodGetAnyOperationsAborted = 22
)

type comObject struct{ vtbl *[23]uintptr }

func (o *comObject) call(method int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtbl[method], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

type progressSink struct {
	vtbl      *[19]uintptr
	permanent bool
}

func recycle(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil && !errors.Is(err, syscall.Errno(sFalse)) {
		return fmt.Errorf("move %s to recycle bin: %w", path, err)
	}
	defer windows.CoUninitialize()
	var op *comObject
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidFileOperation)), 0, clsctxAll, uintptr(unsafe.Pointer(&iidFileOperation)), uintptr(unsafe.Pointer(&op))); failed(hr) {
		return hresultError(path, hr)
	}
	defer op.call(methodRelease)
	if hr := op.call(methodSetOperationFlags, fofNoUI|fofAllowUndo|fofxRecycleOnDelete|fofxEarlyFailure); failed(hr) {
		return hresultError(path, hr)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var item *comObject
	if hr, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item))); failed(hr) {
		return hresultError(path, hr)
	}
	defer item.call(methodRelease)
	recycleSink = progressSink{vtbl: sinkVtbl()}
	if hr := op.call(methodDeleteItem, uintptr(unsafe.Pointer(item)), uintptr(unsafe.Pointer(&recycleSink))); failed(hr) {
		return hresultError(path, hr)
	}
	hr := op.call(methodPerformOperations)
	if recycleSink.permanent {
		return fmt.Errorf("move %s to recycle bin: its drive has no recycle bin, so it was left in place", path)
	}
	if failed(hr) {
		return hresultError(path, hr)
	}
	var aborted int32
	if hr := op.call(methodGetAnyOperationsAborted, uintptr(unsafe.Pointer(&aborted))); failed(hr) {
		return hresultError(path, hr)
	}
	if aborted != 0 {
		return fmt.Errorf("move %s to recycle bin: the operation was aborted", path)
	}
	return nil
}

func failed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

func hresultError(path string, hr uintptr) error {
	return fmt.Errorf("move %s to recycle bin: %w", path, syscall.Errno(uint32(hr)))
}

func newSinkVtbl() *[19]uintptr {
	ok0 := syscall.NewCallback(func(*progressSink) uintptr { return 0 })
	ok1 := syscall.NewCallback(func(*progressSink, uintptr) uintptr { return 0 })
	ok2 := syscall.NewCallback(func(*progressSink, uintptr, uintptr) uintptr { return 0 })
	ok3 := syscall.NewCallback(func(*progressSink, uintptr, uintptr, uintptr) uintptr { return 0 })
	ok4 := syscall.NewCallback(func(*progressSink, uintptr, uintptr, uintptr, uintptr) uintptr { return 0 })
	ok5 := syscall.NewCallback(func(*progressSink, uintptr, uintptr, uintptr, uintptr, uintptr) uintptr { return 0 })
	ok6 := syscall.NewCallback(func(*progressSink, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr) uintptr { return 0 })
	ok7 := syscall.NewCallback(func(*progressSink, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr) uintptr { return 0 })
	refCount := syscall.NewCallback(func(*progressSink) uintptr { return 1 })
	return &[19]uintptr{
		syscall.NewCallback(func(s *progressSink, iid *windows.GUID, out *unsafe.Pointer) uintptr {
			if *iid == iidUnknown || *iid == iidProgressSink {
				*out = unsafe.Pointer(s)
				return 0
			}
			*out = nil
			return eNoInterface
		}),
		refCount,
		refCount,
		ok0,
		ok1,
		ok3,
		ok5,
		ok4,
		ok6,
		ok4,
		ok6,
		syscall.NewCallback(func(s *progressSink, flags uintptr, item uintptr) uintptr {
			if flags&tsfDeleteRecycleIfPossible == 0 {
				s.permanent = true
				return eAbort
			}
			return 0
		}),
		ok4,
		ok3,
		ok7,
		ok2,
		ok0,
		ok0,
		ok0,
	}
}
