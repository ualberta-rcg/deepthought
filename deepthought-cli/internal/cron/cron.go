// Package cron reads, parses, and (carefully) rewrites the user's crontab,
// plus a local tracking registry so entries can be watched over time — the
// foundation for the server later aggregating cron inventories across
// systems and clusters.
package cron

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Kind classifies one crontab line.
type Kind int

const (
	LineBlank Kind = iota
	LineComment
	LineEnv   // VAR=... carried verbatim
	LineEntry // a scheduled command
)

// Line is one parsed crontab line. Hash is a stable identity (sha256 of the
// trimmed raw line, first 12 hex) so entries can be tracked across edits even
// when their position changes.
type Line struct {
	Raw      string
	Kind     Kind
	Schedule string // the five-field spec or @shortcut (entries only)
	Command  string // everything after the schedule (entries only)
	Comment  string // trailing same-line comment, if any
	Hash     string
}

// Parse splits crontab -l output into classified lines. It is deliberately
// forgiving: anything that doesn't look like an entry is carried verbatim, and
// Install(List-collected lines) round-trips the table unchanged.
func Parse(out string) []Line {
	var lines []Line
	for _, raw := range strings.Split(out, "\n") {
		ln := Line{Raw: raw, Hash: hashLine(raw)}
		t := strings.TrimSpace(raw)
		switch {
		case t == "":
			ln.Kind = LineBlank
		case strings.HasPrefix(t, "#"):
			ln.Kind = LineComment
		case isEnvLine(t):
			ln.Kind = LineEnv
		default:
			sched, cmd, ok := splitEntry(t)
			if !ok {
				ln.Kind = LineComment // unrecognized: carry verbatim
				break
			}
			ln.Kind = LineEntry
			ln.Schedule = sched
			// A trailing " # note" is preserved as metadata without losing the
			// command text.
			if i := strings.Index(cmd, " # "); i >= 0 {
				ln.Command = strings.TrimSpace(cmd[:i])
				ln.Comment = strings.TrimSpace(cmd[i+2:])
			} else {
				ln.Command = strings.TrimSpace(cmd)
			}
		}
		lines = append(lines, ln)
	}
	return lines
}

func hashLine(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])[:12]
}

func isEnvLine(t string) bool {
	i := strings.IndexByte(t, '=')
	if i <= 0 {
		return false
	}
	name := t[:i]
	for _, r := range name {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '_') {
			return false
		}
	}
	return true
}

// splitEntry pulls "schedule command" out of a crontab entry line. @shortcuts
// (@daily, @hourly, @reboot, @weekly, @monthly, @yearly) count as schedules.
func splitEntry(t string) (sched, cmd string, ok bool) {
	if strings.HasPrefix(t, "@") {
		f := strings.Fields(t)
		if len(f) < 2 {
			return "", "", false
		}
		return f[0], strings.Join(f[1:], " "), true
	}
	f := strings.Fields(t)
	if len(f) < 6 {
		return "", "", false
	}
	return strings.Join(f[:5], " "), strings.Join(f[5:], " "), true
}

// Diff returns which lines were added and removed between two parses, matched
// by Hash (position-independent).
func Diff(prev, cur []Line) (added, removed []Line) {
	prevSet := map[string]Line{}
	for _, l := range prev {
		if l.Kind == LineEntry {
			prevSet[l.Hash] = l
		}
	}
	curSet := map[string]Line{}
	for _, l := range cur {
		if l.Kind == LineEntry {
			curSet[l.Hash] = l
		}
	}
	for h, l := range curSet {
		if _, there := prevSet[h]; !there {
			added = append(added, l)
		}
	}
	for h, l := range prevSet {
		if _, there := curSet[h]; !there {
			removed = append(removed, l)
		}
	}
	return added, removed
}

// Humanize renders a schedule in plain language. Exact-match the common
// shapes; fall back to the raw spec otherwise (honesty over cleverness).
func Humanize(schedule string) string {
	f := strings.Fields(schedule)
	if len(f) == 0 {
		return schedule
	}
	if strings.HasPrefix(schedule, "@") {
		switch schedule {
		case "@reboot":
			return "every reboot"
		case "@hourly":
			return "hourly"
		case "@daily", "@midnight":
			return "daily"
		case "@weekly":
			return "weekly"
		case "@monthly":
			return "monthly"
		case "@yearly", "@annually":
			return "yearly"
		}
		return schedule
	}
	if len(f) != 5 {
		return schedule
	}
	min, hour, dom, mon, dow := f[0], f[1], f[2], f[3], f[4]
	if dom != "*" || mon != "*" {
		return schedule // date-qualified: too rare to fake nicely
	}
	// */n minutes at a fixed hour → "every n min"
	if strings.HasPrefix(min, "*/") && hour == "*" {
		return "every " + strings.TrimPrefix(min, "*/") + " min"
	}
	// Weekday set at one hour → "weekdays 09:00"
	if hour != "*" && hour != "" && !strings.Contains(hour, ",") && !strings.HasPrefix(hour, "*/") {
		at := pad2(hour) + ":" + pad2(min)
		switch {
		case dow == "1-5":
			return "weekdays " + at
		case dow == "*":
			return "daily " + at
		case dow == "6,0" || dow == "0,6":
			return "weekends " + at
		}
		return schedule
	}
	return schedule
}

// pad2 zero-pads a single-digit numeric field.
func pad2(v string) string {
	if len(v) == 1 && v[0] >= '0' && v[0] <= '9' {
		return "0" + v
	}
	return v
}
