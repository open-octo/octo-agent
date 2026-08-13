//go:build !windows && !darwin && !linux

package crashlog

import (
	"errors"
	"os"
	"runtime"
)

// redirectStderr is unimplemented outside the three platforms the desktop app
// ships on. Install reports the error and leaves the default stderr alone.
func redirectStderr(*os.File) error {
	return errors.New("crashlog: stderr redirection unsupported on " + runtime.GOOS)
}
