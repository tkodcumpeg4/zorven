//go:build windows

package main

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func isAdmin() bool { return windows.GetCurrentProcessToken().IsElevated() }

func isUACDeclined(err error) bool { return errors.Is(err, windows.ERROR_CANCELLED) }

// shellExecuteInfo, SHELLEXECUTEINFOW yapisidir (x/sys/windows ShellExecuteEx
// sunmaz).
type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIconMonitor uintptr
	hProcess     windows.Handle
}

const seeMaskNoCloseProcess = 0x00000040

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// runElevatedWait, exe'yi "runas" ile (UAC) verilen komut satiriyla baslatir,
// bitmesini bekler ve cikis kodunu doner. UAC reddedilirse ERROR_CANCELLED doner.
func runElevatedWait(exe, params string) (uint32, error) {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return 0, err
	}
	var p *uint16
	if params != "" {
		if p, err = windows.UTF16PtrFromString(params); err != nil {
			return 0, err
		}
	}
	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: p,
		nShow:        windows.SW_SHOWNORMAL,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	r, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return 0, errno
		}
		return 0, errors.New("ShellExecuteEx basarisiz")
	}
	if info.hProcess == 0 {
		return 0, errors.New("yukseltilmis surec tutamaci alinamadi")
	}
	defer windows.CloseHandle(info.hProcess)
	if ev, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil || ev != windows.WAIT_OBJECT_0 {
		if err == nil {
			err = errors.New("bekleme basarisiz")
		}
		return 0, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return 0, err
	}
	return code, nil
}
