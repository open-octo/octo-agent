//go:build darwin

package server

import "golang.org/x/sys/unix"

// OSVersion reports the host's macOS product version (e.g. "26.5.2"). The
// frontend gates window-chrome metrics on it: macOS 26 moved the traffic
// lights' centre downward for windows whose binary is stamped with the
// macOS 26 SDK (LC_BUILD_VERSION — the desktop build has been since
// v1.16.18), so the titlebar rows must align to a different axis there.
// LSMinimumSystemVersion is 12.0, well past kern.osproductversion's
// introduction (10.13.4), so the sysctl is always present; an error still
// degrades to "" and the frontend keeps the legacy axis.
func OSVersion() string {
	v, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return ""
	}
	return v
}
