package googlesync

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	advapi     = windows.NewLazySystemDLL("advapi32.dll")
	credRead   = advapi.NewProc("CredReadW")
	credWrite  = advapi.NewProc("CredWriteW")
	credDelete = advapi.NewProc("CredDeleteW")
	credFree   = advapi.NewProc("CredFree")
)

const genericCredential = 1
const localMachinePersistence = 2

type windowsCredential struct {
	Flags          uint32
	Type           uint32
	Target         *uint16
	Comment        *uint16
	LastWritten    windows.Filetime
	BlobSize       uint32
	Blob           *byte
	Persist        uint32
	AttributeCount uint32
	Attributes     uintptr
	TargetAlias    *uint16
	Username       *uint16
}

func (s osCredentialStore) load() ([]byte, error) {
	target, err := windows.UTF16PtrFromString(s.key)
	if err != nil {
		return nil, err
	}
	var value *windowsCredential
	ok, _, err := credRead.Call(uintptr(unsafe.Pointer(target)), genericCredential, 0, uintptr(unsafe.Pointer(&value)))
	runtime.KeepAlive(target)
	if ok == 0 {
		return nil, windowsCredentialError(err)
	}
	defer credFree.Call(uintptr(unsafe.Pointer(value)))
	if value.Blob == nil || value.BlobSize > 2560 {
		return nil, fmt.Errorf("invalid Windows credential blob")
	}
	return append([]byte(nil), unsafe.Slice(value.Blob, value.BlobSize)...), nil
}

func (s osCredentialStore) save(data []byte) error {
	if len(data) == 0 || len(data) > 2560 {
		return fmt.Errorf("Windows credential exceeds capacity")
	}
	target, err := windows.UTF16PtrFromString(s.key)
	if err != nil {
		return err
	}
	value := windowsCredential{
		Type: genericCredential, Target: target, BlobSize: uint32(len(data)),
		Blob: &data[0], Persist: localMachinePersistence,
	}
	ok, _, err := credWrite.Call(uintptr(unsafe.Pointer(&value)), 0)
	runtime.KeepAlive(data)
	runtime.KeepAlive(value)
	if ok == 0 {
		return windowsCredentialError(err)
	}
	return nil
}

func (s osCredentialStore) remove() error {
	target, err := windows.UTF16PtrFromString(s.key)
	if err != nil {
		return err
	}
	ok, _, err := credDelete.Call(uintptr(unsafe.Pointer(target)), genericCredential, 0)
	runtime.KeepAlive(target)
	if ok == 0 {
		return windowsCredentialError(err)
	}
	return nil
}

func windowsCredentialError(err error) error {
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		return errMissingCredential
	}
	return err
}
