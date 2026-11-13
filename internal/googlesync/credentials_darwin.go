//go:build darwin && cgo

package googlesync

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const keychainAccount = "Karte"

func (s osCredentialStore) load() ([]byte, error) {
	service, account := C.CString(s.key), C.CString(keychainAccount)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	var size C.UInt32
	var data unsafe.Pointer
	status := C.SecKeychainFindGenericPassword(nil, C.UInt32(len(s.key)), service,
		C.UInt32(len(keychainAccount)), account, &size, &data, nil)
	if err := keychainError(status); err != nil {
		return nil, err
	}
	defer C.SecKeychainItemFreeContent(nil, data)
	if size > 64<<10 {
		return nil, fmt.Errorf("invalid Keychain credential blob")
	}
	return C.GoBytes(data, C.int(size)), nil
}

func (s osCredentialStore) save(data []byte) error {
	service, account := C.CString(s.key), C.CString(keychainAccount)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	payload := C.CBytes(data)
	defer C.free(payload)
	status := C.SecKeychainAddGenericPassword(nil, C.UInt32(len(s.key)), service,
		C.UInt32(len(keychainAccount)), account, C.UInt32(len(data)), payload, nil)
	if status != C.errSecDuplicateItem {
		return keychainError(status)
	}
	item, err := s.findItem()
	if err != nil {
		return err
	}
	defer C.CFRelease(C.CFTypeRef(item))
	return keychainError(C.SecKeychainItemModifyAttributesAndData(item, nil, C.UInt32(len(data)), payload))
}

func (s osCredentialStore) remove() error {
	item, err := s.findItem()
	if err != nil {
		return err
	}
	defer C.CFRelease(C.CFTypeRef(item))
	return keychainError(C.SecKeychainItemDelete(item))
}

func (s osCredentialStore) findItem() (C.SecKeychainItemRef, error) {
	service, account := C.CString(s.key), C.CString(keychainAccount)
	defer C.free(unsafe.Pointer(service))
	defer C.free(unsafe.Pointer(account))
	var item C.SecKeychainItemRef
	status := C.SecKeychainFindGenericPassword(nil, C.UInt32(len(s.key)), service,
		C.UInt32(len(keychainAccount)), account, nil, nil, &item)
	return item, keychainError(status)
}

func keychainError(status C.OSStatus) error {
	switch status {
	case C.errSecSuccess:
		return nil
	case C.errSecItemNotFound:
		return errMissingCredential
	default:
		return fmt.Errorf("Keychain credential error (%d)", int(status))
	}
}
