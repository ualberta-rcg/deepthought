package queen

import (
	"regexp"
	"strings"

	"deepthought-cli/internal/tools"
)

// destructiveCmds matches shell commands that are unconditionally denied under Rule 1
// ("never run destructive ops"). The check is intentionally conservative: it greps
// the raw command string, so a destructive op hidden inside a compound command is
// still caught. False positives (refusing something merely suspicious) are acceptable
// here — the cost of running rm -rf / is higher than the cost of asking the user to
// rephrase.
//
// Add patterns here as we learn them. Keep them narrow enough not to fire on legit
// commands (e.g. `rm` of a single temp file is allowed; `rm -rf /` is not).
var destructiveCmds = []*regexp.Regexp{
	regexp.MustCompile(`\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f|--recursive)\s+(/|~|\$HOME|\*)`), // rm -rf / ~ $HOME *
	regexp.MustCompile(`\brm\s+(-[a-zA-Z]*f[a-zA-Z]*r|--recursive)\s+(/|~|\$HOME|\*)`),
	regexp.MustCompile(`\bmkfs\b`),
	regexp.MustCompile(`\bdd\b.*\bof=/dev/`), // dd writing to a device
	regexp.MustCompile(`>\s*/dev/s[d-z]`),    // redirect to a raw disk
	regexp.MustCompile(`:\(\)\s*\{`),         // fork bomb
	regexp.MustCompile(`\b(shutdown|reboot|halt|poweroff)\b`),
	regexp.MustCompile(`\bgit\s+push\s+.*--force\b.*\b(main|master)\b`), // force-push to protected refs
}

// isDestructive reports whether a tool call would do something Rule 1 forbids. Only
// bash is inspected today (read can't mutate); the command string is checked against
// the denylist. Unknown tools default to non-destructive (Queen's other rules still
// gate them via ReadOnly + Mode).
func isDestructive(tool tools.Tool, args map[string]any) bool {
	if tool == nil || tool.Name() != "bash" {
		return false
	}
	cmd, _ := args["command"].(string)
	if cmd == "" {
		return false
	}
	for _, re := range destructiveCmds {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

// DestructiveReason returns a human-facing explanation for why a command was denied,
// or "" if it wasn't destructive. Used by the TUI to render the denial.
func DestructiveReason(cmd string) string {
	for _, re := range destructiveCmds {
		if re.MatchString(cmd) {
			return "refused (matches destructive pattern: " + strings.TrimSpace(re.String()) + ")"
		}
	}
	return ""
}
