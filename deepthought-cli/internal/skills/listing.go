package skills

import "strings"

// Listing renders the compact "available skills" block for the model's system
// prompt: one line per skill with its name, description, and when-to-use. The
// skill *bodies* are deliberately NOT included — the model loads the body of the
// one relevant skill on demand via the `skill` tool (progressive disclosure), so
// only the small index lives in every request. Returns "" when there are no
// skills (the caller omits the block entirely).
func Listing(sk []*Skill) string {
	if len(sk) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Available skills — call the `skill` tool with a skill's name to load its full " +
		"instructions before acting on a task it covers:\n")
	for _, s := range sk {
		line := "  - " + s.Name + ": " + s.Description
		if w := oneLine(s.WhenToUse); w != "" {
			line += " (use when: " + w + ")"
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// oneLine collapses a multi-line string to a single trimmed line.
func oneLine(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
