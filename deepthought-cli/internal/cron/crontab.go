package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Runner is the exec seam (hermetic tests; the real one is ExecRunner). stdin
// is fed to the command when non-nil; output is combined stdout+stderr.
type Runner interface {
	Run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)
}

// Client reads and (carefully) rewrites the user's crontab. Every Install
// backs the current table up first (Dir/backups, newest 10 kept, 0600); the
// new table is piped to `crontab -` on stdin — no temp files, no shell
// interpolation anywhere.
type Client struct {
	Run Runner
	Dir string // registry + backup directory (e.g. DataDir/cron)
}

func NewClient(dir string) *Client {
	return &Client{Run: ExecRunner{}, Dir: dir}
}

// List returns the parsed crontab. "no crontab for X" is an empty table, not
// an error; anything else surfaces honestly.
func (c *Client) List(ctx context.Context) ([]Line, error) {
	out, err := c.Run.Run(ctx, nil, "crontab", "-l")
	if err != nil {
		if isNoCrontab(out, err) {
			return nil, nil
		}
		return nil, fmt.Errorf("crontab -l: %w", err)
	}
	return Parse(string(out)), nil
}

// Install writes the full new table (raw lines) via `crontab -` after backing
// up the current one. Backups rotate: the newest 10 are kept.
func (c *Client) Install(ctx context.Context, lines []string) error {
	current, err := c.Run.Run(ctx, nil, "crontab", "-l")
	if err != nil && !isNoCrontab(current, err) {
		return fmt.Errorf("crontab -l (before install): %w", err)
	}
	if err := c.backup(string(current)); err != nil {
		return err
	}
	newTable := strings.Join(lines, "\n") + "\n"
	_, err = c.Run.Run(ctx, []byte(newTable), "crontab", "-")
	return err
}

// Undo restores the most recent backup.
func (c *Client) Undo(ctx context.Context) error {
	data, name, err := c.newestBackup()
	if err != nil {
		return err
	}
	if err := c.backup(data); err != nil { // undo is itself undoable
		return err
	}
	_ = c.markRestored(name)
	_, err = c.Run.Run(ctx, []byte(data), "crontab", "-")
	return err
}

func (c *Client) backup(current string) error {
	dir := filepath.Join(c.Dir, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name := filepath.Join(dir, "crontab-"+time.Now().UTC().Format("20060102T150405.000000000"))
	if err := os.WriteFile(name, []byte(current), 0o600); err != nil {
		return err
	}
	return pruneBackups(dir, 10)
}

func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "crontab-") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // timestamp prefix → chronological
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

func (c *Client) newestBackup() (data, name string, err error) {
	dir := filepath.Join(c.Dir, "backups")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", fmt.Errorf("no backups: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "crontab-") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", "", fmt.Errorf("no backups")
	}
	sort.Strings(names)
	name = names[len(names)-1]
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", "", err
	}
	return string(b), name, nil
}

func (c *Client) markRestored(usedBackup string) error {
	r, err := LoadRegistry(c.Dir)
	if err == nil {
		r.LastBackup = usedBackup
		_ = r.Save(c.Dir)
	}
	return nil
}

func isNoCrontab(out []byte, err error) bool {
	if err == nil {
		return false
	}
	msg := string(out) + err.Error()
	return strings.Contains(msg, "no crontab for")
}
