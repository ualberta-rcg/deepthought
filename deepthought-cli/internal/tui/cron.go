package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"deepthought-cli/internal/cron"
)

// cron.go: the F8 Cron screen — manage the USER'S REAL crontab with a hard
// safety gate. Edits stage into a pending table; `P` shows the diff; `y` from
// the diff hits an explicit y/N confirm (ALWAYS, regardless of Queen op mode —
// replacing a crontab is destructive-class); apply installs through
// internal/cron (which backs the table up first) and folds the tracking
// registry.

// cronClient is the seam the screen programs against (the real client is
// *cron.Client; tests fake it).
type cronClient interface {
	List(ctx context.Context) ([]cron.Line, error)
	Install(ctx context.Context, lines []string) error
	Undo(ctx context.Context) error
	LoadRegistry() (cron.Registry, error)
	SaveRegistry(cron.Registry) error
}

// realCronClient adapts *cron.Client + the registry file to cronClient.
type realCronClient struct {
	c   *cron.Client
	dir string
}

func (r realCronClient) List(ctx context.Context) ([]cron.Line, error) {
	return r.c.List(ctx)
}
func (r realCronClient) Install(ctx context.Context, lines []string) error {
	return r.c.Install(ctx, lines)
}
func (r realCronClient) Undo(ctx context.Context) error { return r.c.Undo(ctx) }
func (r realCronClient) LoadRegistry() (cron.Registry, error) {
	return cron.LoadRegistry(r.dir)
}
func (r realCronClient) SaveRegistry(reg cron.Registry) error { return reg.Save(r.dir) }

type cronView int

const (
	cvList    cronView = iota // crontab + pending sections
	cvEdit                    // inline raw-line editor (add or edit)
	cvDiff                    // old vs new table
	cvConfirm                 // explicit y/N gate before apply/undo
	cvDiscard                 // esc with staged changes: discard?
)

// CronModel is the F8 Cron screen.
type CronModel struct {
	client cronClient
	reg    cron.Registry
	lines  []cron.Line // the live table as last listed
	preg   cron.Registry

	pending *pendingTable // non-nil while changes are staged
	view    cronView
	cursor  int

	edit    *fieldEdit // cvEdit raw-line editor
	editIdx int        // index into pending rows, -1 = adding
	confirm string     // what cvConfirm will do ("apply" | "undo")
	toast   string
	err     string
	vp      viewport.Model
	width   int
	height  int
}

// pendingTable is the staged next crontab: raw lines + which are new.
type pendingTable struct {
	rows []string // full raw table (env/comments included)
}

func NewCronModel(dataDir string) CronModel {
	dir := dataDir + "/cron"
	return CronModel{client: realCronClient{c: cron.NewClient(dir), dir: dir}, view: cvList, vp: viewport.New()}
}

func newCronModelWith(c cronClient) CronModel {
	return CronModel{client: c, view: cvList, vp: viewport.New()}
}

// CapturingKeys: an inline editor or the y/N confirm is consuming keys.
func (m CronModel) CapturingKeys() bool {
	return (m.edit != nil && m.edit.kind == fText) || m.view == cvConfirm || m.view == cvDiscard
}

func (m CronModel) Init() tea.Cmd {
	return m.reloadCmd()
}

type cronLoadedMsg struct {
	lines []cron.Line
	reg   cron.Registry
	err   error
}

func (m CronModel) reloadCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		lines, err := client.List(context.Background())
		if err != nil {
			return cronLoadedMsg{err: err}
		}
		reg, err := client.LoadRegistry()
		return cronLoadedMsg{lines: lines, reg: reg, err: err}
	}
}

func (m CronModel) Update(msg tea.Msg) (CronModel, tea.Cmd) {
	switch msg := msg.(type) {
	case cronLoadedMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.err = ""
		m.lines, m.reg, m.preg = msg.lines, msg.reg, msg.reg
		if m.pending == nil {
			m.stage() // start staging from the live table
		}
		return m, nil
	case cronAppliedMsg:
		if msg.err != nil {
			m.err, m.toast = msg.err.Error(), ""
			return m, nil
		}
		m.pending = nil
		m.view, m.confirm = cvList, ""
		return m, m.reloadCmd()
	}
	if m.edit != nil {
		if kp, ok := msg.(tea.KeyPressMsg); ok {
			switch kp.String() {
			case "enter":
				return m.commitEdit()
			case "esc":
				m.edit = nil
				m.view = cvList
				return m, nil
			}
			m.edit.update(kp)
		} else if m.edit.kind == fText {
			var cmd tea.Cmd
			m.edit.input, cmd = m.edit.input.Update(msg)
			return m, cmd
		}
		return m, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch m.view {
	case cvConfirm, cvDiscard:
		return m.updateConfirm(key)
	}
	switch key.String() {
	case "esc", "q":
		if m.view == cvDiff {
			m.view = cvList // back to the list — never the discard gate from a review
			return m, nil
		}
		if m.dirty() {
			m.view = cvDiscard
			return m, nil
		}
		return m, Back()
	case "pgup", "pgdown", "home", "end":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case "up", "k":
		if rows := m.rowCount(); rows > 0 {
			m.cursor = (m.cursor - 1 + rows) % rows
		}
	case "down", "j":
		if rows := m.rowCount(); rows > 0 {
			m.cursor = (m.cursor + 1) % rows
		}
	case "enter":
		if m.view == cvList {
			return m.beginEdit()
		}
	case "a":
		if m.view == cvList {
			return m.beginAdd()
		}
	case "e":
		if m.view == cvList {
			return m.beginEdit()
		}
	case "d":
		if m.view == cvList {
			return m.stageDelete()
		}
	case "P":
		if m.dirty() {
			m.view = cvDiff
			return m, nil
		}
	case "y":
		if m.view == cvDiff {
			m.view, m.confirm = cvConfirm, "apply"
			return m, nil
		}
	case "u":
		if m.view == cvList {
			m.view, m.confirm = cvConfirm, "undo"
		}
		return m, nil
	case "r":
		return m, m.reloadCmd()
	}
	return m, nil
}

type cronAppliedMsg struct{ err error }

// updateConfirm: y executes, anything else aborts. ALWAYS asks — replacing a
// crontab is destructive-class, so no op-mode shortcut exists.
func (m CronModel) updateConfirm(key tea.KeyPressMsg) (CronModel, tea.Cmd) {
	if key.String() == "y" {
		if m.view == cvDiscard {
			m.pending, m.view = nil, cvList
			return m, m.reloadCmd()
		}
		client := m.client
		if m.confirm == "undo" {
			return m, func() tea.Msg {
				return cronAppliedMsg{err: client.Undo(context.Background())}
			}
		}
		if m.pending == nil {
			m.view, m.confirm = cvList, ""
			return m, nil
		}
		rows := m.pending.rows
		return m, func() tea.Msg {
			return cronAppliedMsg{err: client.Install(context.Background(), rows)}
		}
	}
	m.view, m.confirm = cvList, ""
	return m, nil
}

// --- staging ------------------------------------------------------------------

func (m *CronModel) stage() {
	rows := make([]string, len(m.lines))
	for i, l := range m.lines {
		rows[i] = l.Raw
	}
	m.pending = &pendingTable{rows: rows}
	m.cursor = 0 // indexes the entries list (see pendingEntries), not the raw rows
}

func (m CronModel) dirty() bool {
	if m.pending == nil {
		return false
	}
	live := make([]string, len(m.lines))
	for i, l := range m.lines {
		live[i] = l.Raw
	}
	return strings.Join(live, "\n") != strings.Join(m.pending.rows, "\n")
}

func (m CronModel) pendingEntries() (out []int) {
	if m.pending == nil {
		return nil
	}
	for i, raw := range m.pending.rows {
		if l := cron.Parse(raw); len(l) == 1 && l[0].Kind == cron.LineEntry {
			out = append(out, i)
		}
	}
	return out
}

func (m CronModel) beginEdit() (CronModel, tea.Cmd) {
	idx := m.pendingEntryAtCursor()
	if idx < 0 {
		return m, nil
	}
	m.edit = newTextEdit("entry", m.pending.rows[idx], false)
	m.edit.setWidth(m.width - 8)
	m.editIdx, m.view = idx, cvEdit
	return m, m.edit.input.Focus()
}

func (m CronModel) beginAdd() (CronModel, tea.Cmd) {
	m.pending.rows = append(m.pending.rows, "0 9 * * * echo hello")
	idx := len(m.pending.rows) - 1
	m.edit = newTextEdit("entry", m.pending.rows[idx], false)
	m.edit.setWidth(m.width - 8)
	m.editIdx, m.view, m.cursor = idx, cvEdit, len(m.pendingEntries())-1
	return m, m.edit.input.Focus()
}

func (m CronModel) commitEdit() (CronModel, tea.Cmd) {
	raw := strings.TrimSpace(m.edit.value())
	if raw == "" {
		m.edit, m.view = nil, cvList
		return m.stageDeleteAt(m.editIdx) // cleared = delete
	}
	if l := cron.Parse(raw); len(l) != 1 || l[0].Kind != cron.LineEntry {
		m.toast = "not a valid crontab entry — fix it (esc cancels and removes the row)"
		return m, nil // stay in the editor; nothing junk stays staged
	}
	m.pending.rows[m.editIdx] = raw
	m.edit, m.toast, m.view = nil, "", cvList
	return m, nil
}

func (m CronModel) stageDelete() (CronModel, tea.Cmd) {
	return m.stageDeleteAt(m.pendingEntryAtCursor())
}

func (m CronModel) stageDeleteAt(idx int) (CronModel, tea.Cmd) {
	if idx < 0 || idx >= len(m.pending.rows) {
		return m, nil
	}
	m.pending.rows = append(m.pending.rows[:idx], m.pending.rows[idx+1:]...)
	if m.cursor > 0 {
		m.cursor--
	}
	return m, nil
}

func (m CronModel) pendingEntryAtCursor() int {
	entries := m.pendingEntries()
	if len(entries) == 0 || m.cursor < 0 || m.cursor >= len(entries) {
		return -1
	}
	return entries[m.cursor]
}

// --- rows + view -----------------------------------------------------------------

func (m CronModel) rowCount() int {
	// The cursor walks the entries; review/undo are key actions, not rows.
	return len(m.pendingEntries())
}

func (m CronModel) rows() []string {
	if m.pending == nil {
		// Still loading (or the load failed — View shows the error toast).
		return []string{dimNote("  loading crontab…")}
	}
	var out []string

	added, removed := m.preg.DiffSinceLast(m.entryLines())
	removedSet := map[string]bool{}
	for _, r := range removed {
		removedSet[r.Hash] = true
	}
	addedSet := map[string]bool{}
	for _, a := range added {
		addedSet[a.Hash] = true
	}

	// » Your crontab — one row per entry, the cursor marked like every other list.
	body := []string{}
	for k, idx := range m.pendingEntries() {
		l := cron.Parse(m.pending.rows[idx])[0]
		row := fmt.Sprintf("%s %s", truncatePad(cron.Humanize(l.Schedule), 14), clipLine(l.Command, max(20, m.width-40)))
		if addedSet[l.Hash] {
			row = styleToolResult.Render("+") + row
		}
		body = append(body, m.mark(k, row))
	}
	if len(body) == 0 {
		body = []string{"  " + emptyRow("cron entries")}
	}
	out = append(out, Section{
		Title:  "Your crontab",
		Extra:  fmt.Sprintf("%d entries", len(m.pendingEntries())),
		Rows:   body,
		Note:   "edits are staged; every apply takes a backup first",
		Source: "crontab -l",
	}.Render()...)

	// » Removed since last visit
	if len(removed) > 0 {
		rb := []string{}
		for _, r := range removed {
			rb = append(rb, "  "+styleSettingsFoot.Render("− "+clipLine(r.Command, max(20, m.width-44))))
		}
		out = append(out, Section{Title: "Removed since last visit", Rows: rb}.Render()...)
	}

	// » Pending changes
	if m.dirty() {
		addN, delN := m.pendingCounts()
		out = append(out, Section{
			Title: "Pending changes",
			Extra: fmt.Sprintf("+%d −%d — P review · y apply", addN, delN),
		}.Render()...)
	}
	return out
}

func (m CronModel) entryLines() []cron.Line {
	var out []cron.Line
	for _, l := range m.lines {
		if l.Kind == cron.LineEntry {
			out = append(out, l)
		}
	}
	return out
}

func (m CronModel) liveRaw() string {
	raws := make([]string, len(m.lines))
	for i, l := range m.lines {
		raws[i] = l.Raw
	}
	return strings.Join(raws, "\n")
}

func (m CronModel) pendingCounts() (add, del int) {
	prev := cron.Parse(m.liveRaw())
	cur := cron.Parse(strings.Join(m.pending.rows, "\n"))
	a, d := cron.Diff(prev, cur)
	return len(a), len(d)
}

// diffRows renders the old vs new table for cvDiff.
func (m CronModel) diffRows() []string {
	prev := cron.Parse(m.liveRaw())
	cur := cron.Parse(strings.Join(m.pending.rows, "\n"))
	added, removed := cron.Diff(prev, cur)
	addSet := map[string]bool{}
	for _, l := range added {
		addSet[l.Hash] = true
	}
	body := []string{}
	for _, l := range cur {
		if l.Kind != cron.LineEntry {
			continue
		}
		mark := "  "
		if addSet[l.Hash] {
			mark = styleToolResult.Render("+ ")
		}
		body = append(body, mark+cron.Humanize(l.Schedule)+"  "+clipLine(l.Command, max(20, m.width-36)))
	}
	for _, l := range removed {
		body = append(body, styleError.Render("− ")+cron.Humanize(l.Schedule)+"  "+clipLine(l.Command, max(20, m.width-36)))
	}
	return Section{
		Title: "Review changes",
		Extra: fmt.Sprintf("+%d −%d", len(added), len(removed)),
		Rows:  body,
		Note:  "y applies the NEW table; the current one is backed up first · esc back",
	}.Render()
}

// Resize sizes the viewport.
func (m CronModel) Resize(w, h int) CronModel {
	m.width, m.height = w, h
	m.vp.SetWidth(w - 4)
	bh := h - 2 - 1 - 1
	if bh < 1 {
		bh = 1
	}
	m.vp.SetHeight(bh)
	if m.edit != nil {
		m.edit.setWidth(w - 8)
	}
	return m
}

func (m CronModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	var content string
	switch m.view {
	case cvDiff:
		content = strings.Join(m.diffRows(), "\n")
	default:
		content = strings.Join(m.rows(), "\n")
	}
	m.vp.SetContent(content)

	title := screenTitle("Cron")
	right := m.toast
	if m.err != "" {
		right = "✗ " + m.err
	}
	if right != "" {
		gap := m.width - 6 - lipgloss.Width(title) - lipgloss.Width(styleToast.Render(right))
		if gap < 1 {
			gap = 1
		}
		title += strings.Repeat(" ", gap) + styleToast.Render(right)
	}
	if m.edit != nil {
		content = styleEditActive.Render("▶ ") + m.edit.view(m.width)
		m.vp.SetContent(content)
	}
	m.keepCursorVisible()
	prompt := ""
	switch m.view {
	case cvConfirm:
		prompt = styleToolAsk.Render(fmt.Sprintf("  %s the crontab? y/N   (a backup is taken first)", m.confirm))
	case cvDiscard:
		prompt = styleToolAsk.Render("  discard staged changes? y/N")
	}
	if prompt != "" {
		m.vp.SetContent(strings.Join(m.rows(), "\n") + "\n" + prompt)
	}
	return AppScreenScroll(m.width, m.height, title, m.vp.View(), m.vp.Height(), KeyBar(m.keybar()))
}

// keepCursorVisible scrolls the viewport so the cursor row is on screen.
func (m *CronModel) keepCursorVisible() {
	h := m.vp.Height()
	if h <= 0 || m.view != cvList {
		return
	}
	y := m.vp.YOffset()
	if m.cursor < y {
		m.vp.SetYOffset(m.cursor)
	} else if m.cursor >= y+h {
		m.vp.SetYOffset(m.cursor - h + 1)
	}
}

// mark renders one list row with the cursor highlight.
func (m CronModel) mark(i int, text string) string {
	if i == m.cursor {
		return styleMenuSel.Render("▶ " + text)
	}
	return styleMenuUnsel.Render("  " + text)
}

func (m CronModel) keybar() []KeyHint {
	switch m.view {
	case cvConfirm, cvDiscard:
		return []KeyHint{{"y", "yes"}, {"any", "no"}}
	case cvEdit:
		return []KeyHint{{"enter", "save"}, {"esc", "cancel"}}
	case cvDiff:
		return []KeyHint{{"y", "apply"}, {"esc", "back"}}
	}
	hints := []KeyHint{{"↑↓", "move"}, {"enter", "edit"}}
	if m.dirty() {
		hints = append(hints, KeyHint{"P", "diff"}, KeyHint{"y", "apply"})
	}
	return append(hints, KeyHint{"a", "add"}, KeyHint{"d", "delete"}, KeyHint{"u", "undo"}, KeyHint{"r", "refresh"}, KeyHint{"esc", "back"})
}
