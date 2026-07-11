//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// osLang returns the user's default locale name from Windows (e.g. "zh-CN",
// "en-US") via GetUserDefaultLocaleName. Windows GUI apps don't get LANG set,
// so this is the reliable source. Empty on failure.
func osLang() string {
	const localeNameMaxLength = 85 // LOCALE_NAME_MAX_LENGTH
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetUserDefaultLocaleName")
	buf := make([]uint16, localeNameMaxLength)
	n, _, _ := proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}
