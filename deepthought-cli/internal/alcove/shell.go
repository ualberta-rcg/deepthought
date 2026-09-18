// Package alcove owns the long-lived shell used by a DeepThought session.
//
// Commands execute in the same bash process, so cd, export, module load, conda
// activate, and virtual environments persist across tool calls.
package alcove

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const DefaultMaxOutput = 1 << 20 // 1 MiB

// Result is one completed command.
type Result struct {
	Output    string
	ExitCode  int
	Truncated bool
}

// Options controls the shell resource envelope.
type Options struct {
	MaxOutput int
	Dir       string
	Env       []string
	Envelope  *Envelope
}

// Shell is one persistent bash process. Run is serialized because shell state
// is inherently ordered.
type Shell struct {
	mu        sync.Mutex
	opts      Options
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *os.File
	reader    *bufio.Reader
	closed    bool
	startOnce sync.Once
	startErr  error
}

// New creates a lazy shell. The process starts on the first Run.
func New(opts Options) *Shell {
	if opts.MaxOutput <= 0 {
		opts.MaxOutput = DefaultMaxOutput
	}
	return &Shell{opts: opts}
}

func (s *Shell) start() error {
	s.startOnce.Do(func() {
		name := "bash"
		args := []string{"--noprofile", "--norc"}
		if s.opts.Envelope != nil && s.opts.Envelope.Available() {
			name, args = s.opts.Envelope.Wrap(name, args...)
		}
		cmd := exec.Command(name, args...)
		cmd.Dir = s.opts.Dir
		cmd.Env = append(os.Environ(), "PS1=", "PROMPT_COMMAND=")
		cmd.Env = append(cmd.Env, s.opts.Env...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		stdin, err := cmd.StdinPipe()
		if err != nil {
			s.startErr = fmt.Errorf("alcove: stdin: %w", err)
			return
		}
		outR, outW, err := os.Pipe()
		if err != nil {
			s.startErr = fmt.Errorf("alcove: output pipe: %w", err)
			return
		}
		cmd.Stdout, cmd.Stderr = outW, outW
		if err := cmd.Start(); err != nil {
			_ = outR.Close()
			_ = outW.Close()
			s.startErr = fmt.Errorf("alcove: start bash: %w", err)
			return
		}
		_ = outW.Close()
		s.cmd, s.stdin, s.stdout = cmd, stdin, outR
		s.reader = bufio.NewReaderSize(outR, 32<<10)
	})
	return s.startErr
}

// Run executes command in the persistent shell and waits for its sentinel.
func (s *Shell) Run(ctx context.Context, command string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return Result{}, errors.New("alcove: shell is closed")
	}
	if err := s.start(); err != nil {
		return Result{}, err
	}

	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return Result{}, fmt.Errorf("alcove: nonce: %w", err)
	}
	marker := "__DEEPTHOUGHT_CLI_" + hex.EncodeToString(nonce) + "__"
	// A top-level exit would terminate the persistent shell before it can emit
	// the sentinel. Preserve the requested status in a subshell instead.
	fields := strings.Fields(command)
	if len(fields) >= 1 && len(fields) <= 2 && fields[0] == "exit" {
		command = "( " + command + " )"
	}
	script := fmt.Sprintf("{ %s\n}; __deepthought_cli_rc=$?; printf '\\n%s%%d\\n' \"$__deepthought_cli_rc\"\n", command, marker)
	if _, err := io.WriteString(s.stdin, script); err != nil {
		return Result{}, fmt.Errorf("alcove: write command: %w", err)
	}

	type readResult struct {
		result Result
		err    error
	}
	done := make(chan readResult, 1)
	go func() {
		r, err := s.readUntil(marker)
		done <- readResult{result: r, err: err}
	}()

	select {
	case got := <-done:
		return got.result, got.err
	case <-ctx.Done():
		s.killLocked()
		<-done // reader exits after the process group is killed
		return Result{}, ctx.Err()
	}
}

func (s *Shell) readUntil(marker string) (Result, error) {
	var out strings.Builder
	truncated := false
	for {
		fragment, err := s.reader.ReadString('\n')
		if strings.HasPrefix(fragment, marker) {
			raw := strings.TrimSpace(strings.TrimPrefix(fragment, marker))
			code, convErr := strconv.Atoi(raw)
			if convErr != nil {
				return Result{}, fmt.Errorf("alcove: malformed exit sentinel %q", raw)
			}
			return Result{Output: strings.TrimSpace(out.String()), ExitCode: code, Truncated: truncated}, nil
		}

		if out.Len() < s.opts.MaxOutput {
			remaining := s.opts.MaxOutput - out.Len()
			if len(fragment) > remaining {
				out.WriteString(fragment[:remaining])
				truncated = true
			} else {
				out.WriteString(fragment)
			}
		} else if fragment != "" {
			truncated = true
		}

		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if errors.Is(err, io.EOF) {
				return Result{}, errors.New("alcove: shell exited before command completed")
			}
			return Result{}, fmt.Errorf("alcove: read output: %w", err)
		}
	}
}

// Close terminates the shell and all children in its process group.
func (s *Shell) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.killLocked()
	return nil
}

// killLocked tears the shell down. This is best-effort by design: it runs on
// the shutdown path with no error channel to report into, and after a SIGKILL
// a "kill of an already-dead process" failure carries no information we could
// act on. Callers holding the lock own the lifetime; nothing here may block.
func (s *Shell) killLocked() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		_, _ = s.cmd.Process.Wait()
	}
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.stdout != nil {
		_ = s.stdout.Close()
	}
}
