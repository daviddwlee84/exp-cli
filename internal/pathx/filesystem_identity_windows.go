//go:build windows

package pathx

import (
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DirectoryFilesystemIdentity returns a stable local filesystem identifier for a directory.
func DirectoryFilesystemIdentity(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(
		name,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", err
	}
	index := uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
	return fmt.Sprintf("windows:%x:%x", uint64(info.VolumeSerialNumber), index), nil
}

func protectPrivateOpenFile(file *os.File, want fs.FileMode, directory bool) error {
	if file == nil || want.Perm()&0o077 != 0 {
		return nil
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return fmt.Errorf("resolve current user for private ACL: %w", err)
	}
	var pinner runtime.Pinner
	pinner.Pin(user.User.Sid)
	defer pinner.Unpin()
	inheritance := uint32(windows.NO_INHERITANCE)
	if directory {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("construct private ACL: %w", err)
	}
	// os.Root's metadata handle lacks WRITE_DAC. Reopen the same object by
	// handle, preserving identity instead of reopening an attacker-swappable path.
	reopened, err := reopenPrivateSecurityHandle(windows.Handle(file.Fd()), directory)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(reopened)
	if err := windows.SetSecurityInfo(reopened, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION,
		user.User.Sid, nil, acl, nil); err != nil {
		return fmt.Errorf("apply private ACL: %w", err)
	}
	return nil
}

func checkPrivateOpenFile(file *os.File, _ fs.FileMode, description string, singleLink bool) error {
	handle := windows.Handle(file.Fd())
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return err
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%s is a reparse point", description)
	}
	if singleLink && information.NumberOfLinks != 1 {
		return fmt.Errorf("%s has %d hard links; want 1", description, information.NumberOfLinks)
	}
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("inspect %s owner and ACL: %w", description, err)
	}
	if descriptor == nil {
		return fmt.Errorf("inspect %s owner and ACL: security descriptor is unavailable", description)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("inspect %s owner: %w", description, err)
	}
	if owner == nil {
		return fmt.Errorf("inspect %s owner: owner SID is unavailable", description)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("inspect current user for %s: %w", description, err)
	}
	if user == nil || user.User.Sid == nil || !owner.Equals(user.User.Sid) {
		return fmt.Errorf("%s is not owned by the current user", description)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return fmt.Errorf("inspect %s security descriptor control: %w", description, err)
	}
	if control&windows.SE_DACL_PRESENT == 0 || control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("%s discretionary ACL is absent or inherits access", description)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("inspect %s discretionary ACL: %w", description, err)
	}
	if dacl == nil || dacl.AceCount == 0 {
		return fmt.Errorf("%s must have an owner-only discretionary ACL", description)
	}
	effectiveOwner := false
	// Windows may split a directory's GENERIC_ALL inheritable grant into an
	// effective file-rights ACE and an inherit-only generic-rights ACE. Validate
	// every grant rather than requiring an encoding-specific single ACE.
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil || ace == nil {
			return fmt.Errorf("inspect %s discretionary ACL entry: %w", description, err)
		}
		entrySID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 || ace.Mask == 0 || entrySID == nil || !entrySID.Equals(user.User.Sid) {
			return fmt.Errorf("%s discretionary ACL grants access beyond the current user", description)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE == 0 {
			effectiveOwner = true
		}
	}
	if !effectiveOwner {
		return fmt.Errorf("%s lacks an effective owner grant", description)
	}
	return nil
}

var reopenFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")

func reopenPrivateSecurityHandle(original windows.Handle, directory bool) (windows.Handle, error) {
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	h, _, err := reopenFile.Call(uintptr(original), uintptr(windows.WRITE_DAC|windows.WRITE_OWNER|windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES), uintptr(windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE), uintptr(flags))
	if windows.Handle(h) == windows.InvalidHandle {
		// Some directory handles returned by os.Root cannot use ReOpenFile.
		// Resolve the held handle, open without following a final reparse point,
		// and prove the same file ID before changing security on the new handle.
		buffer := make([]uint16, 32768)
		n, nameErr := windows.GetFinalPathNameByHandle(original, &buffer[0], uint32(len(buffer)), 0)
		if nameErr != nil || n == 0 || n >= uint32(len(buffer)) {
			return 0, fmt.Errorf("resolve private object for ACL protection: %w", err)
		}
		reopened, openErr := windows.CreateFile(&buffer[0], windows.WRITE_DAC|windows.WRITE_OWNER|windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, flags, 0)
		if openErr != nil {
			return 0, fmt.Errorf("open held private object for ACL protection: %w", openErr)
		}
		h = uintptr(reopened)
	}
	var before, after windows.ByHandleFileInformation
	if e := windows.GetFileInformationByHandle(original, &before); e != nil {
		windows.CloseHandle(windows.Handle(h))
		return 0, e
	}
	if e := windows.GetFileInformationByHandle(windows.Handle(h), &after); e != nil {
		windows.CloseHandle(windows.Handle(h))
		return 0, e
	}
	if before.VolumeSerialNumber != after.VolumeSerialNumber || before.FileIndexHigh != after.FileIndexHigh || before.FileIndexLow != after.FileIndexLow {
		windows.CloseHandle(windows.Handle(h))
		return 0, fmt.Errorf("private filesystem object changed")
	}
	return windows.Handle(h), nil
}
