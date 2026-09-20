package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/babel"
	commandpkg "deepthought-cli/internal/commands"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/queen"
	"deepthought-cli/internal/slurm"
	"deepthought-cli/internal/tools"
	"deepthought-cli/internal/unimatrix"
)

// StatusInfo feeds the top bar's status cluster. Owned by the root model; populated
// from the loaded config (package config) at startup.
type StatusInfo struct {
	Model  string // active model label, e.g. "Qwen 3.5 122B"
	Mode   string // permission preset, e.g. "review"
	Addr   string // "local" or the SSH listen addr (e.g. ":2323") — shown on settings, not the bar
	Effort string
}

const (
	chatInputHeight   = 3 // top border + one input line + bottom border
	maxSuggestVisible = 5 // cap on visible popover rows
)

// sugItem is one slash-command suggestion. `name` includes the leading "/".
type sugItem struct {
	name string
	desc string
}

// commands is the v0 command set. (Later: merge of built-ins + skills + plugins.)
// filterCommands is the fuzzy drop-in seam — swap its body, keep the signature.
var commandRegistry = commandpkg.Builtins()

// chatSystemPrompt is prepended to every request. It tells DeepThought what it is
// (NOT Claude — it's DeepThought, an agentic terminal assistant), where its settings
// live, and what it can do. Kept short: the model doesn't need a novel.
func chatSystemPrompt(cfgPath string) string {
	return "You are DeepThought, an agentic terminal assistant (not Claude — you are DeepThought). " +
		"You run over SSH on a terminal; you are not a web assistant. " +
		"You can write and run code, operate HPC clusters (Slurm, Lmod modules, schedulers, " +
		"parallel jobs), manage Proxmox virtualization, and help with scientific work. " +
		"Use the bash and read tools to investigate and act rather than just describing. " +
		"History may contain tombstones; call expand(id) whenever omitted full text could matter. " +
		"Be concise and direct; say what you did. " +
		"Your own settings (providers, models, roles) live in " + cfgPath + " — point users there " +
		"to change models or configuration. Don't claim to be Claude or Anthropic."
}

// chatMaxTokens caps each completion. Conservative so we stay under per-model
// ceilings across the catalog; raised later when sampling is configurable.
const chatDefaultMaxTokens = 8192

// Streaming uses a channel-drain pattern (no tea.Program handle needed):
//   - startStream spawns a goroutine that runs babel.ChatStream; each delta is
//     pushed onto a channel. The Cmd returns streamStartedMsg carrying the channel.
//   - On streamStartedMsg, Update stores the channel and arms drainCmd.
//   - drainCmd blocks reading ONE item from the channel and returns it as a
//     streamItemMsg. Each streamItemMsg handler appends the delta to the transcript
//     and re-arms drainCmd, so chunks render as they arrive. The final item closes
//     the loop and commits the assistant turn to history.
type streamItem struct {
	delta     string // visible content chunk
	reasoning string // thinking-trace chunk
	toolCalls []babel.ToolCall
	usage     babel.Usage // token accounting from the final chunk (zero if none)
	err       error
	final     bool
}

type streamStartedMsg struct{ ch <-chan streamItem }
type streamItemMsg streamItem

// titleGeneratedMsg carries a summary-model-generated chat title back into Update.
type titleGeneratedMsg struct {
	title string
	err   error
}

// dispatchState tracks an in-flight tool-dispatch sequence: the assistant
// transmission that introduced the calls and the index of the call currently
// being processed.
type dispatchState struct {
	transmission *history.Transmission
	idx          int
}

// pendingApproval is set while a Queen "ask" is on screen awaiting a y/n.
type pendingApproval struct {
	probe  *history.Probe
	tool   tools.Tool
	args   map[string]any
	inline bool // true = !-mode bash (no probe graph / no dispatch)
}

// toolResultMsg carries a completed (or denied) tool run back into Update.
type toolResultMsg struct {
	probe  *history.Probe
	result tools.Result
}

// InferenceSource resolves roles to pooled clients. app.Settings satisfies it
// structurally; tests use a fake. It lives here (not as a concrete dependency)
// so tui never imports app.
type InferenceSource interface {
	RoleClient(role string) (*babel.Client, unimatrix.Model, error)
	Path() string                // settings file path, surfaced to the model in the system prompt
	CycleModel() (string, error) // /model: advance the chat model to the next one
}

type behaviorSource interface {
	Effort() babel.Effort
	MaxTokens() int
	Temperature() float64
}

type behaviorMutator interface {
	CycleEffort() (babel.Effort, error)
	ImportProviders() (int, error)
}

// ChatModel is the chat screen: a scrollable transcript (viewport), a slash
// autocomplete popover that floats above the input when open, and a bordered
// input box. Non-slash input is sent to the model via babel and the reply is
// streamed (frame-by-frame via the spinner while waiting, then the full text) into
// the transcript. Plain struct, not a tea.Model.
type ChatModel struct {
	vp     viewport.Model
	input  textinput.Model
	lines  []string
	width  int
	height int
	ready  bool

	// inference: src resolves the agentic role to a client+model per request
	// (so settings edits apply live); coll is the rich collective object; store
	// is the persistence seam. busy + pendIdx track an in-flight call and its
	// transcript row. streaming + acc + thinkAcc + streamCh drive the live
	// render: while busy && !streaming the row shows the spinner (cold-start
	// wait); once the first delta lands, streaming flips true and the row
	// shows the accumulating thinking block + reply.
	src       InferenceSource
	reg       *tools.Registry
	gate      *queen.Gate
	coll      *history.Collective
	store     history.Store
	cluster   slurm.ClusterSnapshot // latest cached snapshot → the model's cluster blurb
	env       EnvInfo               // static host/session environment → the env brief
	skills    string                // compact "available skills" index → a per-request system note
	replayed  bool                  // prior turns rendered into the transcript (resume)
	busy      bool
	streaming bool
	acc       string
	thinkAcc  string // reasoning trace accumulating for the in-flight turn
	pendIdx   int    // index into lines of the in-flight row, -1 when idle
	streamCh  <-chan streamItem
	cancel    context.CancelFunc // cancels the in-flight stream; esc triggers it
	spin      spinner.Model
	spinFrame int              // our own frame counter; the spinner's frame field is unexported
	dispatch  *dispatchState   // non-nil while dispatching tool calls
	awaiting  *pendingApproval // non-nil while a y/n permission prompt is on screen

	// Activity / session accounting.
	turnStarted   time.Time // when the current busy turn began
	activityVerb  string    // rotating verb for the activity line
	sessionIn     int       // session prompt tokens (billing sum)
	sessionOut    int       // session completion tokens
	lastContext   int       // most recent prompt_tokens (= window fill)
	sessionCycles int       // completed LLM rounds this session (tool-loop cycles)
	sessionMsgs   int       // user + assistant messages echoed this session
	queue         []string  // typed lines waiting while busy
	liveTokens    int       // rough live estimate while streaming

	// currentProducer is the model driving the in-flight turn; stamped onto each
	// Transmission/Incursion as Producer (provenance) at commit time.
	currentProducer *history.Producer

	// input history for bash-style up/down recall. histPos == len(history) is the
	// "fresh line" position; up moves older, down moves newer, down-at-end clears.
	history []string
	histPos int

	// slash-autocomplete state
	sugOpen     bool
	sugItems    []sugItem
	sugSelected int
}

// NewChatModel builds the chat screen with the input focused, persisting to a
// fresh JSONL file under chatDir. src resolves roles to clients/models per
// request; reg+gate are the shared tool registry and Queen gate.
func NewChatModel(src InferenceSource, reg *tools.Registry, gate *queen.Gate, sessionID string, source history.ChatStoreSource) ChatModel {
	prompt := chatSystemPrompt("")
	if src != nil {
		prompt = chatSystemPrompt(src.Path())
	}
	return buildChat(src, reg, gate, func() (history.Store, *history.Collective) {
		store := source()
		coll, _ := store.CreateCollective(history.SpawnCollectiveRequest{
			SystemPrompt: prompt,
			SessionID:    sessionID,
		})
		return store, coll
	})
}

// ResumeChatModel loads an existing collective from the store and continues it:
// new turns append to the same record, and the wire history re-flattens so the
// model picks up where the last chat left off.
func ResumeChatModel(src InferenceSource, reg *tools.Registry, gate *queen.Gate, source history.ChatStoreSource, collID string) (ChatModel, error) {
	store := source()
	coll, err := store.Resume(collID)
	if err != nil {
		return ChatModel{}, err
	}
	m := buildChat(src, reg, gate, func() (history.Store, *history.Collective) {
		return store, coll
	})
	// replayHistory runs on the first applyLayout (once width is known).
	return m, nil
}

// buildChat is the shared constructor for new + resumed chats.
func buildChat(src InferenceSource, reg *tools.Registry, gate *queen.Gate, makeStore func() (history.Store, *history.Collective)) ChatModel {
	ti := textinput.New()
	ti.Placeholder = "Ask DeepThought…"
	ti.Prompt = "" // the box is the prompt; no leading glyph
	ti.Focus()
	sp := spinner.New(spinner.WithSpinner(splashSpinner), spinner.WithStyle(styleSystem))
	store, coll := makeStore()
	return ChatModel{
		vp:      viewport.New(),
		input:   ti,
		src:     src,
		reg:     reg,
		gate:    gate,
		coll:    coll,
		store:   store,
		pendIdx: -1,
		acc:     "",
		spin:    sp,
	}
}

func (m ChatModel) Init() tea.Cmd { return m.input.Focus() }

// Resize stores geometry and relayouts. m.height is already
// (terminal H − TopBarHeight − BottomBarHeight); the root does that subtraction
// so it lives in one place.
func (m ChatModel) Resize(w, h int) ChatModel {
	m.width, m.height = w, h
	m.applyLayout()
	return m
}

// applyLayout is the single chokepoint for sizing the regions: viewport fills
// what's left above the activity strip, the (variable) popover, and the fixed
// input box. Pointer receiver — it mutates; called from value-receiver methods.
func (m *ChatModel) applyLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	if !m.replayed {
		m.replayHistory()
	}
	m.vp.SetWidth(m.width)
	vpH := m.height - chatInputHeight - ActivityHeight - m.popoverHeight()
	if vpH < 1 {
		vpH = 1
	}
	m.vp.SetHeight(vpH)
	m.input.SetWidth(m.width - 4) // box border (1) + padding (1) each side
	m.ready = true
	m.vp.GotoBottom() // keep the transcript pinned when the popover opens/closes
}

// replayHistory renders the loaded collective's prior turns into the
// transcript, so a resumed chat shows its history. Idempotent (guarded by
// m.replayed); safe to call before the first View.
func (m *ChatModel) replayHistory() {
	m.replayed = true
	if m.coll == nil || len(m.coll.Incursions) == 0 {
		return
	}
	w := m.width
	if w == 0 {
		w = 80
	}
	for _, inc := range m.coll.Incursions {
		if inc.Status == history.IncursionFailed {
			continue
		}
		row := styleUserEcho.Width(w).Render(stylePromptPrefix.Render("❯ ") + inc.Prompt)
		m.lines = append(m.lines, "", row)
		for _, tx := range inc.Transmissions {
			if tx.Text != "" {
				m.lines = append(m.lines, assistantView(tx.Text))
			}
		}
	}
	m.flush()
}

// popoverHeight is 0 when closed, else min(maxSuggestVisible, len items).
func (m ChatModel) popoverHeight() int {
	if !m.sugOpen || len(m.sugItems) == 0 {
		return 0
	}
	n := len(m.sugItems)
	if n > maxSuggestVisible {
		n = maxSuggestVisible
	}
	return n
}

// Cursor returns the input's terminal caret, offset to its real screen position:
// top bar + viewport + activity + popover + input top border (Y), box border +
// padding (X). Recomputed every render, so it tracks resize and popover open/close.
func (m ChatModel) Cursor() *tea.Cursor {
	c := m.input.Cursor()
	if c == nil {
		return nil
	}
	c.Position.Y += TopBarHeight + m.vp.Height() + ActivityHeight + m.popoverHeight() + 1
	c.Position.X += 2
	return c
}

// Update routes model replies and spinner ticks to their handlers, then keys:
// when the popover is open it intercepts navigation/accept; otherwise keys go to
// the input and a value change re-derives the popover.
func (m ChatModel) Update(msg tea.Msg) (ChatModel, tea.Cmd) {
	switch msg := msg.(type) {
	case streamStartedMsg:
		m.streamCh = msg.ch
		return m, drainCmd(m.streamCh)
	case streamItemMsg:
		return m.handleStreamItem(msg)
	case titleGeneratedMsg:
		if msg.err != nil {
			return m, nil // title generation is best-effort; never block on it
		}
		m.coll.Title = msg.title
		_ = m.store.SaveObject(m.coll)
		_ = m.store.Flush()
		return m, nil
	case toolResultMsg:
		return m.handleToolResult(msg)
	case inlineBashResultMsg:
		if msg.result.IsError {
			m.appendTurn(styleError.Render("  ↳ " + msg.result.Summary))
		} else {
			m.appendTurn(styleToolResult.Render("  ↳ " + msg.result.Summary))
		}
		return m, nil
	case spinner.TickMsg:
		// Animate the spinner only while busy (activity line + cold-start
		// transcript row). Once streaming, deltas re-render the reply row;
		// activity still ticks for elapsed/verb.
		if !m.busy {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		m.spinFrame++
		if !m.streaming {
			m.refreshPending()
		}
		// Live token estimate from accumulated text while streaming.
		if m.streaming {
			m.liveTokens = estimateTokens(m.acc) + estimateTokens(m.thinkAcc)
		}
		return m, cmd
	}

	if kp, ok := msg.(tea.KeyPressMsg); ok {
		// ESC / Ctrl-C while busy interrupts the agent: cancel the in-flight
		// stream and break the tool loop. Once idle, esc is a no-op — chat
		// is the navigation home.
		if m.busy && (kp.String() == "esc" || kp.String() == "ctrl+c") {
			return m.interrupt()
		}
		// Idle esc: clear a queued line if any; otherwise chat is the navigation
		// root (home), so esc is a no-op. Don't intercept when an approval prompt
		// or the slash popover is open — those own esc.
		if !m.busy && m.awaiting == nil && !m.sugOpen && kp.String() == "esc" {
			if len(m.queue) > 0 {
				m.queue = nil
				return m, nil
			}
			return m, nil
		}
		// A pending Queen permission prompt intercepts y/n/a/A/d (and enter=yes).
		// No other input is accepted while approval is on screen.
		if m.awaiting != nil {
			switch kp.String() {
			case "y", "Y", "enter":
				ap := m.awaiting
				m.awaiting = nil
				if ap.inline {
					return m, runInlineBashCmd(ap.tool, ap.args)
				}
				return m, runToolCmd(ap.tool, ap.args, ap.probe)
			case "a":
				ap := m.awaiting
				m.awaiting = nil
				if m.gate != nil {
					m.gate.GrantTask(ap.tool.Name(), ap.args)
				}
				if ap.inline {
					return m, runInlineBashCmd(ap.tool, ap.args)
				}
				return m, runToolCmd(ap.tool, ap.args, ap.probe)
			case "A":
				ap := m.awaiting
				m.awaiting = nil
				if m.gate != nil {
					m.gate.GrantAlways(ap.tool.Name(), ap.args)
				}
				if ap.inline {
					return m, runInlineBashCmd(ap.tool, ap.args)
				}
				return m, runToolCmd(ap.tool, ap.args, ap.probe)
			case "n", "N", "esc":
				ap := m.awaiting
				m.awaiting = nil
				if ap.inline {
					m.appendTurn(styleError.Render("  ↳ denied by user"))
					return m, nil
				}
				return m, deniedResultCmd(ap.probe, "denied by user")
			case "d", "D":
				ap := m.awaiting
				m.awaiting = nil
				if m.gate != nil {
					m.gate.DenyAlways(ap.tool.Name(), ap.args)
				}
				if ap.inline {
					m.appendTurn(styleError.Render("  ↳ denied by user (always)"))
					return m, nil
				}
				return m, deniedResultCmd(ap.probe, "denied by user (always)")
			}
			return m, nil // swallow everything else
		}
		if m.sugOpen {
			switch kp.String() {
			case "up":
				m.moveSug(-1)
				return m, nil
			case "down":
				m.moveSug(1)
				return m, nil
			case "tab":
				m.fillFromSelected() // fill "/name ", no run
				return m, nil
			case "enter":
				// Run the selected command immediately.
				m.input.SetValue(m.sugItems[m.sugSelected].name)
				m.sugOpen = false
				m.applyLayout()
				return m.submit()
			case "esc":
				m.sugOpen = false
				m.applyLayout()
				return m, nil
			}
		}
		if kp.String() == "enter" {
			return m.submit()
		}
		// Bash-style input history: up/down recall prior submissions. Only when
		// idle, no approval prompt, and the slash popover is closed (it owns the
		// arrows while open).
		if !m.busy && m.awaiting == nil {
			switch kp.String() {
			case "up":
				m.recallHistory(-1)
				return m, nil
			case "down":
				m.recallHistory(1)
				return m, nil
			}
		}
	}

	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		m.recomputePopover() // skip on blink ticks / non-mutating msgs
	}
	return m, cmd
}

// pushHistory appends a submitted line to the input history (skipping empties
// and consecutive duplicates), like bash, and resets the browse position.
func (m *ChatModel) pushHistory(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if n := len(m.history); n > 0 && m.history[n-1] == line {
		m.histPos = len(m.history)
		return
	}
	m.history = append(m.history, line)
	m.histPos = len(m.history)
}

// recallHistory browses the input history: dir -1 = older (up), +1 = newer
// (down). At the newest end, down clears the input to a fresh line.
func (m *ChatModel) recallHistory(dir int) {
	if len(m.history) == 0 {
		return
	}
	if dir < 0 {
		if m.histPos > 0 {
			m.histPos--
		}
	} else {
		if m.histPos < len(m.history) {
			m.histPos++
		}
	}
	if m.histPos >= len(m.history) {
		m.input.SetValue("")
	} else {
		m.input.SetValue(m.history[m.histPos])
	}
	m.recomputePopover()
}

// submit trims the input, clears it, closes the popover, and either runs a slash
// command, queues the line while busy, runs inline bash (!), or kicks off a model
// round-trip. /quit works even mid-reply.
func (m ChatModel) submit() (ChatModel, tea.Cmd) {
	typed := strings.TrimSpace(m.input.Value())
	val := typed
	if fields := strings.Fields(val); len(fields) > 0 && strings.HasPrefix(fields[0], "/") {
		if command, ok := commandRegistry.Lookup(fields[0]); ok {
			val = "/" + command.Name
		}
	}
	// Record what the user actually typed for up/down recall (bash-style).
	m.pushHistory(typed)
	if val == "/quit" || val == "/fish" {
		return m, tea.Quit
	}
	// For everything else, clear the input + close the popover up front.
	m.input.SetValue("")
	m.sugOpen = false
	m.applyLayout()
	switch {
	case val == "":
		if m.coll.LastInterrupted() != nil {
			return m.resumeInterrupted()
		}
		return m, nil
	case val == "/help" || val == "?":
		m.systemLine("help: type a message · !cmd for bash · /help (or ?) · /model · /settings · /quit")
		return m, nil
	case val == "/settings":
		return m, Goto(ScreenSettings)
	case val == "/context":
		return m, Goto(ScreenGrid)
	case val == "/model":
		return m, func() tea.Msg { return OpenModelChooserMsg{} }
	case val == "/resume":
		return m.resumeInterrupted()
	case val == "/effort":
		return m, func() tea.Msg { return OpenEffortMsg{} }
	case val == "/import":
		if source, ok := m.src.(behaviorMutator); ok {
			n, err := source.ImportProviders()
			if err != nil {
				m.systemLine("✗ " + err.Error())
			} else {
				m.systemLine(fmt.Sprintf("imported %d provider(s)", n))
			}
		}
		return m, nil
	case strings.HasPrefix(val, "/"):
		m.systemLine("unknown command: " + val + " — try /help")
		return m, nil
	case strings.HasPrefix(val, "!"):
		// Inline bash mode: run through the bash tool + Queen, no model round-trip.
		return m.runInlineBash(strings.TrimSpace(strings.TrimPrefix(val, "!")))
	}
	if m.busy {
		// Queue the line; activity bar shows "queued: …"; auto-send when idle.
		m.queue = append(m.queue, val)
		m.systemLine(styleSystem.Render("queued (" + fmt.Sprintf("%d", len(m.queue)) + ") — esc clears"))
		return m, nil
	}
	m.userEcho(val)
	return m.beginChat(val)
}

// --- model round-trip -------------------------------------------------------

// beginChat appends the user incursion to history, paints a "thinking…" row, and fires
// the streaming call + the spinner's first tick.
func (m ChatModel) beginChat(text string) (ChatModel, tea.Cmd) {
	inc := m.coll.StartIncursion(text)
	_ = m.store.SaveObject(inc)
	return m.armStream()
}

// interrupt cancels the in-flight stream and breaks the tool loop: drops the
// pending row, marks the active incursion failed, and returns to idle. Stale
// stream items from the cancelled goroutine are ignored (handleStreamItem
// no-ops when !busy).
func (m ChatModel) interrupt() (ChatModel, tea.Cmd) {
	partialText, partialThinking := m.acc, m.thinkAcc
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.busy = false
	m.streaming = false
	m.streamCh = nil
	m.dispatch = nil
	if m.pendIdx >= 0 && m.pendIdx < len(m.lines) {
		m.replacePending(m.liveView())
	}
	m.acc = ""
	m.thinkAcc = ""
	m.pendIdx = -1
	if inc := m.coll.ActiveIncursion(); inc != nil {
		if partialText != "" || partialThinking != "" {
			tx, _ := inc.AddTransmission(strings.TrimSpace(partialText), nil)
			tx.Producer = m.currentProducer
			if partialThinking != "" {
				syn := tx.AddSynapse(partialThinking, "reasoning")
				_ = m.store.SaveObject(syn)
			}
			_ = m.store.SaveObject(tx)
		}
		inc.Interrupt("interrupted by user")
		_ = m.store.SaveObject(inc)
		_ = m.store.Flush()
	}
	m.appendTurn(styleSystem.Render("— interrupted — /resume to continue"))
	if m.gate != nil {
		m.gate.ClearTaskGrants()
	}
	return m, nil
}

func (m ChatModel) resumeInterrupted() (ChatModel, tea.Cmd) {
	inc := m.coll.LastInterrupted()
	if inc == nil {
		m.systemLine("nothing to resume")
		return m, nil
	}
	if m.busy {
		m.systemLine("(DeepThought is still working)")
		return m, nil
	}
	inc.Status = history.IncursionStreaming
	inc.Error = ""
	_ = m.store.SaveObject(inc)
	return m.armStream()
}

// Busy reports whether a model/tool turn is active.
func (m ChatModel) Busy() bool { return m.busy }

// SessionUsage returns the session's token totals for the Stats screen / status line.
func (m ChatModel) SessionUsage() (in, out, lastContext int) {
	return m.sessionIn, m.sessionOut, m.lastContext
}

// SessionStats returns token + activity counters for the Stats screen.
func (m ChatModel) SessionStats() (in, out, lastContext, cycles, messages int) {
	return m.sessionIn, m.sessionOut, m.lastContext, m.sessionCycles, m.sessionMsgs
}

// QueuedHint returns the front of the input queue (for the activity chip), or "".
func (m ChatModel) QueuedHint() string {
	if len(m.queue) == 0 {
		return ""
	}
	return m.queue[0]
}

// Notice appends a system line for root-level key handling.
func (m ChatModel) Notice(text string) ChatModel {
	m.systemLine(text)
	return m
}

// armStream paints the "thinking…" row and fires the SSE stream + spinner tick. Used
// both for the first incursion and to re-stream after tool results complete a loop cycle.
// busy is asserted by callers; this leaves it set. The agentic role is resolved
// HERE, per request, so a settings save takes effect on the very next turn.
func (m ChatModel) armStream() (ChatModel, tea.Cmd) {
	client, model, err := m.src.RoleClient(unimatrix.RoleAgentic)
	if err != nil {
		m.appendTurn(styleError.Render("✗ " + err.Error()))
		return m, nil
	}
	// Stamp the producer for the transmissions this turn will create.
	m.currentProducer = &history.Producer{
		Provider: model.Provider,
		ModelID:  model.ID,
	}
	m.busy = true
	m.streaming = false
	m.acc = ""
	m.thinkAcc = ""
	m.liveTokens = 0
	m.turnStarted = time.Now()
	seed := model.ID
	if m.coll != nil {
		seed += m.coll.ID
	}
	m.activityVerb = verbFor(seed + fmt.Sprintf("%d", len(m.coll.Incursions)))
	m.lines = append(m.lines, "") // blank breathing row above the in-flight row
	m.pendIdx = len(m.lines)
	m.lines = append(m.lines, m.pendingView())
	m.flush()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	return m, tea.Batch(startStream(ctx, client, m.newRequest(model)), tea.Cmd(m.spin.Tick))
}

// newRequest builds the ChatRequest for the current incursion: the model, the full message
// history (system prompt + incursions incl. any tool calls/results), the advertised tools,
// and sampling. Effort is the SOLE reasoning control: off means no thinking;
// any other level means thinking on at that level. Built fresh per incursion so the latest
// history is always sent.
func (m ChatModel) newRequest(model unimatrix.Model) babel.ChatRequest {
	effort := babel.EffortMedium
	maxTokens := chatDefaultMaxTokens
	temperature := 0.7
	if source, ok := m.src.(behaviorSource); ok {
		effort = source.Effort()
		maxTokens = source.MaxTokens()
		temperature = source.Temperature()
	}
	if effort == "" {
		effort = babel.EffortMedium
	}
	if !model.Can(unimatrix.CapReasoning) {
		effort = babel.EffortOff // non-reasoning model: never request thinking
	}
	if model.Effort != "" {
		effort = babel.Effort(model.Effort)
	}
	return babel.ChatRequest{
		Model:          model.ID,
		Messages:       m.requestMessages(),
		Tools:          m.reg.Schemas(),
		MaxTokens:      maxTokens,
		Temperature:    temperature,
		Effort:         effort,
		ReasoningStyle: model.EffectiveReasoningStyle(),
	}
}

// SetCluster fills the cached Slurm snapshot from the background poller, so the
// next request can carry a live cluster blurb to the model.
func (m ChatModel) SetCluster(s slurm.ClusterSnapshot) ChatModel {
	m.cluster = s
	return m
}

// SetSkills stores the compact "available skills" index so each request can tell
// the model which skills exist (it loads a body on demand via the `skill` tool).
func (m ChatModel) SetSkills(s string) ChatModel {
	m.skills = s
	return m
}

// requestMessages returns the message slice to send by flattening the rich
// collective. It uses MessagesByState so each probe renders at its own residency
// (Full today; a demoted probe renders shorter once a Queen thread demotes it).
// Two transient notes may be appended — the available-skills index and the live
// cluster-status blurb. Both are sent to the model but NOT persisted to the
// collective.
func (m ChatModel) requestMessages() []babel.Message {
	msgs := m.coll.MessagesByState()
	if m.skills != "" {
		msgs = append(msgs, babel.Message{Role: "system", Content: m.skills})
	}
	if blurb := m.clusterBlurb(); blurb != "" {
		msgs = append(msgs, babel.Message{Role: "system", Content: blurb})
	}
	if b := m.envBrief(); b != "" {
		msgs = append(msgs, babel.Message{Role: "system", Content: b})
	}
	return msgs
}

// SetEnv stamps the static environment (drives the per-request env brief).
func (m ChatModel) SetEnv(e EnvInfo) ChatModel {
	m.env = e
	return m
}

// envBrief is the compact, detection-driven environment note injected into
// every request beside the cluster blurb — the model's ground truth about
// WHERE it runs. Hard-capped at envBriefMax lines ("not super big"); ""
// when nothing was detected.
func (m ChatModel) envBrief() string {
	var facts []string
	e := m.env
	if e.ShortName != "" || e.Host != "" {
		facts = append(facts, "host "+orDefault(e.ShortName, e.Host)+hostSuffix(e))
	}
	if e.OSName != "" && e.Kernel != "" {
		facts = append(facts, e.OSName+", kernel "+e.Kernel)
	} else if e.OSName != "" {
		facts = append(facts, e.OSName)
	}
	if e.Slurm && m.cluster.GPUType != "" {
		facts = append(facts, "slurm cluster, GPUs "+m.cluster.GPUType)
	} else if e.Slurm {
		facts = append(facts, "slurm cluster")
	}
	if e.Module {
		facts = append(facts, "lmod modules")
	}
	if m.cluster.Fairshare > 0 {
		facts = append(facts, fmt.Sprintf("fairshare %.2f", m.cluster.Fairshare))
	}
	for _, r := range m.cluster.StorageRows {
		facts = append(facts, fmt.Sprintf("%s %s/%s", r.Label, r.Used, r.Size))
	}
	// Negative capability: forbidden/constrained things are worth more than
	// positive ones (they prevent failed attempts).
	if p := proxyEnv(); p != "" {
		facts = append(facts, "outbound network via proxy "+p+" — direct connections fail")
	}
	if len(facts) == 0 {
		return ""
	}
	if len(facts) > envBriefMax {
		facts = facts[:envBriefMax]
	}
	return "\n[Environment — detected at startup; verify before acting on it.]\n" +
		strings.Join(facts, "\n") + "\n"
}

// envBriefMax caps the environment brief's size in the system prompt.
const envBriefMax = 6

// hostSuffix renders the arch/cpu/mem suffix for the host line ("" when
// unprobed).
func hostSuffix(e EnvInfo) string {
	if e.Arch == "" && e.CPUs == 0 {
		return ""
	}
	spec := e.Arch
	if e.CPUs > 0 {
		spec += fmt.Sprintf(", %d cpus", e.CPUs)
	}
	if e.MemGB > 0 {
		spec += fmt.Sprintf(", %d GB", e.MemGB)
	}
	return " (" + spec + ")"
}

// proxyEnv reports the configured outbound proxy, if any.
func proxyEnv() string {
	for _, k := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// clusterBlurb is a compact, clearly-labelled snapshot of cluster state so the
// model can reason about scheduling ("is the cluster busy?") without running
// squeue itself. Returns "" when nothing has been gathered yet. Kept to a few
// lines; it says the sample may be stale and to verify before acting.
func (m ChatModel) clusterBlurb() string {
	c := m.cluster
	if c.NodesTotal == 0 && c.CPUTotal == 0 && c.GPUs == 0 {
		return ""
	}
	var b strings.Builder
	stamp := "unknown time"
	if !c.FetchedAt.IsZero() {
		stamp = c.FetchedAt.Format("15:04")
	}
	b.WriteString("\n[Cluster status, sampled " + stamp +
		" — may be a few minutes stale; verify with the slurm tools before acting on it.]\n")
	var facts []string
	if c.NodesTotal > 0 {
		facts = append(facts, fmt.Sprintf("nodes %d/%d up", c.NodesUp, c.NodesTotal))
	}
	if c.CPUTotal > 0 {
		facts = append(facts, fmt.Sprintf("CPUs %d%% used", int(frac01(float64(c.CPUAlloc), float64(c.CPUTotal))*100+0.5)))
	}
	if c.GPUs > 0 {
		g := fmt.Sprintf("GPUs %d/%d in use", c.GPUsUsed, c.GPUs)
		if c.GPUUsable > 0 {
			g += fmt.Sprintf(", %d usable now", c.GPUUsable)
		}
		facts = append(facts, g)
	}
	facts = append(facts, fmt.Sprintf("cluster queue %d running / %d pending", c.JobsRunning, c.JobsPending))
	if len(c.YourJobs) > 0 {
		facts = append(facts, fmt.Sprintf("you have %d job(s)", len(c.YourJobs)))
	}
	if c.Fairshare > 0 {
		label, _ := slurm.FairshareTier(c.Fairshare)
		facts = append(facts, fmt.Sprintf("your fairshare %.2f (%s)", c.Fairshare, label))
	}
	for _, r := range c.StorageRows {
		if r.Label == "scratch" {
			facts = append(facts, fmt.Sprintf("scratch %d%% used", r.Pct))
		}
	}
	b.WriteString(strings.Join(facts, "; ") + ".\n")
	return b.String()
}

// handleStreamItem renders one streamed delta (or finalizes on the last item).
// Reasoning chunks accumulate into the thinking block; content chunks into the
// reply — the first of either flips the in-flight row from spinner to live text.
// On the final item: on error the failed user incursion is dropped; on success the
// assistant transmission (text + thinking synapse + any tool calls) is committed
// to history, and if the model requested tools the dispatch loop begins, otherwise
// busy clears.
func (m ChatModel) handleStreamItem(it streamItemMsg) (ChatModel, tea.Cmd) {
	// If the turn was interrupted (esc), the stream goroutine still drains a
	// final/cancelled item onto the channel — ignore it so we don't reopen a
	// closed turn.
	if !m.busy {
		return m, nil
	}
	if !m.finalItem(it) {
		if it.reasoning != "" || it.delta != "" {
			m.streaming = true
			m.thinkAcc += it.reasoning
			m.acc += it.delta
			m.replacePending(m.liveView())
		}
		return m, drainCmd(m.streamCh) // re-arm: wait for the next chunk
	}

	// Stream over.
	m.streaming = false
	m.streamCh = nil
	m.pendIdx = -1

	inc := m.coll.ActiveIncursion()
	if inc == nil {
		// Should not happen, but recover gracefully.
		m.busy = false
		m.appendTurn(styleError.Render("✗ internal error: no active incursion"))
		return m, nil
	}

	if it.err != nil {
		m.busy = false
		m.appendTurn(styleError.Render("✗ " + it.err.Error()))
		inc.Fail(it.err.Error())
		_ = m.store.SaveObject(inc)
		_ = m.store.Flush()
		return m, nil
	}

	inc.Status = history.IncursionDispatching
	tx, _ := inc.AddTransmission(strings.TrimSpace(m.acc), it.toolCalls)
	tx.Producer = m.currentProducer // provenance: which model produced this
	usage := it.usage
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		// Gateway omitted usage — fall back to a chars/4 estimate so counts
		// are never blank on the Stats screen / activity line.
		usage = estimateUsage(m.requestMessages(), m.acc, m.thinkAcc)
	}
	tx.Cost = history.Cost{InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens}
	m.recordUsage(usage)
	if m.thinkAcc != "" {
		syn := tx.AddSynapse(m.thinkAcc, "reasoning")
		_ = m.store.SaveObject(syn)
	}
	_ = m.store.SaveObject(tx)
	_ = m.store.SaveObject(inc)

	if len(it.toolCalls) == 0 {
		// Plain answer — render it and go idle.
		m.busy = false
		if m.gate != nil {
			m.gate.ClearTaskGrants()
		}
		inc.MarkCompleted()
		_ = m.store.SaveObject(inc)
		_ = m.store.Flush()
		if tx.Text == "" {
			m.appendTurn(styleSystem.Render("(empty reply)"))
		} else {
			m.sessionMsgs++
		}
		cmds := []tea.Cmd{sessionUsageCmd(m)}
		// After the first real exchange, ask the summary model for a pretty title
		// (best-effort; failures are swallowed). Only real chats with a reply.
		if m.coll.Title == "" && tx.Text != "" && len(m.coll.Incursions) == 1 {
			cmds = append(cmds, generateTitleCmd(m.src, inc.Prompt, tx.Text))
		}
		if next := m.drainQueue(); next != nil {
			cmds = append(cmds, next)
		}
		return m, tea.Batch(cmds...)
	}

	// Tool calls requested: freeze any streamed text (leave the row as the assistant
	// reply) and begin dispatch. If neither text nor thinking landed, drop the
	// now-stale spinner row.
	if tx.Text == "" && m.thinkAcc == "" && len(m.lines) > 0 {
		m.lines = m.lines[:len(m.lines)-1] // remove the blank breathing row too
		m.flush()
	}
	return m.beginDispatch(tx)
}

// finalItem reports whether this item is the terminal one (err set, or the channel
// closed — surfaced as a final item with no delta).
func (m ChatModel) finalItem(it streamItemMsg) bool {
	return it.final || it.err != nil
}

// --- tool dispatch loop -----------------------------------------------------

// beginDispatch stores the requested tool calls and processes the first one. busy
// stays true across the whole dispatch + re-stream; it only clears on a plain-text
// answer (or error / cap).
func (m ChatModel) beginDispatch(tx *history.Transmission) (ChatModel, tea.Cmd) {
	m.dispatch = &dispatchState{transmission: tx}
	return m.processCurrentTool()
}

// processCurrentTool handles the tool call at dispatch.idx: ask Queen, then either
// run, deny, or pause for a y/n. When all calls are processed it re-streams (loop
// back) or stops if the turn cap is hit.
func (m ChatModel) processCurrentTool() (ChatModel, tea.Cmd) {
	if m.dispatch == nil {
		return m, nil
	}
	if m.dispatch.idx >= len(m.dispatch.transmission.Probes) {
		// All calls in this assistant transmission resolved → feed results back, re-stream.
		m.dispatch = nil

		inc := m.coll.ActiveIncursion()
		if inc == nil {
			m.busy = false
			m.appendTurn(styleError.Render("✗ internal error: no active incursion during dispatch"))
			return m, nil
		}

		inc.Cycles++
		if inc.Cycles >= m.coll.MaxCycles {
			m.busy = false
			inc.Fail(fmt.Sprintf("hit tool-loop cap (%d cycles)", m.coll.MaxCycles))
			_ = m.store.SaveObject(inc)
			_ = m.store.Flush()
			m.appendTurn(styleError.Render(fmt.Sprintf("✗ hit tool-loop cap (%d cycles)", m.coll.MaxCycles)))
			return m, nil
		}

		_ = m.store.SaveObject(inc)
		return m.armStream()
	}

	probe := m.dispatch.transmission.Probes[m.dispatch.idx]
	probe.Status = history.ProbePending

	tool, ok := m.reg.Lookup(probe.Name)
	if !ok {
		// Unknown tool: feed the error back as a tool result so the model can recover.
		m.appendTurn(styleError.Render("▸ unknown tool: " + probe.Name))
		return m, deniedResultCmd(probe, "no such tool: "+probe.Name)
	}

	args := probe.Arguments
	m.appendTurn(styleTool.Render("▸ " + probe.Name + ": " + summarizeArgs(probe.Name, args)))

	if probe.ArgumentsError != "" {
		return m, deniedResultCmd(probe, "invalid arguments JSON: "+probe.ArgumentsError)
	}

	switch m.gate.Decide(context.Background(), tool, args) {
	case queen.Allow:
		probe.Decision = queen.Allow
		probe.Status = history.ProbeRunning // allowed → dispatch immediately
		probe.StartedAt = now()
		return m, runToolCmd(tool, args, probe)
	case queen.Deny:
		probe.Decision = queen.Deny
		probe.Status = history.ProbeDenied // agrees with Decision (terminal for this probe)
		reason := "refused by Queen"
		if probe.Name == "bash" {
			if cmd, _ := args["command"].(string); cmd != "" {
				if r := queen.DestructiveReason(cmd); r != "" {
					reason = r
				}
			}
		}
		probe.DecisionReason = reason
		return m, deniedResultCmd(probe, reason)
	default: // Ask
		probe.Decision = queen.Ask
		m.awaiting = &pendingApproval{probe: probe, tool: tool, args: args}
		m.appendTurn(styleToolAsk.Render("  ⚠ allow " + probe.Name + "? [y] once  [a] task  [A] always  [n]/[d]"))
		return m, nil
	}
}

// handleToolResult renders the result chrome, records the result on the probe,
// advances the dispatch, and processes the next probe.
func (m ChatModel) handleToolResult(r toolResultMsg) (ChatModel, tea.Cmd) {
	probe := r.probe
	probe.Status = history.ProbeCompleted
	if r.result.IsError {
		probe.Status = history.ProbeFailed
	}
	probe.Result = history.ResultView{
		Content: r.result.Content,
		IsError: r.result.IsError,
		Summary: r.result.Summary,
		Summaries: history.SummarySet{
			Full: r.result.Content,
		},
	}
	probe.FinishedAt = now()
	_ = probe.ExtractPatterns()

	if r.result.IsError {
		m.appendTurn(styleError.Render("  ↳ " + r.result.Summary))
	} else {
		m.appendTurn(styleToolResult.Render("  ↳ " + r.result.Summary))
	}

	inc := m.coll.ActiveIncursion()
	if inc != nil {
		_ = m.store.SaveObject(probe)
		_ = m.store.SaveObject(inc)
	}

	if m.dispatch != nil {
		m.dispatch.idx++
	}
	return m.processCurrentTool()
}

// runToolCmd runs a tool in a Cmd goroutine (bash can block for its whole timeout).
// Snapshots tool/args/probe so it never captures the ChatModel.
func runToolCmd(tool tools.Tool, args map[string]any, probe *history.Probe) tea.Cmd {
	return func() tea.Msg {
		res := tool.Run(context.Background(), args)
		return toolResultMsg{probe: probe, result: res}
	}
}

// deniedResultCmd emits a toolResultMsg carrying a denial/error, so the model sees a
// (failed) tool result and can react, keeping the assistant/tool turn pairing valid.
func deniedResultCmd(probe *history.Probe, msg string) tea.Cmd {
	return func() tea.Msg {
		return toolResultMsg{
			probe: probe,
			result: tools.Result{
				Content: msg,
				IsError: true,
				Summary: msg,
			},
		}
	}
}

// summarizeArgs returns a short, single-line preview of what a tool call targets, so
// the transcript chrome is glanceable without dumping the whole arg map.
func summarizeArgs(name string, args map[string]any) string {
	switch name {
	case "bash":
		if c, _ := args["command"].(string); c != "" {
			return truncate(c, 60)
		}
	case "read":
		if p, _ := args["file_path"].(string); p != "" {
			return p
		}
	}
	// Fallback: "<k>=<v>" pairs, truncated.
	var b strings.Builder
	first := true
	for k, v := range args {
		if !first {
			b.WriteByte(' ')
		}
		first = false
		fmt.Fprintf(&b, "%s=%v", k, v)
	}
	return truncate(b.String(), 60)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func onOffValue(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// pendingView renders the placeholder row held at m.pendIdx (cold-start wait,
// before the first delta). Deliberately BLANK: the animated verb lives only in
// the bottom activity strip — a second copy under the streaming text was
// noise (the once-over removed it).
func (m ChatModel) pendingView() string { return "" }

// activityView renders the 1-row strip above the input.
func (m ChatModel) activityView() string {
	tokens := m.liveTokens
	if !m.busy && m.lastContext > 0 {
		tokens = m.lastContext
	}
	var elapsed time.Duration
	if m.busy && !m.turnStarted.IsZero() {
		elapsed = time.Since(m.turnStarted)
	}
	queued := ""
	if len(m.queue) > 0 {
		queued = m.queue[0]
	}
	return RenderActivity(m.width, ActivityState{
		Busy:      m.busy,
		Streaming: m.streaming,
		Verb:      m.activityVerb,
		Tokens:    tokens,
		Elapsed:   elapsed,
		Queued:    queued,
		Awaiting:  m.awaiting != nil,
		SpinFrame: m.spinFrame,
		BashHint:  true,
	})
}

// recordUsage updates session billing totals and last-turn context size.
func (m *ChatModel) recordUsage(u babel.Usage) {
	if u.PromptTokens > 0 {
		m.sessionIn += u.PromptTokens
		m.lastContext = u.PromptTokens // most recent = window fill
	}
	if u.CompletionTokens > 0 {
		m.sessionOut += u.CompletionTokens
	}
	m.liveTokens = u.PromptTokens + u.CompletionTokens
	m.sessionCycles++ // one LLM round completed
}

// estimateTokens is a rough chars/4 heuristic used when the gateway omits usage.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	n := len(s) / 4
	if n < 1 {
		n = 1
	}
	return n
}

// estimateUsage builds a Usage from message + reply text when the gateway
// didn't report one.
func estimateUsage(msgs []babel.Message, reply, thinking string) babel.Usage {
	in := 0
	for _, msg := range msgs {
		in += estimateTokens(msg.Content)
		for _, tc := range msg.ToolCalls {
			in += estimateTokens(tc.Function.Arguments) + estimateTokens(tc.Function.Name)
		}
	}
	out := estimateTokens(reply) + estimateTokens(thinking)
	return babel.Usage{PromptTokens: in, CompletionTokens: out, TotalTokens: in + out}
}

// SessionUsageMsg carries live session token totals to the root (Stats + status line).
type SessionUsageMsg struct {
	In, Out, LastContext int
	Cycles, Messages     int
}

func sessionUsageCmd(m ChatModel) tea.Cmd {
	return func() tea.Msg {
		return SessionUsageMsg{
			In: m.sessionIn, Out: m.sessionOut, LastContext: m.lastContext,
			Cycles: m.sessionCycles, Messages: m.sessionMsgs,
		}
	}
}

// drainQueue pops the next queued user line and starts a new turn, or nil.
func (m *ChatModel) drainQueue() tea.Cmd {
	if len(m.queue) == 0 || m.busy {
		return nil
	}
	next := m.queue[0]
	m.queue = m.queue[1:]
	m.userEcho(next)
	_, cmd := m.beginChat(next)
	return cmd
}

// runInlineBash runs a leading-"!" command through the bash tool + Queen with
// no model round-trip. Results echo into the transcript like a normal tool call.
func (m ChatModel) runInlineBash(cmd string) (ChatModel, tea.Cmd) {
	if cmd == "" {
		m.systemLine("usage: ! <shell command>")
		return m, nil
	}
	if m.reg == nil {
		m.systemLine("✗ no tools registered")
		return m, nil
	}
	tool, ok := m.reg.Lookup("bash")
	if !ok {
		m.systemLine("✗ bash tool not available")
		return m, nil
	}
	args := map[string]any{"command": cmd}
	m.userEcho("!" + cmd)
	m.appendTurn(styleTool.Render("▸ bash: " + truncate(cmd, 60)))
	if m.gate != nil {
		switch m.gate.Decide(context.Background(), tool, args) {
		case queen.Deny:
			reason := "refused by Queen"
			if r := queen.DestructiveReason(cmd); r != "" {
				reason = r
			}
			m.appendTurn(styleError.Render("  ↳ " + reason))
			return m, nil
		case queen.Ask:
			probe := &history.Probe{Name: "bash", Arguments: args, Status: history.ProbePending}
			m.awaiting = &pendingApproval{probe: probe, tool: tool, args: args, inline: true}
			m.appendTurn(styleToolAsk.Render("  ⚠ allow bash? [y/n/a/A/d]"))
			return m, nil
		}
	}
	return m, runInlineBashCmd(tool, args)
}

// inlineBashResultMsg is like toolResultMsg but for !-mode (no probe graph).
type inlineBashResultMsg struct {
	result tools.Result
}

func runInlineBashCmd(tool tools.Tool, args map[string]any) tea.Cmd {
	return func() tea.Msg {
		return inlineBashResultMsg{result: tool.Run(context.Background(), args)}
	}
}

// liveView renders the in-flight row once deltas are arriving: the dim thinking
// block (if the model is emitting a reasoning trace) above the reply so far
// (if any content has landed yet).
func (m ChatModel) liveView() string {
	var b strings.Builder
	if m.thinkAcc != "" {
		b.WriteString(styleThinking.Render("∴ "+m.thinkAcc) + "\n")
	}
	if m.acc != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(assistantView(m.acc))
	}
	return b.String()
}

// refreshPending re-renders the in-flight row (called on each spinner tick) so the
// spinner advances. No-op when idle or already streaming text.
func (m *ChatModel) refreshPending() {
	if m.pendIdx < 0 || m.pendIdx >= len(m.lines) || m.streaming {
		return
	}
	m.lines[m.pendIdx] = m.pendingView()
	m.flush()
}

// replacePending swaps the in-flight row for the given line (accumulating reply or
// final result). Falls back to a fresh turn if the index is somehow unset.
func (m *ChatModel) replacePending(line string) {
	if m.pendIdx >= 0 && m.pendIdx < len(m.lines) {
		m.lines[m.pendIdx] = line
		m.flush()
	} else {
		m.appendTurn(line)
	}
}

// startStream is the tea.Cmd that kicks off the SSE stream. A free function (not a
// method) so the closure captures only the shared client + the snapshot request —
// never the ChatModel, which is copied by value through Update. It returns
// streamStartedMsg as soon as the reader goroutine is armed; deltas flow later via
// drainCmd. Cancellation rides on the client's own timeout; KServe cold starts can
// run minutes before the first byte.
func startStream(ctx context.Context, client *babel.Client, req babel.ChatRequest) tea.Cmd {
	return func() tea.Msg {
		ch := make(chan streamItem, 64)
		go func() {
			defer close(ch)
			rep, err := client.ChatStream(ctx, req, func(d babel.StreamDelta) {
				ch <- streamItem{delta: d.Content, reasoning: d.Reasoning}
			})
			ch <- streamItem{err: err, final: true, toolCalls: rep.ToolCalls, usage: rep.Usage}
		}()
		return streamStartedMsg{ch: ch}
	}
}

// generateTitleCmd asks the summary model for a short, human-friendly title for
// the chat (run after the first exchange). Best-effort: any error is swallowed
// by the handler. Titles are capped at 64 runes.
func generateTitleCmd(src InferenceSource, prompt, reply string) tea.Cmd {
	return func() tea.Msg {
		client, model, err := src.RoleClient(unimatrix.RoleSummary)
		if err != nil {
			return titleGeneratedMsg{err: err}
		}
		rep, err := client.Chat(context.Background(), babel.ChatRequest{
			Model: model.ID,
			Messages: []babel.Message{
				{Role: "system", Content: "Generate a concise title (at most 6 words, no quotes, no trailing period) for this conversation. Reply with the title only."},
				{Role: "user", Content: "User: " + truncate(prompt, 400) + "\nAssistant: " + truncate(reply, 400)},
			},
			MaxTokens:   24,
			Temperature: 0.3,
		})
		if err != nil {
			return titleGeneratedMsg{err: err}
		}
		title := strings.Trim(strings.TrimSpace(rep.Text), "\"'`.")
		if r := []rune(title); len(r) > 64 {
			title = string(r[:63]) + "…"
		}
		if title == "" {
			return titleGeneratedMsg{err: fmt.Errorf("empty title")}
		}
		return titleGeneratedMsg{title: title}
	}
}

// streamItemMsg. Bubble Tea runs each Cmd in its own goroutine, so blocking here is
// fine and is what turns the async stream into per-chunk Msgs. The handler re-arms.
func drainCmd(ch <-chan streamItem) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		it, ok := <-ch
		if !ok {
			return streamItemMsg{final: true}
		}
		return streamItemMsg(it)
	}
}

// assistantView renders a model reply with a primary-colored "● " prefix.
func assistantView(text string) string {
	return styleAssistant.Render("● " + text)
}

// --- popover mechanics ------------------------------------------------------

// filterCommands returns commands whose name starts with q. Swap the body for a
// fuzzy index later; the signature is the drop-in point.
func filterCommands(q string) []sugItem {
	found := commandRegistry.Search(q, 0)
	out := make([]sugItem, 0, len(found))
	for _, command := range found {
		out = append(out, sugItem{name: "/" + command.Name, desc: command.Description})
	}
	return out
}

// recomputePopover opens/closes/filters the popover from the current input and
// relayouts so the input stays pinned.
func (m *ChatModel) recomputePopover() {
	v := m.input.Value()
	open := strings.HasPrefix(v, "/") && !strings.Contains(v, " ")
	if !open {
		m.sugOpen, m.sugItems, m.sugSelected = false, nil, 0
		m.applyLayout()
		return
	}
	m.sugOpen = true
	m.sugItems = filterCommands(v)
	if len(m.sugItems) == 0 {
		m.sugOpen = false // typed /xyz with no match
	}
	if m.sugSelected >= len(m.sugItems) {
		m.sugSelected = 0
	}
	m.applyLayout()
}

func (m *ChatModel) moveSug(delta int) {
	n := len(m.sugItems)
	if n == 0 {
		return
	}
	m.sugSelected = (m.sugSelected + delta + n) % n // wraps
}

func (m *ChatModel) fillFromSelected() {
	if len(m.sugItems) == 0 {
		return
	}
	m.input.SetValue(m.sugItems[m.sugSelected].name + " ") // trailing space, no run
	m.input.CursorEnd()
	m.sugOpen = false
	m.applyLayout()
}

// suggestionView renders the popover: a window of at most maxSuggestVisible rows
// centered on the selection. Returns "" when closed/empty (so View omits it).
func (m ChatModel) suggestionView() string {
	if !m.sugOpen || len(m.sugItems) == 0 {
		return ""
	}
	items := m.sugItems
	start := 0
	if len(items) > maxSuggestVisible {
		start = m.sugSelected - maxSuggestVisible/2
		if start < 0 {
			start = 0
		}
		if max := len(items) - maxSuggestVisible; start > max {
			start = max
		}
	}
	end := start + maxSuggestVisible
	if end > len(items) {
		end = len(items)
	}
	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		label := fmt.Sprintf("%-7s  %s", m.sugItems[i].name, m.sugItems[i].desc)
		if i == m.sugSelected {
			rows = append(rows, styleMenuSel.Render("▶ "+label))
		} else {
			rows = append(rows, styleMenuUnsel.Render("  "+label))
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// --- transcript echoes (reference style) ------------------------------------

// appendTurn pushes one blank breathing row then the styled line.
func (m *ChatModel) appendTurn(line string) {
	m.lines = append(m.lines, "", line)
	m.flush()
}

// flush is the single point that re-renders the transcript into the viewport and
// pins it to the bottom. Called whenever lines changes (append or in-place edit of
// the pending row).
func (m *ChatModel) flush() {
	m.vp.SetContent(strings.Join(m.lines, "\n"))
	m.vp.GotoBottom()
}

// userEcho renders a full-width tinted block with a dim "❯ " prefix (no label).
func (m *ChatModel) userEcho(text string) {
	m.sessionMsgs++
	row := styleUserEcho.Width(m.width).Render(stylePromptPrefix.Render("❯ ") + text)
	m.appendTurn(row)
}

// slashEcho renders "❯ /cmd" in dim (used when commands are run from history path).
func (m ChatModel) slashEcho(cmd string) {
	m.appendTurn(styleSlashEcho.Render("❯ " + cmd))
}

// systemLine renders a dim, prefix-less help/system message.
func (m *ChatModel) systemLine(text string) {
	m.appendTurn(styleSystem.Render(text))
}

// View stacks the transcript, the activity strip, the popover (when open), and
// the input box.
func (m ChatModel) View() string {
	if !m.ready {
		return ""
	}
	layers := []string{m.vp.View(), m.activityView()}
	if pop := m.suggestionView(); pop != "" {
		layers = append(layers, pop)
	}
	layers = append(layers, styleInputBox.Render(m.input.View()))
	return lipgloss.JoinVertical(lipgloss.Left, layers...)
}

// now returns the current time; extracted so tests can stub it if needed later.
func now() time.Time { return time.Now() }
