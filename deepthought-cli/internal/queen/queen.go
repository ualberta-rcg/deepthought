// Package queen is DeepThought's permission gate (per docs/ARCHITECTURE.md): every tool
// call passes through it before running. Named for the Borg Queen — the personification
// of the Collective's will, whose approval every action needs; some she refuses
// outright, no matter how the collective asks.
//
// Decision order:
//  1. Rule 1 — destructive operations are DENIED outright (overrules everything).
//  2. Persistent always-deny rules → Deny.
//  3. Task-scoped / always-allow grants → Allow.
//  4. Explicit allow/ask/deny rule lists (config) — first match wins.
//  5. Operation-mode category defaults (read/write/run/tool/sudo → allow|ask|deny).
//  6. Fallback: read-only → Allow; else Ask (or Allow in auto).
package queen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"deepthought-cli/internal/tools"
)

// Mode is the legacy permission preset. Kept for back-compat; new code should
// use OpMode names ("safe", "safe-auto", "auto").
type Mode int

const (
	// Review prompts before each mutating tool call (maps to OpMode "safe").
	Review Mode = iota
	// AlwaysProceed runs mutating tools without prompting (maps to OpMode "auto").
	// Destructive ops are STILL denied — Rule 1 outranks the user.
	AlwaysProceed
	// Sandbox is reserved; behaves like Review for now.
	Sandbox
)

// Decision is what the gate says about a tool call.
type Decision int

const (
	Allow Decision = iota
	Deny
	Ask
)

// Category classifies a tool call for operation-mode lookup.
type Category string

const (
	CatRead Category = "read"
	CatWrite Category = "write" // reserved for future edit tool; bash file writes land in run today
	CatRun   Category = "run"
	CatTool  Category = "tool"
	CatSudo  Category = "sudo"
)

// DecisionName is allow|ask|deny as stored in config / op-mode profiles.
type DecisionName string

const (
	DecAllow DecisionName = "allow"
	DecAsk   DecisionName = "ask"
	DecDeny  DecisionName = "deny"
)

// OpMode is a named profile mapping each category → allow|ask|deny.
type OpMode struct {
	Name string
	Map  map[Category]DecisionName
}

// Built-in operation modes.
var (
	OpSafe = OpMode{
		Name: "safe",
		Map: map[Category]DecisionName{
			CatRead: DecAsk, CatWrite: DecAsk, CatRun: DecAsk, CatTool: DecAsk, CatSudo: DecDeny,
		},
	}
	OpSafeAuto = OpMode{
		Name: "safe-auto",
		Map: map[Category]DecisionName{
			CatRead: DecAllow, CatWrite: DecAllow, CatRun: DecAsk, CatTool: DecAllow, CatSudo: DecAsk,
		},
	}
	OpAuto = OpMode{
		Name: "auto",
		Map: map[Category]DecisionName{
			CatRead: DecAllow, CatWrite: DecAllow, CatRun: DecAllow, CatTool: DecAllow, CatSudo: DecAllow,
		},
	}
)

// BuiltinModes returns the built-in operation modes in cycle order.
func BuiltinModes() []OpMode { return []OpMode{OpSafe, OpSafeAuto, OpAuto} }

// LookupMode finds a built-in (or custom) mode by name. Unknown → OpSafe.
func LookupMode(name string, custom []OpMode) OpMode {
	name = strings.ToLower(strings.TrimSpace(name))
	// Legacy aliases.
	switch name {
	case "", "review", "safe":
		return OpSafe
	case "always-proceed", "auto":
		return OpAuto
	case "safe-auto", "safe_auto", "sandbox":
		if name == "sandbox" {
			return OpSafe // sandbox reserved; treat as safe for now
		}
		return OpSafeAuto
	}
	for _, m := range custom {
		if strings.EqualFold(m.Name, name) {
			return m
		}
	}
	for _, m := range BuiltinModes() {
		if m.Name == name {
			return m
		}
	}
	return OpSafe
}

// ModeFromLegacy maps the old Mode enum onto an OpMode name.
func ModeFromLegacy(m Mode) string {
	switch m {
	case AlwaysProceed:
		return "auto"
	default:
		return "safe"
	}
}

// PersistFunc is called when the user chooses "always" / "never" so the host
// can write a rule into config.json. Optional.
type PersistFunc func(decision DecisionName, rule string)

// Rules holds the hybrid allow/ask/deny rule lists (reference-style strings).
type Rules struct {
	Allow []string
	Ask   []string
	Deny  []string
}

// Gate is the permission evaluator. One instance per session (holds task-scoped
// grants that must not leak across SSH users). Mode/OpMode/Rules are mutable
// under the mutex so F9 / Settings can hot-swap.
type Gate struct {
	mu sync.Mutex

	// Legacy Mode kept for tests / NewGate(Review); Op is the source of truth.
	Mode Mode
	Op   OpMode
	Rules Rules
	Custom []OpMode

	// Task-scoped grants: keys like "bash:ls *" cleared when the incursion ends.
	taskAllow map[string]bool
	// Session always-allow / always-deny (also persisted via OnPersist).
	alwaysAllow map[string]bool
	alwaysDeny  map[string]bool

	OnPersist PersistFunc
}

// NewGate builds a gate in the given legacy mode.
func NewGate(mode Mode) *Gate {
	g := &Gate{
		Mode:        mode,
		Op:          LookupMode(ModeFromLegacy(mode), nil),
		taskAllow:   map[string]bool{},
		alwaysAllow: map[string]bool{},
		alwaysDeny:  map[string]bool{},
	}
	return g
}

// NewGateFromConfig builds a gate from an operation-mode name + rule lists.
func NewGateFromConfig(modeName string, rules Rules, custom []OpMode) *Gate {
	g := NewGate(Review)
	g.SetOpMode(modeName, custom)
	g.Rules = rules
	g.Custom = custom
	return g
}

// Clone returns a fresh Gate sharing OpMode / Rules / OnPersist but with empty
// task- and session-scoped grants. SSH sessions must call this so one user's
// "allow for this task" never leaks into another connection.
func (g *Gate) Clone() *Gate {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	opMap := make(map[Category]DecisionName, len(g.Op.Map))
	for k, v := range g.Op.Map {
		opMap[k] = v
	}
	custom := make([]OpMode, len(g.Custom))
	for i, c := range g.Custom {
		cm := make(map[Category]DecisionName, len(c.Map))
		for k, v := range c.Map {
			cm[k] = v
		}
		custom[i] = OpMode{Name: c.Name, Map: cm}
	}
	return &Gate{
		Mode: g.Mode,
		Op:   OpMode{Name: g.Op.Name, Map: opMap},
		Rules: Rules{
			Allow: append([]string(nil), g.Rules.Allow...),
			Ask:   append([]string(nil), g.Rules.Ask...),
			Deny:  append([]string(nil), g.Rules.Deny...),
		},
		Custom:      custom,
		taskAllow:   map[string]bool{},
		alwaysAllow: map[string]bool{},
		alwaysDeny:  map[string]bool{},
		OnPersist:   g.OnPersist,
	}
}

// SetOpMode switches the active operation mode by name.
func (g *Gate) SetOpMode(name string, custom []OpMode) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Op = LookupMode(name, custom)
	switch g.Op.Name {
	case "auto":
		g.Mode = AlwaysProceed
	default:
		g.Mode = Review
	}
}

// OpModeName returns the active operation-mode name.
func (g *Gate) OpModeName() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.Op.Name
}

// CycleOpMode advances safe → safe-auto → auto → safe and returns the new name.
func (g *Gate) CycleOpMode() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	order := []string{"safe", "safe-auto", "auto"}
	at := 0
	for i, n := range order {
		if n == g.Op.Name {
			at = i
			break
		}
	}
	next := order[(at+1)%len(order)]
	g.Op = LookupMode(next, g.Custom)
	switch g.Op.Name {
	case "auto":
		g.Mode = AlwaysProceed
	default:
		g.Mode = Review
	}
	return g.Op.Name
}

// ClearTaskGrants drops all task-scoped grants (call when an incursion ends).
func (g *Gate) ClearTaskGrants() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.taskAllow = map[string]bool{}
}

// GrantTask allows matching calls until ClearTaskGrants (this incursion).
func (g *Gate) GrantTask(toolName string, args map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.taskAllow[ruleKey(toolName, args)] = true
}

// GrantAlways allows matching calls for the rest of the session and persists.
func (g *Gate) GrantAlways(toolName string, args map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := ruleKey(toolName, args)
	g.alwaysAllow[key] = true
	rule := ruleString(toolName, args)
	if g.OnPersist != nil && rule != "" {
		fn := g.OnPersist
		g.mu.Unlock()
		fn(DecAllow, rule)
		g.mu.Lock()
	}
}

// DenyAlways denies matching calls for the rest of the session and persists.
func (g *Gate) DenyAlways(toolName string, args map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := ruleKey(toolName, args)
	g.alwaysDeny[key] = true
	rule := ruleString(toolName, args)
	if g.OnPersist != nil && rule != "" {
		fn := g.OnPersist
		g.mu.Unlock()
		fn(DecDeny, rule)
		g.mu.Lock()
	}
}

// Decide returns the decision for a tool call.
func (g *Gate) Decide(_ context.Context, tool tools.Tool, args map[string]any) Decision {
	if tool == nil {
		return Deny
	}
	// 1. Rule 1: destructive ops denied always.
	if isDestructive(tool, args) {
		return Deny
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	key := ruleKey(tool.Name(), args)
	if g.alwaysDeny[key] {
		return Deny
	}
	if g.alwaysAllow[key] || g.taskAllow[key] {
		return Allow
	}

	// 4. Explicit rule lists (deny > ask > allow for safety on overlaps —
	// first matching list in that order).
	if matchRules(g.Rules.Deny, tool.Name(), args) {
		return Deny
	}
	if matchRules(g.Rules.Ask, tool.Name(), args) {
		return Ask
	}
	if matchRules(g.Rules.Allow, tool.Name(), args) {
		return Allow
	}

	// 5. Operation-mode category default.
	cat := classify(tool, args)
	if d, ok := g.Op.Map[cat]; ok {
		return decisionFromName(d)
	}

	// 6. Fallback: read-only allow; else ask (or allow in auto legacy).
	if tool.ReadOnly() {
		return Allow
	}
	if g.Mode == AlwaysProceed {
		return Allow
	}
	return Ask
}

func decisionFromName(d DecisionName) Decision {
	switch d {
	case DecAllow:
		return Allow
	case DecDeny:
		return Deny
	default:
		return Ask
	}
}

// classify maps a tool call onto a category.
func classify(tool tools.Tool, args map[string]any) Category {
	name := tool.Name()
	if tool.ReadOnly() || name == "read" {
		return CatRead
	}
	if name == "bash" {
		cmd, _ := args["command"].(string)
		cmd = strings.TrimSpace(cmd)
		if strings.HasPrefix(cmd, "sudo ") || cmd == "sudo" {
			return CatSudo
		}
		return CatRun
	}
	return CatTool
}

// ruleKey is a coarse session-grant key (tool + primary arg prefix).
func ruleKey(toolName string, args map[string]any) string {
	return toolName + ":" + primaryArg(toolName, args)
}

// ruleString builds a persistable reference-style rule, e.g. Bash(git *) or Read(~/**).
func ruleString(toolName string, args map[string]any) string {
	if toolName == "" {
		return ""
	}
	arg := primaryArg(toolName, args)
	title := strings.ToUpper(toolName[:1]) + toolName[1:]
	if arg == "" {
		return title
	}
	// Soften exact command into a prefix pattern for bash.
	if toolName == "bash" {
		fields := strings.Fields(arg)
		if len(fields) > 0 {
			return title + "(" + fields[0] + " *)"
		}
	}
	if toolName == "read" {
		dir := filepath.Dir(arg)
		if dir != "" && dir != "." {
			return title + "(" + dir + "/**)"
		}
	}
	return title + "(" + arg + ")"
}

func primaryArg(toolName string, args map[string]any) string {
	if args == nil {
		return ""
	}
	switch toolName {
	case "bash":
		s, _ := args["command"].(string)
		return strings.TrimSpace(s)
	case "read":
		s, _ := args["file_path"].(string)
		return strings.TrimSpace(s)
	}
	return ""
}

// matchRules reports whether any rule matches this tool call.
// Formats: "Bash", "Bash(git *)", "Read(~/**)", "bash", "bash(ls*)".
func matchRules(rules []string, toolName string, args map[string]any) bool {
	arg := primaryArg(toolName, args)
	for _, raw := range rules {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		name, pattern := parseRule(raw)
		if !strings.EqualFold(name, toolName) {
			continue
		}
		if pattern == "" {
			return true // tool-level allow/deny/ask
		}
		if matchPattern(pattern, arg) {
			return true
		}
	}
	return false
}

func parseRule(raw string) (name, pattern string) {
	open := strings.IndexByte(raw, '(')
	if open < 0 {
		return raw, ""
	}
	close := strings.LastIndexByte(raw, ')')
	if close <= open {
		return raw, ""
	}
	return raw[:open], raw[open+1 : close]
}

// matchPattern is a simple glob: * matches any run of chars; ~ expands to $HOME.
func matchPattern(pattern, value string) bool {
	if strings.HasPrefix(pattern, "~") {
		if h, err := homeDir(); err == nil {
			pattern = h + pattern[1:]
		}
	}
	if strings.HasPrefix(value, "~") {
		if h, err := homeDir(); err == nil {
			value = h + value[1:]
		}
	}
	ok, err := filepath.Match(pattern, value)
	if err == nil && ok {
		return true
	}
	// filepath.Match doesn't treat ** specially; also try prefix* style.
	if strings.HasSuffix(pattern, "*") && !strings.HasSuffix(pattern, "/**") {
		return strings.HasPrefix(value, strings.TrimSuffix(pattern, "*"))
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return value == prefix || strings.HasPrefix(value, prefix+"/")
	}
	return value == pattern
}

func homeDir() (string, error) {
	return os.UserHomeDir()
}
