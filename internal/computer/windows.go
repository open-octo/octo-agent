//go:build windows

package computer

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Pixel channel for Windows: GDI screen capture plus SendInput, all through
// plain Win32 so the CGO_ENABLED=0 release cross-builds ship it unchanged.
// Constants and struct layouts below follow winuser.h / wingdi.h / windef.h.

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procSendInput                     = user32.NewProc("SendInput")
	procSetCursorPos                  = user32.NewProc("SetCursorPos")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procGetWindowRect                 = user32.NewProc("GetWindowRect")
	procIsIconic                      = user32.NewProc("IsIconic")
	procGetWindowTextW                = user32.NewProc("GetWindowTextW")
	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procVkKeyScanW                    = user32.NewProc("VkKeyScanW")

	procBitBlt                 = gdi32.NewProc("BitBlt")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfLeftDown  = 0x0002
	mouseeventfLeftUp    = 0x0004
	mouseeventfRightDown = 0x0008
	mouseeventfRightUp   = 0x0010
	mouseeventfWheel     = 0x0800
	mouseeventfHWheel    = 0x1000

	keyeventfExtendedKey = 0x0001
	keyeventfKeyUp       = 0x0002
	keyeventfUnicode     = 0x0004

	wheelDelta = 120

	smCxScreen = 0
	smCyScreen = 1

	srccopy      = 0x00CC0020
	captureblt   = 0x40000000
	biRGB        = 0
	dibRGBColors = 0

	vkBack    = 0x08
	vkTab     = 0x09
	vkReturn  = 0x0D
	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12 // Alt
	vkEscape  = 0x1B
	vkSpace   = 0x20
	vkPrior   = 0x21 // Page Up
	vkNext    = 0x22 // Page Down
	vkEnd     = 0x23
	vkHome    = 0x24
	vkLeft    = 0x25
	vkUp      = 0x26
	vkRight   = 0x27
	vkDown    = 0x28
	vkDelete  = 0x2E
	vkLWin    = 0x5B
	vkF1      = 0x70 // F1..F12 are contiguous through 0x7B

	// minWindowEdge filters out tool palettes and hidden helper windows when
	// resolving an app's main window, same threshold as the macOS path.
	minWindowEdge = 50
)

// dpiAwarenessContextPerMonitorAwareV2 is DPI_AWARENESS_CONTEXT(-4).
const dpiAwarenessContextPerMonitorAwareV2 = ^uintptr(3)

// mouseInput mirrors MOUSEINPUT.
type mouseInput struct {
	dx, dy    int32
	mouseData uint32
	dwFlags   uint32
	time      uint32
	extraInfo uintptr
}

// keybdInput mirrors KEYBDINPUT; it overlays the same union bytes as
// mouseInput inside input.
type keybdInput struct {
	wVk, wScan uint16
	dwFlags    uint32
	time       uint32
	extraInfo  uintptr
}

// input mirrors INPUT: a DWORD type followed by a union whose largest member
// is MOUSEINPUT. The union holds a ULONG_PTR so it is pointer-aligned, hence
// the padding after type on 64-bit.
type input struct {
	typ uint32
	_   [unsafe.Sizeof(uintptr(0)) - 4]byte
	mi  mouseInput
}

func (in *input) keyboard() *keybdInput { return (*keybdInput)(unsafe.Pointer(&in.mi)) }

// bitmapInfoHeader mirrors BITMAPINFOHEADER; bitmapInfo mirrors BITMAPINFO
// (header plus a one-entry colour table that 32-bit BI_RGB never uses).
type bitmapInfoHeader struct {
	size          uint32
	width, height int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       uint32
	clrImportant  uint32
}

type bitmapInfo struct {
	header bitmapInfoHeader
	colors [1]uint32
}

// ensureDPIAware opts the process into per-monitor-v2 DPI awareness once, so
// GetSystemMetrics, GetWindowRect, SetCursorPos and the GDI capture all speak
// physical pixels. The desktop shell already declares this in its manifest
// (a second call fails harmlessly); the CLI has no manifest and needs it.
var dpiOnce sync.Once

func ensureDPIAware() {
	dpiOnce.Do(func() {
		if procSetProcessDpiAwarenessContext.Find() == nil {
			procSetProcessDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorAwareV2)
		}
	})
}

// Windows has no Accessibility / Screen Recording grants: input synthesis and
// capture are available to any interactive process. (UIPI still blocks input
// into windows of a higher integrity level — an elevated app cannot be
// driven unless octo runs elevated too; that surfaces as SendInput failing.)
func trusted() bool              { return true }
func screenCaptureAllowed() bool { return true }
func requestScreenCapture()      {}
func requestAccessibility()      {}

func screenSize() (float64, float64, error) {
	ensureDPIAware()
	w, _, _ := procGetSystemMetrics.Call(smCxScreen)
	h, _, _ := procGetSystemMetrics.Call(smCyScreen)
	if w == 0 || h == 0 {
		return 0, 0, fmt.Errorf("computer: GetSystemMetrics reported a %dx%d primary display", w, h)
	}
	return float64(int32(w)), float64(int32(h)), nil
}

// screenshot captures the primary display through GDI (BitBlt into a
// compatible bitmap, GetDIBits as top-down 32-bit BGRA) and encodes PNG.
func screenshot() ([]byte, error) {
	w, h, err := screenSize()
	if err != nil {
		return nil, err
	}
	width, height := int32(w), int32(h)

	screen, _, _ := procGetDC.Call(0)
	if screen == 0 {
		return nil, fmt.Errorf("computer: GetDC(screen) failed")
	}
	defer procReleaseDC.Call(0, screen)

	mem, _, _ := procCreateCompatibleDC.Call(screen)
	if mem == 0 {
		return nil, fmt.Errorf("computer: CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(mem)

	bmp, _, _ := procCreateCompatibleBitmap.Call(screen, uintptr(width), uintptr(height))
	if bmp == 0 {
		return nil, fmt.Errorf("computer: CreateCompatibleBitmap(%dx%d) failed", width, height)
	}
	defer procDeleteObject.Call(bmp)

	old, _, _ := procSelectObject.Call(mem, bmp)
	ok, _, e := procBitBlt.Call(mem, 0, 0, uintptr(width), uintptr(height), screen, 0, 0, srccopy|captureblt)
	// GetDIBits requires the bitmap NOT to be selected into any DC, so
	// restore the DC's original bitmap before reading the pixels.
	procSelectObject.Call(mem, old)
	if ok == 0 {
		return nil, fmt.Errorf("computer: BitBlt failed: %v", e)
	}

	// Negative height requests a top-down DIB so row 0 is the top of the
	// screen; 32 bpp BI_RGB yields BGRA with no stride padding.
	bi := bitmapInfo{header: bitmapInfoHeader{
		size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		width:       width,
		height:      -height,
		planes:      1,
		bitCount:    32,
		compression: biRGB,
	}}
	buf := make([]byte, int(width)*int(height)*4)
	lines, _, e := procGetDIBits.Call(mem, bmp, 0, uintptr(height), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), dibRGBColors)
	if int32(lines) == 0 { // int return: only the low 32 bits are defined
		return nil, fmt.Errorf("computer: GetDIBits failed: %v", e)
	}

	img := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	for i := 0; i+3 < len(buf); i += 4 {
		img.Pix[i+0] = buf[i+2] // R
		img.Pix[i+1] = buf[i+1] // G
		img.Pix[i+2] = buf[i+0] // B
		img.Pix[i+3] = 0xFF
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, fmt.Errorf("computer: encoding capture: %w", err)
	}
	return out.Bytes(), nil
}

func sendInput(ins []input) error {
	if len(ins) == 0 {
		return nil
	}
	n, _, e := procSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(input{}))
	if int(n) != len(ins) {
		return fmt.Errorf("computer: SendInput delivered %d of %d events: %v (an elevated target window blocks input from a non-elevated octo)", n, len(ins), e)
	}
	return nil
}

func setCursor(x, y float64) error {
	ensureDPIAware()
	if ok, _, e := procSetCursorPos.Call(uintptr(int32(x)), uintptr(int32(y))); ok == 0 {
		return fmt.Errorf("computer: SetCursorPos(%.0f, %.0f) failed: %v", x, y, e)
	}
	return nil
}

func moveTo(x, y float64) error { return setCursor(x, y) }

func click(button string, x, y float64, clicks int) error {
	if err := setCursor(x, y); err != nil {
		return err
	}
	down, up := uint32(mouseeventfLeftDown), uint32(mouseeventfLeftUp)
	if button == "right" {
		down, up = mouseeventfRightDown, mouseeventfRightUp
	}
	ins := make([]input, 0, clicks*2)
	for i := 0; i < clicks; i++ {
		ins = append(ins,
			input{typ: inputMouse, mi: mouseInput{dwFlags: down}},
			input{typ: inputMouse, mi: mouseInput{dwFlags: up}},
		)
	}
	return sendInput(ins)
}

// scroll posts wheel events at the current cursor position. A positive
// WHEEL_DELTA rotates the wheel away from the user (content scrolls up), so
// the sign of dy is flipped to keep the contract "positive dy = scroll down".
func scroll(dx, dy float64) error {
	var ins []input
	if dy != 0 {
		ins = append(ins, input{typ: inputMouse, mi: mouseInput{dwFlags: mouseeventfWheel, mouseData: uint32(int32(-dy * wheelDelta))}})
	}
	if dx != 0 {
		ins = append(ins, input{typ: inputMouse, mi: mouseInput{dwFlags: mouseeventfHWheel, mouseData: uint32(int32(dx * wheelDelta))}})
	}
	return sendInput(ins)
}

// typeText injects each UTF-16 unit as a KEYEVENTF_UNICODE press/release,
// independent of keyboard layout. Line breaks become Enter presses: Win32
// edit controls act on VK_RETURN, not on a bare U+000A character, so typing
// "\n" literally would collapse lines that macOS keeps. Batches stay small so
// one lost batch does not drop a whole paragraph.
func typeText(s string) error {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if i > 0 {
			if err := press("enter", 0); err != nil {
				return err
			}
		}
		if err := typeUnits(line); err != nil {
			return err
		}
	}
	return nil
}

func typeUnits(s string) error {
	units := utf16.Encode([]rune(s))
	const batch = 64
	for start := 0; start < len(units); start += batch {
		end := start + batch
		if end > len(units) {
			end = len(units)
		}
		ins := make([]input, 0, (end-start)*2)
		for _, u := range units[start:end] {
			var d, up input
			d.typ, up.typ = inputKeyboard, inputKeyboard
			kd, ku := d.keyboard(), up.keyboard()
			kd.wScan, kd.dwFlags = u, keyeventfUnicode
			ku.wScan, ku.dwFlags = u, keyeventfUnicode|keyeventfKeyUp
			ins = append(ins, d, up)
		}
		if err := sendInput(ins); err != nil {
			return err
		}
	}
	return nil
}

// winNamedKeys maps canonical key names (see keyAliases) to virtual-key
// codes. ext marks keys on the extended part of the enhanced keyboard, which
// need KEYEVENTF_EXTENDEDKEY for apps that distinguish them from the numpad.
var winNamedKeys = map[string]struct {
	vk  uint16
	ext bool
}{
	"enter": {vkReturn, false}, "tab": {vkTab, false}, "space": {vkSpace, false},
	"backspace": {vkBack, false}, "delete": {vkDelete, true}, "escape": {vkEscape, false},
	"left": {vkLeft, true}, "right": {vkRight, true}, "down": {vkDown, true}, "up": {vkUp, true},
	"home": {vkHome, true}, "end": {vkEnd, true}, "pageup": {vkPrior, true}, "pagedown": {vkNext, true},
	"f1": {vkF1, false}, "f2": {vkF1 + 1, false}, "f3": {vkF1 + 2, false}, "f4": {vkF1 + 3, false},
	"f5": {vkF1 + 4, false}, "f6": {vkF1 + 5, false}, "f7": {vkF1 + 6, false}, "f8": {vkF1 + 7, false},
	"f9": {vkF1 + 8, false}, "f10": {vkF1 + 9, false}, "f11": {vkF1 + 10, false}, "f12": {vkF1 + 11, false},
}

// winKeyCode resolves a canonical key name to a virtual-key code. Single
// characters go through VkKeyScanW so the current keyboard layout decides;
// its high byte reports the shift state needed to produce the character
// (bit 0 = Shift), which is folded into needShift.
func winKeyCode(key string) (vk uint16, ext, needShift bool, err error) {
	if k, ok := winNamedKeys[key]; ok {
		return k.vk, k.ext, false, nil
	}
	if len(key) != 1 {
		return 0, false, false, fmt.Errorf("computer: key %q has no Windows virtual-key code", key)
	}
	r, _, _ := procVkKeyScanW.Call(uintptr(key[0]))
	scan := int16(r)
	if scan == -1 {
		return 0, false, false, fmt.Errorf("computer: character %q is not on the current keyboard layout", key)
	}
	return uint16(scan & 0xFF), false, scan>>8&1 != 0, nil
}

func keyEvent(vk uint16, flags uint32) input {
	var in input
	in.typ = inputKeyboard
	k := in.keyboard()
	k.wVk, k.dwFlags = vk, flags
	return in
}

// press chords the modifiers around one key: modifiers down, key down/up,
// modifiers up in reverse. "cmd" maps to the Windows key.
func press(key string, flags uint64) error {
	vk, ext, needShift, err := winKeyCode(key)
	if err != nil {
		return err
	}
	var mods []uint16
	if flags&flagShift != 0 || needShift {
		mods = append(mods, vkShift)
	}
	if flags&flagControl != 0 {
		mods = append(mods, vkControl)
	}
	if flags&flagOption != 0 {
		mods = append(mods, vkMenu)
	}
	if flags&flagCommand != 0 {
		mods = append(mods, vkLWin)
	}
	var keyFlags uint32
	if ext {
		keyFlags = keyeventfExtendedKey
	}
	ins := make([]input, 0, len(mods)*2+2)
	for _, m := range mods {
		ins = append(ins, keyEvent(m, 0))
	}
	ins = append(ins, keyEvent(vk, keyFlags), keyEvent(vk, keyFlags|keyeventfKeyUp))
	for i := len(mods) - 1; i >= 0; i-- {
		ins = append(ins, keyEvent(mods[i], keyeventfKeyUp))
	}
	return sendInput(ins)
}

// Per-process event injection (CGEventPostToPid) has no Windows counterpart
// in this substrate; the tool layer never calls these.
func clickPid(pid, winID int, button string, x, y float64, clicks int) error { return ErrUnsupported }
func typeTextPid(pid int, s string) error                                    { return ErrUnsupported }

// enumWin carries state into the EnumWindows callback. NewCallback slots are
// a scarce per-process resource, so the callback is created once and the
// query lives in this mutex-guarded struct instead of a closure.
var enumWin struct {
	sync.Mutex
	once    sync.Once
	cb      uintptr
	owner   string // match target when wantPid == 0
	byTitle bool   // owner matches the window title (second pass) instead of the executable
	wantPid uint32
	found   bool
	win     Window
}

func enumWindowsProc(hwnd uintptr, _ uintptr) uintptr {
	const next, stop = 1, 0
	h := windows.HWND(hwnd)
	if !windows.IsWindowVisible(h) {
		return next
	}
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
		return next
	}
	var rc windows.Rect
	if ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc))); ok == 0 {
		return next
	}
	w, ht := rc.Right-rc.Left, rc.Bottom-rc.Top
	if w < minWindowEdge || ht < minWindowEdge {
		return next
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(h, &pid); err != nil {
		return next
	}
	if enumWin.wantPid != 0 {
		if pid != enumWin.wantPid {
			return next
		}
	} else if !ownerMatches(hwnd, pid, enumWin.owner, enumWin.byTitle) {
		return next
	}
	enumWin.found = true
	enumWin.win = Window{PID: int(pid), ID: int(hwnd), X: float64(rc.Left), Y: float64(rc.Top), W: float64(w), H: float64(ht)}
	return stop
}

// ownerMatches compares owner against the executable name (with or without
// ".exe", case-insensitive) or, when byTitle is set, against the window title
// as a case-insensitive substring. The two are separate passes so that an
// exact executable match anywhere in Z order beats a title that merely
// mentions the name (a browser tab titled "… notepad …" above Notepad).
func ownerMatches(hwnd uintptr, pid uint32, owner string, byTitle bool) bool {
	want := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(owner)), ".exe")
	if want == "" {
		return false
	}
	if !byTitle {
		exe := processBaseName(pid)
		return exe != "" && strings.TrimSuffix(strings.ToLower(exe), ".exe") == want
	}
	title := make([]uint16, 512)
	r, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
	n := int(int32(r)) // int return: only the low 32 bits are defined
	if n <= 0 {
		return false
	}
	return strings.Contains(strings.ToLower(windows.UTF16ToString(title[:n])), want)
}

func processBaseName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH*2)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	full := windows.UTF16ToString(buf[:size])
	if i := strings.LastIndexAny(full, `\/`); i >= 0 {
		return full[i+1:]
	}
	return full
}

// enumerate runs one EnumWindows pass with the given query and returns the
// first (top-most in Z order) match.
func enumerate(owner string, byTitle bool, pid uint32) (Window, bool) {
	ensureDPIAware()
	enumWin.Lock()
	defer enumWin.Unlock()
	enumWin.once.Do(func() { enumWin.cb = windows.NewCallback(enumWindowsProc) })
	enumWin.owner, enumWin.byTitle, enumWin.wantPid = owner, byTitle, pid
	enumWin.found, enumWin.win = false, Window{}
	// EnumWindows reports failure when the callback stops the enumeration
	// early, which is exactly the found case — so the error is ignored and
	// the found flag decides.
	_ = windows.EnumWindows(enumWin.cb, nil)
	return enumWin.win, enumWin.found
}

func findWindow(owner string) (Window, error) {
	if strings.TrimSpace(owner) == "" {
		return Window{}, fmt.Errorf("computer: empty app name")
	}
	// Executable name first across the whole Z order, title substring only
	// when no process is called that.
	if w, ok := enumerate(owner, false, 0); ok {
		return w, nil
	}
	if w, ok := enumerate(owner, true, 0); ok {
		return w, nil
	}
	return Window{}, fmt.Errorf("computer: no on-screen window found for %q — is the app open and not minimized? (match by executable name without .exe, or by a window-title substring)", owner)
}

// windowForPid finds the top-most visible window owned by pid; the AX
// channel needs a window handle where the shared API carries a pid.
func windowForPid(pid int) (Window, error) {
	w, ok := enumerate("", false, uint32(pid))
	if !ok {
		return Window{}, fmt.Errorf("computer: pid %d has no on-screen window", pid)
	}
	return w, nil
}
