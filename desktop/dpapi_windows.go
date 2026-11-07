//go:build windows

package main

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// dpapi, Windows DPAPI (CryptProtectData) ile kullanici-baglamli sifreleme:
// token yalniz bu Windows kullanicisi tarafindan cozulebilir.
type dpapi struct{}

func platformTokenProtector() tokenProtect { return dpapi{} }

func blobBytes(out windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...)
}

func (dpapi) Protect(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("empty")
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return blobBytes(out), nil
}

func (dpapi) Unprotect(enc []byte) ([]byte, error) {
	if len(enc) == 0 {
		return nil, errors.New("empty")
	}
	in := windows.DataBlob{Size: uint32(len(enc)), Data: &enc[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return blobBytes(out), nil
}
