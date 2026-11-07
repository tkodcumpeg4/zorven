//go:build windows

package main

import (
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// shellExecuteInfo, SHELLEXECUTEINFOW (x/sys/windows bunu sunmaz).
type shellExecuteInfo struct {
	cbSize         uint32
	fMask          uint32
	hwnd           uintptr
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       uintptr
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      uintptr
	dwHotKey       uint32
	hIconOrMonitor uintptr
	hProcess       windows.Handle
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
)

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// currentUserSID, bu sureci calistiran kullanicinin SID'i (okunamazsa bos).
func currentUserSID() string {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return ""
	}
	return u.User.Sid.String()
}

// runElevatedWait, exe'yi UAC ("runas") ile baslatir, bitmesini bekler ve cikis
// kodunu doner. Kullanici onayi reddederse hata doner.
func runElevatedWait(exe, params string, timeout time.Duration) (int, error) {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return -1, err
	}
	var args *uint16
	if params != "" {
		if args, err = windows.UTF16PtrFromString(params); err != nil {
			return -1, err
		}
	}
	sei := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsync,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: args,
		nShow:        windows.SW_HIDE,
	}
	sei.cbSize = uint32(unsafe.Sizeof(sei))
	r, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&sei)))
	if r == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return -1, errors.New(tm("net.adminDenied"))
		}
		return -1, callErr
	}
	if sei.hProcess == 0 {
		return -1, errors.New("no process handle")
	}
	defer windows.CloseHandle(sei.hProcess)
	ev, err := windows.WaitForSingleObject(sei.hProcess, uint32(timeout.Milliseconds()))
	if err != nil {
		return -1, err
	}
	if ev == uint32(windows.WAIT_TIMEOUT) {
		return -1, errors.New(tm("upd.helperTimeout"))
	}
	var code uint32
	if err := windows.GetExitCodeProcess(sei.hProcess, &code); err != nil {
		return -1, err
	}
	return int(code), nil
}

// waitPIDExit, pid'li surecin bitmesini en cok timeout kadar bekler.
func waitPIDExit(pid int, timeout time.Duration) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return // surec zaten yok
	}
	defer windows.CloseHandle(h)
	_, _ = windows.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
}
