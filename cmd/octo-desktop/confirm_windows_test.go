//go:build windows

package main

import (
	"testing"

	"golang.org/x/sys/windows"
)

// The bug this replaces answered "no" to every question because it matched the
// pressed button by label. Nothing but the Yes response may read as a yes —
// including the 0 MessageBox returns when it fails to open, which must never
// quit the app on its own.
func TestConfirmAnswerOnlyAcceptsYes(t *testing.T) {
	const (
		idNo     = 7
		idCancel = 2
		failed   = 0
	)
	if !confirmAnswer(idYes) {
		t.Error("confirmAnswer(IDYES) = false, want true")
	}
	for _, ret := range []int32{idNo, idCancel, failed} {
		if confirmAnswer(ret) {
			t.Errorf("confirmAnswer(%d) = true, want false", ret)
		}
	}
}

// MB_YESNO is what makes the two buttons a question rather than a lone OK, and
// the response idYes belongs to; the rest is how the box is presented.
func TestConfirmFlagsAskAYesNoQuestion(t *testing.T) {
	if confirmFlags&windows.MB_YESNO == 0 {
		t.Error("confirmFlags must include MB_YESNO")
	}
	if confirmFlags&windows.MB_SETFOREGROUND == 0 {
		t.Error("confirmFlags must include MB_SETFOREGROUND: the prompt is raised from the tray, with the app in the background")
	}
}

// Return must not be an answer of yes. These prompts stop a backend other
// clients are connected to, and MessageBox defaults to its first button —
// which for MB_YESNO is Yes — unless told otherwise.
func TestConfirmFlagsDefaultToNo(t *testing.T) {
	if confirmFlags&windows.MB_DEFBUTTON2 == 0 {
		t.Error("confirmFlags must include MB_DEFBUTTON2 so the cancel button is the default")
	}
}
