package alcove

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestShellPersistsDirectoryAndEnvironment(t *testing.T) {
	sh := New(Options{Dir: t.TempDir()})
	t.Cleanup(func() { _ = sh.Close() })

	run := func(command string) Result {
		t.Helper()
		got, err := sh.Run(context.Background(), command)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	if got := run("mkdir work && cd work && export ANNORAX_TEST=present"); got.ExitCode != 0 {
		t.Fatalf("setup exit = %d: %s", got.ExitCode, got.Output)
	}
	got := run(`printf '%s|%s' "$PWD" "$ANNORAX_TEST"`)
	if !strings.HasSuffix(got.Output, "/work|present") {
		t.Fatalf("state did not persist: %q", got.Output)
	}
}

func TestShellPersistsFunctionsLikeModule(t *testing.T) {
	sh := New(Options{})
	t.Cleanup(func() { _ = sh.Close() })

	if _, err := sh.Run(context.Background(), `module() { export LOADED_MODULE="$1"; }; module cuda/12.4`); err != nil {
		t.Fatal(err)
	}
	got, err := sh.Run(context.Background(), `printf %s "$LOADED_MODULE"`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Output != "cuda/12.4" {
		t.Fatalf("module-like function did not persist: %q", got.Output)
	}
}

func TestShellCapsOutput(t *testing.T) {
	sh := New(Options{MaxOutput: 32})
	t.Cleanup(func() { _ = sh.Close() })
	got, err := sh.Run(context.Background(), `printf '%0100d' 0`)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || len(got.Output) != 32 {
		t.Fatalf("output=%d truncated=%v", len(got.Output), got.Truncated)
	}
}

func TestShellTimeoutKillsProcessGroup(t *testing.T) {
	sh := New(Options{})
	t.Cleanup(func() { _ = sh.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := sh.Run(ctx, "sleep 30"); err == nil {
		t.Fatal("expected timeout")
	}
}
