//go:build windows

package pathx

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrivateWindowsDACLValidation(t *testing.T) {
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := ProtectPrivateRoot(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckPrivateRoot(root, 0o700, "owner-only root"); err != nil {
		t.Fatalf("protected owner-only root rejected: %v", err)
	}

	file, err := root.OpenFile("private.json", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := ProtectPrivateOpenFile(file, 0o600); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckPrivateFile(root, "private.json", 0o600, "owner-only file"); err != nil {
		t.Fatalf("protected owner-only file rejected: %v", err)
	}

	world, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	var pinner runtime.Pinner
	pinner.Pin(world)
	pinner.Pin(user.User.Sid)
	defer pinner.Unpin()
	permissive, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		windowsAccess(user.User.Sid, windows.GENERIC_ALL, windows.NO_INHERITANCE),
		windowsAccess(world, windows.GENERIC_ALL, windows.NO_INHERITANCE),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { setFileDACL(t, filepath.Join(rootPath, "private.json"), permissive, false) })
	setFileDACL(t, filepath.Join(rootPath, "private.json"), permissive, false)
	if _, err := CheckPrivateFile(root, "private.json", 0o600, "permissive file"); err == nil {
		t.Fatal("owner-created file granting Everyone access was accepted as private")
	}

	setFileDACL(t, filepath.Join(rootPath, "private.json"), nil, false)
	if _, err := CheckPrivateFile(root, "private.json", 0o600, "null-DACL file"); err == nil {
		t.Fatal("null DACL was accepted as private")
	}

	emptyDescriptor, err := windows.SecurityDescriptorFromString("D:P")
	if err != nil {
		t.Fatal(err)
	}
	empty, _, err := emptyDescriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	setFileDACL(t, filepath.Join(rootPath, "private.json"), empty, false)
	if _, err := CheckPrivateFile(root, "private.json", 0o600, "empty-DACL file"); err == nil {
		t.Fatal("empty DACL was accepted as usable private state")
	}
}

func TestPrivateWindowsDACLRejectsInheritedAccess(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	world, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		t.Fatal(err)
	}
	var pinner runtime.Pinner
	pinner.Pin(world)
	defer pinner.Unpin()
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		windowsAccess(world, windows.GENERIC_ALL, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	setFileDACL(t, parent, acl, false)
	child := filepath.Join(parent, "inherited")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(child)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := CheckPrivateRoot(root, 0o700, "inherited root"); err == nil {
		t.Fatal("inherited Everyone DACL was accepted as private")
	}
}

func windowsAccess(sid *windows.SID, mask windows.ACCESS_MASK, inheritance uint32) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: mask,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

func setFileDACL(t *testing.T, path string, acl *windows.ACL, unprotect bool) {
	t.Helper()
	information := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if unprotect {
		information = windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, information, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}
