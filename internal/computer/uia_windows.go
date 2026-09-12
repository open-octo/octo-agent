//go:build windows

package computer

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Accessibility channel for Windows: UI Automation (UIA) driven over raw COM
// vtables — no cgo, no COM helper dependency. GUIDs, pattern / control-type
// ids and vtable slot numbers below come from uiautomationclient.h.
//
// One limitation is structural: IUIAutomationRangeValuePattern::SetValue
// takes a double, which the Windows x64 / arm64 calling conventions pass in a
// floating-point register that syscall.SyscallN cannot load. Numeric targets
// (sliders, spinners) are therefore set through the string-typed
// IUIAutomationValuePattern::SetValue or, failing that, the MSAA bridge
// IUIAutomationLegacyIAccessiblePattern::SetValue — both accept text.

var (
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	oleaut32 = windows.NewLazySystemDLL("oleaut32.dll")

	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procSysAllocString   = oleaut32.NewProc("SysAllocString")
	procSysFreeString    = oleaut32.NewProc("SysFreeString")
	procSysStringLen     = oleaut32.NewProc("SysStringLen")
)

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic("computer: bad GUID literal " + s)
	}
	return g
}

var (
	clsidCUIAutomation                       = mustGUID("{ff48dba4-60ef-4201-aa87-54103eef594e}")
	iidIUIAutomation                         = mustGUID("{30cbe57d-d9d0-452a-ab13-7ac5ac4825ee}")
	iidIUIAutomationInvokePattern            = mustGUID("{fb377fbe-8ea6-46d5-9c73-6499642d3059}")
	iidIUIAutomationValuePattern             = mustGUID("{a94cd8b1-0844-4cd6-9d2d-640537ab39e9}")
	iidIUIAutomationTogglePattern            = mustGUID("{94cf8058-9b8d-4ab9-8bfd-4cd0a33c8c70}")
	iidIUIAutomationLegacyIAccessiblePattern = mustGUID("{828055ad-355b-4435-86d5-3b51c14a9b1b}")
)

const (
	uiaInvokePatternId            = 10000
	uiaValuePatternId             = 10002
	uiaTogglePatternId            = 10015
	uiaLegacyIAccessiblePatternId = 10018

	// Control type ids are contiguous from UIA_ButtonControlTypeId.
	uiaFirstControlTypeId = 50000

	// HRESULTs (winerror.h).
	sFalse          = 0x00000001
	rpcEChangedMode = 0x80010106

	// Vtable slots, 0-based, IUnknown occupying 0..2.
	slotQueryInterface = 0
	slotRelease        = 2

	slotAutomationElementFromHandle    = 6
	slotAutomationGetControlViewWalker = 14

	slotElementGetCurrentPattern      = 16
	slotElementGetCurrentControlType  = 21
	slotElementGetCurrentName         = 23
	slotElementGetCurrentHelpText     = 31
	slotElementGetCurrentBoundingRect = 43
	slotWalkerGetFirstChildElement    = 4
	slotWalkerGetNextSiblingElement   = 6
	slotInvokePatternInvoke           = 3
	slotValuePatternSetValue          = 3
	slotTogglePatternToggle           = 3
	slotLegacyPatternDoDefaultAction  = 4
	slotLegacyPatternSetValue         = 5
)

// uiaControlTypeNames indexes control type id − uiaFirstControlTypeId.
var uiaControlTypeNames = [...]string{
	"Button", "Calendar", "CheckBox", "ComboBox", "Edit", "Hyperlink", "Image",
	"ListItem", "List", "Menu", "MenuBar", "MenuItem", "ProgressBar", "RadioButton",
	"ScrollBar", "Slider", "Spinner", "StatusBar", "Tab", "TabItem", "Text", "ToolBar",
	"ToolTip", "Tree", "TreeItem", "Custom", "Group", "Thumb", "DataGrid", "DataItem",
	"Document", "SplitButton", "Window", "Pane", "Header", "HeaderItem", "Table",
	"TitleBar", "Separator", "SemanticZoom", "AppBar",
}

func controlTypeName(id int32) string {
	i := int(id) - uiaFirstControlTypeId
	if i >= 0 && i < len(uiaControlTypeNames) {
		return uiaControlTypeNames[i]
	}
	return fmt.Sprintf("ControlType%d", id)
}

// comObj is a raw COM interface pointer.
type comObj uintptr

// call invokes vtable slot n with this as the first argument. The two
// dereferences go through the address of a Go variable so no uintptr is ever
// converted to unsafe.Pointer (the pattern go vet's unsafeptr check flags).
func (o comObj) call(slot int, args ...uintptr) uintptr {
	objWords := *(**[1]uintptr)(unsafe.Pointer(&o))
	vtbl := *(**[128]uintptr)(unsafe.Pointer(&objWords[0]))
	r, _, _ := syscall.SyscallN(vtbl[slot], append([]uintptr{uintptr(o)}, args...)...)
	return r
}

func (o comObj) release() {
	if o != 0 {
		o.call(slotRelease)
	}
}

func (o comObj) queryInterface(iid *windows.GUID) comObj {
	var out uintptr
	if failed(o.call(slotQueryInterface, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))) {
		return 0
	}
	return comObj(out)
}

func failed(hr uintptr) bool { return int32(hr) < 0 }

func hresultErr(what string, hr uintptr) error {
	if !failed(hr) {
		// A success HRESULT with a null out-pointer — the API declined
		// without saying why (no element for that window, no walker).
		return fmt.Errorf("computer: %s returned nothing", what)
	}
	return fmt.Errorf("computer: %s failed (HRESULT 0x%08x)", what, uint32(hr))
}

// bstrToString reads a BSTR (UTF-16, length-prefixed) without freeing it.
func bstrToString(b uintptr) string {
	if b == 0 {
		return ""
	}
	r, _, _ := procSysStringLen.Call(b)
	n := uint32(r) // UINT return: only the low 32 bits are defined
	if n == 0 {
		return ""
	}
	p := *(**uint16)(unsafe.Pointer(&b))
	return windows.UTF16ToString(unsafe.Slice(p, n))
}

func freeBSTR(b uintptr) {
	if b != 0 {
		procSysFreeString.Call(b)
	}
}

// withCOM runs fn on a thread with COM initialised and a CUIAutomation
// instance in hand. COM apartments are per-thread, so the goroutine is
// pinned for the duration. S_FALSE means the thread was already initialised
// (still balanced with CoUninitialize); RPC_E_CHANGED_MODE means the thread
// is already in a different apartment mode, which UIA calls tolerate — use it
// as is and leave its lifetime to whoever set it up.
func withCOM(fn func(auto comObj) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	uninit := true
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
		switch {
		case errors.Is(err, syscall.Errno(sFalse)):
		case errors.Is(err, syscall.Errno(rpcEChangedMode)):
			uninit = false
		default:
			return fmt.Errorf("computer: CoInitializeEx: %w", err)
		}
	}
	if uninit {
		defer windows.CoUninitialize()
	}

	var raw uintptr
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidCUIAutomation)), 0, windows.CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&iidIUIAutomation)), uintptr(unsafe.Pointer(&raw)))
	if failed(hr) || raw == 0 {
		return hresultErr("CoCreateInstance(CUIAutomation)", hr)
	}
	auto := comObj(raw)
	defer auto.release()
	return fn(auto)
}

func elementFromHandle(auto comObj, hwnd uintptr) (comObj, error) {
	var el uintptr
	if hr := auto.call(slotAutomationElementFromHandle, hwnd, uintptr(unsafe.Pointer(&el))); failed(hr) || el == 0 {
		return 0, hresultErr("IUIAutomation::ElementFromHandle", hr)
	}
	return comObj(el), nil
}

func controlViewWalker(auto comObj) (comObj, error) {
	var w uintptr
	if hr := auto.call(slotAutomationGetControlViewWalker, uintptr(unsafe.Pointer(&w))); failed(hr) || w == 0 {
		return 0, hresultErr("IUIAutomation::get_ControlViewWalker", hr)
	}
	return comObj(w), nil
}

// firstChild / nextSibling return 0 when there is no such element; the
// caller owns (and must release) a non-zero result.
func firstChild(walker, el comObj) comObj {
	var out uintptr
	if failed(walker.call(slotWalkerGetFirstChildElement, uintptr(el), uintptr(unsafe.Pointer(&out)))) {
		return 0
	}
	return comObj(out)
}

func nextSibling(walker, el comObj) comObj {
	var out uintptr
	if failed(walker.call(slotWalkerGetNextSiblingElement, uintptr(el), uintptr(unsafe.Pointer(&out)))) {
		return 0
	}
	return comObj(out)
}

func elementString(el comObj, slot int) string {
	var b uintptr
	if failed(el.call(slot, uintptr(unsafe.Pointer(&b)))) {
		return ""
	}
	defer freeBSTR(b)
	return bstrToString(b)
}

func elementControlType(el comObj) int32 {
	var ct int32
	if failed(el.call(slotElementGetCurrentControlType, uintptr(unsafe.Pointer(&ct)))) {
		return 0
	}
	return ct
}

// elementPattern fetches a current pattern object and narrows it to iid.
// Returns 0 when the element does not support the pattern.
func elementPattern(el comObj, patternID uintptr, iid *windows.GUID) comObj {
	var unk uintptr
	if failed(el.call(slotElementGetCurrentPattern, patternID, uintptr(unsafe.Pointer(&unk)))) || unk == 0 {
		return 0
	}
	u := comObj(unk)
	defer u.release()
	return u.queryInterface(iid)
}

// uiaElement reads the digest fields of one element: control type as the
// role, Name as the title, HelpText as the description, bounding rectangle
// in physical pixels (the process is per-monitor DPI aware).
func uiaElement(el comObj, depth int) AXElement {
	e := AXElement{
		Depth:       depth,
		Role:        controlTypeName(elementControlType(el)),
		Title:       elementString(el, slotElementGetCurrentName),
		Description: elementString(el, slotElementGetCurrentHelpText),
	}
	var rc windows.Rect
	if !failed(el.call(slotElementGetCurrentBoundingRect, uintptr(unsafe.Pointer(&rc)))) {
		e.X, e.Y = float64(rc.Left), float64(rc.Top)
		e.W, e.H = float64(rc.Right-rc.Left), float64(rc.Bottom-rc.Top)
	}
	return e
}

// walkChildren visits el's children in order through walker, stopping when
// visit returns false or the fan-out cap is hit. Each child is released here.
func walkChildren(walker, el comObj, visit func(child comObj) bool) {
	child := firstChild(walker, el)
	for i := 0; child != 0; i++ {
		if i >= axMaxChildren || !visit(child) {
			child.release()
			return
		}
		next := nextSibling(walker, child)
		child.release()
		child = next
	}
}

// axWalk performs the same depth-first traversal axTree renders (from pid's
// top-most window, depth- and fan-out-capped) and calls visit for every
// element with its 0-based traversal index while the COM element is still
// alive. visit returning false stops the whole walk immediately — used by
// axActByIndex to act on one element without paying for the rest of a
// possibly-large tree.
func axWalk(pid, maxDepth int, visit func(index int, el comObj, e AXElement) bool) error {
	if maxDepth <= 0 {
		maxDepth = 12
	}
	win, err := windowForPid(pid)
	if err != nil {
		return err
	}
	return withCOM(func(auto comObj) error {
		root, err := elementFromHandle(auto, uintptr(win.ID))
		if err != nil {
			return err
		}
		defer root.release()
		walker, err := controlViewWalker(auto)
		if err != nil {
			return err
		}
		defer walker.release()

		index := 0
		stop := false
		var walk func(el comObj, depth int)
		walk = func(el comObj, depth int) {
			if stop || depth > maxDepth || index >= axMaxTotal {
				return
			}
			if !visit(index, el, uiaElement(el, depth)) {
				stop = true
				return
			}
			index++
			walkChildren(walker, el, func(child comObj) bool {
				walk(child, depth+1)
				return !stop
			})
		}
		walk(root, 0)
		return nil
	})
}

func axTree(pid, maxDepth int) ([]AXElement, error) {
	out := make([]AXElement, 0, 256)
	err := axWalk(pid, maxDepth, func(_ int, _ comObj, e AXElement) bool {
		out = append(out, e)
		return true
	})
	return out, err
}

// axActByIndex performs action on the element at the given 0-based
// traversal index — the same numbering axTree's returned slice uses, under
// the same pid + maxDepth. Id-addressed counterpart to axFindDo's role+label
// matching, for elements ax_tree shows with an empty or duplicate label.
// Returns the matched element's digest alongside any error so the caller can
// echo back what it actually hit.
func axActByIndex(pid, maxDepth, target int, action func(el comObj) error) (AXElement, error) {
	var found bool
	var matched AXElement
	var actErr error
	err := axWalk(pid, maxDepth, func(i int, el comObj, e AXElement) bool {
		if i == target {
			found = true
			matched = e
			actErr = action(el)
			return false
		}
		return true
	})
	if err != nil {
		return AXElement{}, err
	}
	if !found {
		return AXElement{}, fmt.Errorf("computer: no element with id e%d (re-dump ax_tree — the tree may have changed, or max_depth differs from the dump that produced this id)", target)
	}
	return matched, actErr
}

// axFindDo mirrors the macOS search: an exact-label pass wins over a
// substring pass, each with its own traversal budget, and action runs on the
// first match while the element is still alive.
func axFindDo(pid int, role, contains string, action func(el comObj) error) error {
	win, err := windowForPid(pid)
	if err != nil {
		return err
	}
	c := strings.TrimSpace(contains)
	return withCOM(func(auto comObj) error {
		root, err := elementFromHandle(auto, uintptr(win.ID))
		if err != nil {
			return err
		}
		defer root.release()
		walker, err := controlViewWalker(auto)
		if err != nil {
			return err
		}
		defer walker.release()

		var matched bool
		var actErr error
		search := func(pred func(AXElement) bool) bool {
			visited := 0
			var walk func(el comObj, depth int) bool // true = stop
			walk = func(el comObj, depth int) bool {
				if matched || visited >= axMaxTotal {
					return true
				}
				visited++
				if pred(uiaElement(el, depth)) {
					matched = true
					actErr = action(el)
					return true
				}
				if depth >= 15 {
					return false
				}
				stopped := false
				walkChildren(walker, el, func(child comObj) bool {
					stopped = walk(child, depth+1)
					return !stopped
				})
				return stopped
			}
			walk(root, 0)
			return matched
		}

		if !search(func(e AXElement) bool { return MatchAXExact(e, role, c) }) {
			search(func(e AXElement) bool { return MatchAX(e, role, c) })
		}
		if !matched {
			return fmt.Errorf("computer: no UI Automation element matching role=%q contains=%q", role, contains)
		}
		return actErr
	})
}

// doAXPress performs an already-located element's Invoke/Toggle/default
// action; shared by the label-matched (axPress) and id-addressed
// (axPressByID) paths.
func doAXPress(el comObj) error {
	if p := elementPattern(el, uiaInvokePatternId, &iidIUIAutomationInvokePattern); p != 0 {
		defer p.release()
		if hr := p.call(slotInvokePatternInvoke); failed(hr) {
			return hresultErr("IUIAutomationInvokePattern::Invoke", hr)
		}
		return nil
	}
	if p := elementPattern(el, uiaTogglePatternId, &iidIUIAutomationTogglePattern); p != 0 {
		defer p.release()
		if hr := p.call(slotTogglePatternToggle); failed(hr) {
			return hresultErr("IUIAutomationTogglePattern::Toggle", hr)
		}
		return nil
	}
	if p := elementPattern(el, uiaLegacyIAccessiblePatternId, &iidIUIAutomationLegacyIAccessiblePattern); p != 0 {
		defer p.release()
		if hr := p.call(slotLegacyPatternDoDefaultAction); failed(hr) {
			return hresultErr("IUIAutomationLegacyIAccessiblePattern::DoDefaultAction", hr)
		}
		return nil
	}
	return fmt.Errorf("computer: element supports neither Invoke, Toggle nor a default action — click it by coordinate instead")
}

func axPress(pid int, role, contains string) error {
	return axFindDo(pid, role, contains, doAXPress)
}

func axPressByID(pid, maxDepth, id int) (AXElement, error) {
	return axActByIndex(pid, maxDepth, id, doAXPress)
}

// doAXSetValue is axSetValue/axSetValueByID's shared element-level action.
func doAXSetValue(el comObj, value string) error {
	wide, err := windows.UTF16PtrFromString(value)
	if err != nil {
		return fmt.Errorf("computer: value %q: %w", value, err)
	}
	if p := elementPattern(el, uiaValuePatternId, &iidIUIAutomationValuePattern); p != 0 {
		defer p.release()
		b, _, _ := procSysAllocString.Call(uintptr(unsafe.Pointer(wide)))
		defer freeBSTR(b)
		if hr := p.call(slotValuePatternSetValue, b); failed(hr) {
			return hresultErr(fmt.Sprintf("IUIAutomationValuePattern::SetValue(%q)", value), hr)
		}
		return nil
	}
	if p := elementPattern(el, uiaLegacyIAccessiblePatternId, &iidIUIAutomationLegacyIAccessiblePattern); p != 0 {
		defer p.release()
		if hr := p.call(slotLegacyPatternSetValue, uintptr(unsafe.Pointer(wide))); failed(hr) {
			return hresultErr(fmt.Sprintf("IUIAutomationLegacyIAccessiblePattern::SetValue(%q)", value), hr)
		}
		return nil
	}
	return fmt.Errorf("computer: element accepts no text value (RangeValue-only controls cannot be set from this build; focus it with ax_press and use key arrows instead)")
}

func axSetValue(pid int, role, contains, value string) error {
	return axFindDo(pid, role, contains, func(el comObj) error {
		return doAXSetValue(el, value)
	})
}

func axSetValueByID(pid, maxDepth, id int, value string) (AXElement, error) {
	return axActByIndex(pid, maxDepth, id, func(el comObj) error {
		return doAXSetValue(el, value)
	})
}
