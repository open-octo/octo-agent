package crashlog

import (
	"os"

	"golang.org/x/sys/windows"
)

// redirectStderr points STD_ERROR_HANDLE at f. The Go runtime resolves that
// handle with a fresh GetStdHandle call on every write to fd 2 (runtime.write1
// in os_windows.go), so replacing it here captures every panic trace written
// after this point — including in a `-H windowsgui` build, where the inherited
// handle is invalid and those writes go nowhere at all. os.Stderr is swapped
// too, for Go code that writes through it rather than to the descriptor — which
// is also what keeps f alive for the life of the process.
func redirectStderr(f *os.File) error {
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd())); err != nil {
		return err
	}
	os.Stderr = f
	return nil
}
