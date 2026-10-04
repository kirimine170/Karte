//go:build windows

package ephyoutbox

import (
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

// Rename relative to the open directory handle, with ReplaceIfExists false.
// Unlike a path-based check followed by rename, the kernel refuses every
// existing destination, including an empty directory or a case alias.
func installEvidenceDirectory(root *os.Root, stage, candidate string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	name, err := windows.NewNTUnicodeString(stage)
	if err != nil {
		return err
	}
	attrs := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(dir.Fd()), ObjectName: name,
		Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	var source windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&source, windows.DELETE|windows.SYNCHRONIZE, &attrs, &status, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return evidenceWindowsError(err)
	}
	defer windows.CloseHandle(source)
	encoded, err := windows.UTF16FromString(candidate)
	if err != nil {
		return err
	}
	info := struct {
		ReplaceIfExists byte
		RootDirectory   windows.Handle
		FileNameLength  uint32
		FileName        [129]uint16
	}{RootDirectory: windows.Handle(dir.Fd()), FileNameLength: uint32((len(encoded) - 1) * 2)}
	if len(encoded) > len(info.FileName) {
		return windows.ERROR_INVALID_NAME
	}
	copy(info.FileName[:], encoded)
	const fileRenameInformation = 10
	err = windows.NtSetInformationFile(source, &status, (*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Offsetof(info.FileName))+uint32(len(encoded)*2), fileRenameInformation)
	return evidenceWindowsError(err)
}

func evidenceWindowsError(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}

// Evidence file handles are flushed before installation. Windows does not
// provide POSIX directory fsync through os.File; do not treat it as available.
func syncEvidenceDirectory(root *os.Root) error { return nil }
