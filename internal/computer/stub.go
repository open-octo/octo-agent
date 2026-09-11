//go:build !darwin || !cgo

package computer

func trusted() bool              { return false }
func screenCaptureAllowed() bool { return false }

func requestScreenCapture() {}

func requestAccessibility() {}

func screenSize() (float64, float64, error) { return 0, 0, ErrUnsupported }

func screenshot() ([]byte, error) { return nil, ErrUnsupported }

func moveTo(x, y float64) error { return ErrUnsupported }

func click(button string, x, y float64, clicks int) error { return ErrUnsupported }

func scroll(dx, dy float64) error { return ErrUnsupported }

func typeText(s string) error { return ErrUnsupported }

func press(keycode uint16, flags uint64) error { return ErrUnsupported }

func findWindow(owner string) (Window, error) { return Window{}, ErrUnsupported }

func clickPid(pid, winID int, button string, x, y float64, clicks int) error {
	return ErrUnsupported
}

func typeTextPid(pid int, s string) error { return ErrUnsupported }
