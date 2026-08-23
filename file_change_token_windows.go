//go:build windows

package main

import (
	"os"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsFileBasicInfo struct {
	CreationTime   int64
	LastAccessTime int64
	LastWriteTime  int64
	ChangeTime     int64
	FileAttributes uint32
	_              uint32
}

func platformFileChangeToken(path string, _ os.FileInfo) string {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	handle, err := windows.CreateFile(
		pathPointer,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)

	var info windowsFileBasicInfo
	if err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		return ""
	}
	return strconv.FormatInt(info.ChangeTime, 10)
}
