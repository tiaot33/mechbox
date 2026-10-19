//go:build !server

package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	advapi32           = windows.NewLazySystemDLL("advapi32.dll")
	procCredReadW      = advapi32.NewProc("CredReadW")
	procCredWriteW     = advapi32.NewProc("CredWriteW")
	procCredDeleteW    = advapi32.NewProc("CredDeleteW")
	procCredEnumerateW = advapi32.NewProc("CredEnumerateW")
	procCredFree       = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
)

type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func (k keychain) get() ([]byte, bool, error) { return k.parted().get() }

func (k keychain) set(value []byte) error { return k.parted().set(value) }

func (k keychain) delete() error { return k.parted().delete() }

func (k keychain) parted() partedCredential {
	return partedCredential{store: windowsCredentials{}, target: k.service + ":" + k.account, user: k.account}
}

type windowsCredentials struct{}

func (windowsCredentials) read(target string) ([]byte, bool, error) {
	name, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return nil, false, err
	}
	var cred *credential
	if ok, _, err := procCredReadW.Call(uintptr(unsafe.Pointer(name)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&cred))); ok == 0 {
		if errors.Is(err, windows.ERROR_NOT_FOUND) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read credential %s: %w", target, err)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(cred)))
	return bytes.Clone(unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)), true, nil
}

func (windowsCredentials) write(target, user string, blob []byte) error {
	name, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	userName, err := windows.UTF16PtrFromString(user)
	if err != nil {
		return err
	}
	cred := credential{
		Type:               credTypeGeneric,
		TargetName:         name,
		UserName:           userName,
		Persist:            credPersistLocalMachine,
		CredentialBlobSize: uint32(len(blob)),
	}
	if len(blob) > 0 {
		cred.CredentialBlob = &blob[0]
	}
	if ok, _, err := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0); ok == 0 {
		return fmt.Errorf("write credential %s: %w", target, err)
	}
	return nil
}

func (windowsCredentials) remove(target string) error {
	name, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	if ok, _, err := procCredDeleteW.Call(uintptr(unsafe.Pointer(name)), credTypeGeneric, 0); ok == 0 && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		return fmt.Errorf("delete credential %s: %w", target, err)
	}
	return nil
}

func (windowsCredentials) list(prefix string) ([]string, error) {
	filter, err := windows.UTF16PtrFromString(prefix + "*")
	if err != nil {
		return nil, err
	}
	var count uint32
	var creds **credential
	if ok, _, err := procCredEnumerateW.Call(uintptr(unsafe.Pointer(filter)), 0, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&creds))); ok == 0 {
		if errors.Is(err, windows.ERROR_NOT_FOUND) {
			return nil, nil
		}
		return nil, fmt.Errorf("list credentials %s: %w", prefix, err)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(creds)))
	var targets []string
	for _, cred := range unsafe.Slice(creds, count) {
		if target := windows.UTF16PtrToString(cred.TargetName); cred.Type == credTypeGeneric && strings.HasPrefix(target, prefix) {
			targets = append(targets, target)
		}
	}
	return targets, nil
}
