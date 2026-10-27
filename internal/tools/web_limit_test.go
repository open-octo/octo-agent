package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAcquireWebSlot_CapsConcurrentRequests(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	writeCfg(t, "tools:\n  web_max_concurrency: 2\n")

	ctx := context.Background()
	r1, err := acquireWebSlot(ctx)
	if err != nil {
		t.Fatalf("slot 1: %v", err)
	}
	r2, err := acquireWebSlot(ctx)
	if err != nil {
		t.Fatalf("slot 2: %v", err)
	}

	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := acquireWebSlot(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slot 3 past the cap: want it to wait until the deadline, got err=%v", err)
	}

	r1()
	r3, err := acquireWebSlot(ctx)
	if err != nil {
		t.Fatalf("slot 3 after a release: %v", err)
	}
	r2()
	r3()
}

func TestAcquireWebSlot_ZeroIsUnlimited(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	writeCfg(t, "tools:\n  web_max_concurrency: 0\n")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 64; i++ {
		release, err := acquireWebSlot(ctx)
		if err != nil {
			t.Fatalf("slot %d: unlimited should never wait, got %v", i, err)
		}
		defer release()
	}
}
