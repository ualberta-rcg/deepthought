package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseKinds(t *testing.T) {
	in := strings.Join([]string{
		"# a comment",
		"",
		"SHELL=/bin/bash",
		"17 * * * * /usr/bin/daily.sh # note here",
		"@daily /usr/local/bin/cleanup",
		"garbage-without-fields",
		"*/10 * * * * /bin/sync",
	}, "\n")
	lines := Parse(in)
	byCmd := map[string]Line{}
	for _, l := range lines {
		if l.Kind == LineEntry {
			byCmd[l.Command] = l
		}
	}
	if len(byCmd) != 3 {
		t.Fatalf("entries = %d, want 3 (garbage carried verbatim, not an entry)", len(byCmd))
	}
	e := byCmd["/usr/bin/daily.sh"]
	if e.Schedule != "17 * * * *" || e.Comment != "note here" {
		t.Errorf("daily parsed wrong: %+v", e)
	}
	d := byCmd["/usr/local/bin/cleanup"]
	if d.Schedule != "@daily" {
		t.Errorf("@daily schedule = %q", d.Schedule)
	}
	for _, l := range lines {
		if l.Raw != "" && l.Hash == "" {
			t.Error("hash missing")
		}
	}
}

func TestParseRoundTrips(t *testing.T) {
	in := "SHELL=/bin/bash\n\n# c\n17 * * * * run.sh\n"
	var raws []string
	for _, l := range Parse(in) {
		raws = append(raws, l.Raw)
	}
	// The trailing newline parses as one blank trailing line; joining the
	// raws reproduces the input exactly modulo that terminator.
	if got, want := strings.TrimRight(strings.Join(raws, "\n"), "\n"), strings.TrimRight(in, "\n"); got != want {
		t.Errorf("round-trip drifted:\n%q\n%q", want, got)
	}
}

func TestHumanize(t *testing.T) {
	cases := map[string]string{
		"*/5 * * * *":  "every 5 min",
		"0 9 * * *":    "daily 09:00",
		"30 2 * * *":   "daily 02:30",
		"0 9 * * 1-5":  "weekdays 09:00",
		"0 11 * * 6,0": "weekends 11:00",
		"@daily":       "daily",
		"@reboot":      "every reboot",
		"0 9 1 * *":    "0 9 1 * *", // date-qualified: raw fallback
		"1,2 * * * *":  "1,2 * * * *",
	}
	for sched, want := range cases {
		if got := Humanize(sched); got != want {
			t.Errorf("Humanize(%q) = %q, want %q", sched, got, want)
		}
	}
}

func TestDiffByHash(t *testing.T) {
	prev := Parse("0 9 * * * keep.sh\n0 9 * * * gone.sh")
	cur := Parse("0 9 * * * keep.sh\n0 9 * * * new.sh")
	added, removed := Diff(prev, cur)
	if len(added) != 1 || added[0].Command != "new.sh" {
		t.Errorf("added = %+v", added)
	}
	if len(removed) != 1 || removed[0].Command != "gone.sh" {
		t.Errorf("removed = %+v", removed)
	}
}

func TestRegistryObserveAndDiff(t *testing.T) {
	dir := t.TempDir()
	r, err := LoadRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r.Observe(Parse("0 9 * * * a.sh\n0 9 * * * b.sh"), now)
	if len(r.Entries) != 2 {
		t.Fatalf("entries = %d", len(r.Entries))
	}
	if err := r.Save(dir); err != nil {
		t.Fatal(err)
	}

	r2, err := LoadRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Diff BEFORE observing: the registry still holds a+b, the fresh parse is
	// a+c → c added, b removed.
	added, removed := r2.DiffSinceLast(Parse("0 9 * * * a.sh\n0 9 * * * c.sh"))
	if len(added) != 1 || added[0].Command != "c.sh" {
		t.Errorf("added = %+v", added)
	}
	if len(removed) != 1 || removed[0].Command != "b.sh" {
		t.Errorf("removed = %+v", removed)
	}

	// THEN observe folds the new state in; removed entries are kept for history.
	later := now.Add(time.Hour)
	r2.Observe(Parse("0 9 * * * a.sh\n0 9 * * * c.sh"), later)
	if len(r2.Entries) != 3 {
		t.Fatalf("after observe entries = %d (b must be kept for history)", len(r2.Entries))
	}
	for _, e := range r2.Entries {
		if e.Command == "a.sh" && !e.LastSeen.Equal(later) {
			t.Error("a.sh LastSeen not stamped")
		}
	}
}

// fakeRunner records installs and serves a canned `crontab -l`.
type fakeRunner struct {
	listed    string
	noCron    bool
	installed []string // stdin of each `crontab -`
}

func (f *fakeRunner) Run(_ context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	switch {
	case name == "crontab" && len(args) == 1 && args[0] == "-l":
		if f.noCron {
			return []byte("no crontab for tester"), fmt.Errorf("exit 1")
		}
		return []byte(f.listed), nil
	case name == "crontab" && len(args) == 1 && args[0] == "-":
		f.installed = append(f.installed, string(stdin))
		f.listed = strings.TrimRight(string(stdin), "\n")
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected %s %v", name, args)
}

func TestClientListInstallUndo(t *testing.T) {
	dir := t.TempDir()
	fr := &fakeRunner{listed: "0 9 * * * old.sh\n"}
	c := &Client{Run: fr, Dir: dir}

	lines, err := c.List(context.Background())
	entries := 0
	for _, l := range lines {
		if l.Kind == LineEntry {
			entries++
		}
	}
	if err != nil || entries != 1 {
		t.Fatalf("list: %v (%d entries)", err, entries)
	}

	if err := c.Install(context.Background(), []string{"0 9 * * * old.sh", "*/5 * * * * new.sh"}); err != nil {
		t.Fatal(err)
	}
	if len(fr.installed) != 1 || !strings.Contains(fr.installed[0], "new.sh") {
		t.Fatalf("install stdin = %q", fr.installed)
	}
	// The pre-install table was backed up.
	b, name, err := c.newestBackup()
	if err != nil || !strings.Contains(b, "old.sh") {
		t.Fatalf("backup missing: %q %v", b, err)
	}
	// Undo restores it.
	if err := c.Undo(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fr.installed) != 2 || !strings.Contains(fr.installed[1], "old.sh") || strings.Contains(fr.installed[1], "new.sh") {
		t.Fatalf("undo installed %q", fr.installed[1])
	}
	_ = name
}

func TestClientNoCrontabIsEmpty(t *testing.T) {
	c := &Client{Run: &fakeRunner{noCron: true}, Dir: t.TempDir()}
	lines, err := c.List(context.Background())
	if err != nil || lines != nil {
		t.Fatalf("no-crontab should be empty-nil: %v %v", lines, err)
	}
}

func TestBackupsRotate(t *testing.T) {
	dir := t.TempDir()
	c := &Client{Run: &fakeRunner{}, Dir: dir}
	for i := 0; i < 14; i++ {
		if err := c.backup(fmt.Sprintf("table-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "backups"))
	if len(entries) != 10 {
		t.Errorf("backups = %d, want 10", len(entries))
	}
	data, _, err := c.newestBackup()
	if err != nil || data != "table-13" {
		t.Errorf("newest = %q %v", data, err)
	}
}
