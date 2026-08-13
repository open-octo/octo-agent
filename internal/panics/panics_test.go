package panics

import (
	"strings"
	"testing"
)

func TestError_ReportsAndStopsThePanic(t *testing.T) {
	var got error
	func() {
		// The documented usage: recover() stays in the deferred function, and
		// its value is handed to the helper. A version of Error that called
		// recover() itself would never fire here — the panic below would escape
		// and take the test binary with it.
		defer func() { got = Error(recover(), "unit under test", "id", 7) }()
		panic("boom")
	}()

	if got == nil {
		t.Fatal("Error returned nil for a real panic")
	}
	if !strings.Contains(got.Error(), "boom") || !strings.Contains(got.Error(), "unit under test") {
		t.Errorf("error should name both the panic and the goroutine, got: %v", got)
	}
}

func TestError_NilWhenNoPanic(t *testing.T) {
	if err := Error(nil, "unit under test"); err != nil {
		t.Errorf("Error(nil) = %v, want nil", err)
	}
}
