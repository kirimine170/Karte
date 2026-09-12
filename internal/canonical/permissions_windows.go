//go:build windows

package canonical

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func privateACL(directory bool) (*windows.ACL, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, err
	}
	acl, _, err := sd.DACL()
	return acl, err
}
func SecureDirectory(path string) error {
	acl, err := privateACL(true)
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, user.User.Sid, nil, acl, nil)
}
func CheckPrivateFile(path string) error      { return checkPrivateACL(path, false) }
func CheckPrivateDirectory(path string) error { return checkPrivateACL(path, true) }
func checkPrivateACL(path string, directory bool) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if st.IsDir() != directory || (!directory && !st.Mode().IsRegular()) || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe_credentials")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		return fmt.Errorf("unsafe_credentials")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return fmt.Errorf("unsafe_credentials")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(user.User.Sid) {
			return fmt.Errorf("unsafe_credentials")
		}
	}
	return nil
}

// A replacement retains an existing DACL．New private files get a protected
// current-user-only DACL before any bytes are written into the temporary file．
func preparePermissions(root *os.Root, temp, target string, mode os.FileMode) error {
	targetPath := filepath.Join(root.Name(), target)
	var acl *windows.ACL
	var owner *windows.SID
	if _, err := root.Lstat(target); err == nil {
		sd, err := windows.GetNamedSecurityInfo(targetPath, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
		if err != nil {
			return err
		}
		acl, _, err = sd.DACL()
		if err != nil {
			return err
		}
		owner, _, err = sd.Owner()
		if err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if mode.Perm()&0077 == 0 {
		var err error
		acl, err = privateACL(false)
		if err != nil {
			return err
		}
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return err
		}
		owner = user.User.Sid
	} else {
		return nil
	}
	return windows.SetNamedSecurityInfo(filepath.Join(root.Name(), temp), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, owner, nil, acl, nil)
}
