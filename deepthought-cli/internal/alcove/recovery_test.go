package alcove

import (
	"context"
	"testing"
	"time"
)

func TestShellUsableAfterCancellation(t *testing.T) {
	sh := New(Options{Dir: t.TempDir()})
	defer sh.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := sh.Run(ctx, "sleep 30"); err == nil {
		t.Fatal("expected cancellation")
	}
	result, err := sh.Run(context.Background(), "printf recovered")
	if err != nil || result.Output != "recovered" {
		t.Fatalf("recovery: %+v %v", result, err)
	}
}

func TestShellCancelledBeforeStart(t *testing.T) {
	sh := New(Options{Dir: t.TempDir()})
	defer sh.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sh.Run(ctx, "touch should-not-exist"); err == nil {
		t.Fatal("ran cancelled command")
	}
	if sh.cmd != nil {
		t.Fatal("started shell for cancelled command")
	}
}

func TestShellCapsUnbrokenOutput(t *testing.T) {
	sh := New(Options{MaxOutput: 32})
	defer sh.Close()
	r, err := sh.Run(context.Background(), "printf '%0100000d' 0")
	if err != nil || !r.Truncated || len(r.Output) > 32 {
		t.Fatalf("bounded output: %+v %v", r, err)
	}
}
