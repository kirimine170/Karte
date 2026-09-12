//go:build windows

package canonical

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrivateACLAndReplacement(t *testing.T) {
	root := t.TempDir()
	if err := SecureDirectory(root); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateDirectory(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "credential.json")
	if err := WithWriter(root, func(w *Writer) error { return w.WriteCAS("credential.json", nil, []byte("synthetic credential"), 0600) }); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(path); err != nil {
		t.Fatal(err)
	}
	before, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if err := WithWriter(root, func(w *Writer) error {
		mode, err := w.Permissions("credential.json")
		if err != nil {
			return err
		}
		return w.Write("credential.json", []byte("synthetic revision"), mode)
	}); err != nil {
		t.Fatal(err)
	}
	after, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil || after.String() != before.String() {
		t.Fatalf("replacement changed security descriptor: %v", err)
	}
	if err := CheckPrivateFile(path); err != nil {
		t.Fatal(err)
	}
	wide, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := wide.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(path); err == nil {
		t.Fatal("world-accessible credential accepted")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "synthetic revision" {
		t.Fatal("permission inspection changed bytes")
	}
}
