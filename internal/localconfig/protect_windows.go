package localconfig

import (
	"bytes"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

var protectedPrefix = []byte("MUP-DPAPI\x00")

func protectPayload(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty credential payload")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append(append([]byte{}, protectedPrefix...), unsafe.Slice(out.Data, out.Size)...), nil
}
func unprotectPayload(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, protectedPrefix) || len(data) == len(protectedPrefix) {
		return nil, errors.New("Windows credentials are not protected; sign in again using a new profile")
	}
	data = data[len(protectedPrefix):]
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte{}, unsafe.Slice(out.Data, out.Size)...), nil
}

// PrivateDirectory protects configuration, logs, and TLS caches with an inherited
// DACL allowing only this Windows user and SYSTEM; Unix modes do not do that.
func PrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
