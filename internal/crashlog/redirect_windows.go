package crashlog

import (
	"log"
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
	// Only Windows needs this. The stdlib logger captured os.Stderr by value at
	// init (log.std), and slog's default handler goes through it, so replacing
	// the variable leaves both writing to the handle we just redirected away
	// from — while on the dup platforms they keep working untouched, because
	// there it's the descriptor under the unchanged os.File that moved. Without
	// it, everything logged before setupHubLog switches to serve.log would be
	// lost on the one platform that has no console to lose it to.
	log.SetOutput(f)
	return nil
}
