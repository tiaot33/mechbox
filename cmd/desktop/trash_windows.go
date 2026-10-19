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
	sOK                        = 0
	sFalse                     = 1
	eAbort                     = 0x80004004
	eNoInterface               = 0x80004002
)

const (
	unknownRelease            = 2
	opSetOperationFlags       = 5
	opDeleteItem              = 18
	opPerformOperations       = 21
	opGetAnyOperationsAborted = 22
	fileOperationMethods      = 23
)

const (
	sinkQueryInterface = iota
	sinkAddRef
	sinkRelease
	sinkStartOperations
	sinkFinishOperations
	sinkPreRenameItem
	sinkPostRenameItem
	sinkPreMoveItem
	sinkPostMoveItem
	sinkPreCopyItem
	sinkPostCopyItem
	sinkPreDeleteItem
	sinkPostDeleteItem
	sinkPreNewItem
	sinkPostNewItem
	sinkUpdateProgress
	sinkResetTimer
	sinkPauseTimer
	sinkResumeTimer
	sinkMethods
)

type comObject struct {
	vtbl *[fileOperationMethods]uintptr
}

func (o *comObject) call(method int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtbl[method], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

func (o *comObject) release() { o.call(unknownRelease) }

type progressSink struct {
	vtbl      *[sinkMethods]uintptr
	permanent bool
}

func trash(path string) error {
	if err := recycle(path); err != nil {
		return fmt.Errorf("move %s to recycle bin: %w", path, err)
	}
	return nil
}

func recycle(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	recycleMu.Lock()
	defer recycleMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil && !errors.Is(err, syscall.Errno(sFalse)) {
		return err
	}
	defer windows.CoUninitialize()
	var op *comObject
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidFileOperation)), 0, clsctxAll, uintptr(unsafe.Pointer(&iidFileOperation)), uintptr(unsafe.Pointer(&op))); failed(hr) {
		return hresult(hr)
	}
	defer op.release()
	if hr := op.call(opSetOperationFlags, fofNoUI|fofAllowUndo|fofxRecycleOnDelete|fofxEarlyFailure); failed(hr) {
		return hresult(hr)
	}
	var item *comObject
	if hr, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item))); failed(hr) {
		return hresult(hr)
	}
	defer item.release()
	recycleSink = progressSink{vtbl: sinkVtbl()}
	if hr := op.call(opDeleteItem, uintptr(unsafe.Pointer(item)), uintptr(unsafe.Pointer(&recycleSink))); failed(hr) {
		return hresult(hr)
	}
	hr := op.call(opPerformOperations)
	if recycleSink.permanent {
		return errors.New("its drive has no recycle bin, so it was left in place")
	}
	if failed(hr) {
		return hresult(hr)
	}
	var aborted int32
	if hr := op.call(opGetAnyOperationsAborted, uintptr(unsafe.Pointer(&aborted))); failed(hr) {
		return hresult(hr)
	}
	if aborted != 0 {
		return errors.New("the operation was aborted")
	}
	return nil
}

func failed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

func hresult(hr uintptr) error { return syscall.Errno(uint32(hr)) }

func newSinkVtbl() *[sinkMethods]uintptr {
	stub := [...]uintptr{
		syscall.NewCallback(func(*progressSink) uintptr { return sOK }),
		syscall.NewCallback(func(*progressSink, uintptr) uintptr { return sOK }),
		syscall.NewCallback(func(*progressSink, uintptr, uintptr) uintptr { return sOK }),
		syscall.NewCallback(func(*progressSink, uintptr, uintptr, uintptr) uintptr { return sOK }),
		syscall.NewCallback(func(*progressSink, uintptr, uintptr, uintptr, uintptr) uintptr { return sOK }),
		syscall.NewCallback(func(*progressSink, uintptr, uintptr, uintptr, uintptr, uintptr) uintptr { return sOK }),
		syscall.NewCallback(func(*progressSink, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr) uintptr { return sOK }),
		syscall.NewCallback(func(*progressSink, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr, uintptr) uintptr { return sOK }),
	}
	refCount := syscall.NewCallback(func(*progressSink) uintptr { return 1 })
	return &[sinkMethods]uintptr{
		sinkQueryInterface: syscall.NewCallback(func(s *progressSink, iid *windows.GUID, out *unsafe.Pointer) uintptr {
			if *iid == iidUnknown || *iid == iidProgressSink {
				*out = unsafe.Pointer(s)
				return sOK
			}
			*out = nil
			return eNoInterface
		}),
		sinkAddRef:           refCount,
		sinkRelease:          refCount,
		sinkStartOperations:  stub[0],
		sinkFinishOperations: stub[1],
		sinkPreRenameItem:    stub[3],
		sinkPostRenameItem:   stub[5],
		sinkPreMoveItem:      stub[4],
		sinkPostMoveItem:     stub[6],
		sinkPreCopyItem:      stub[4],
		sinkPostCopyItem:     stub[6],
		sinkPreDeleteItem: syscall.NewCallback(func(s *progressSink, flags uintptr, item uintptr) uintptr {
			if flags&tsfDeleteRecycleIfPossible == 0 {
				s.permanent = true
				return eAbort
			}
			return sOK
		}),
		sinkPostDeleteItem: stub[4],
		sinkPreNewItem:     stub[3],
		sinkPostNewItem:    stub[7],
		sinkUpdateProgress: stub[2],
		sinkResetTimer:     stub[0],
		sinkPauseTimer:     stub[0],
		sinkResumeTimer:    stub[0],
	}
}
