package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"deepthought-cli/internal/queen"
	"deepthought-cli/internal/tools"
)

func newWrapChat(t *testing.T, w, h int) ChatModel {
	t.Helper()
	m := NewChatModel(
		fakeSource{},
		tools.NewRegistry(tools.NewBash(), tools.NewRead()),
		queen.NewGate(queen.Review),
		"test_session",
		fileSource(t.TempDir()),
	)
	return m.Resize(w, h)
}

func maxRowWidth(s string) (int, string) {
	widest, row := 0, ""
	for _, r := range strings.Split(s, "\n") {
		if w := ansi.StringWidth(r); w > widest {
			widest, row = w, r
		}
	}
	return widest, row
}

func transcriptRows(m ChatModel) string {
	rows := make([]string, len(m.lines))
	for i, l := range m.lines {
		rows[i] = l.render(m.width)
	}
	return strings.Join(rows, "\n")
}

func TestTranscriptRowsNeverExceedWidth(t *testing.T) {
	long := strings.Repeat("x", 300)
	para := strings.Repeat("the quick brown fox jumps over the lazy dog ", 12)
	url := "https://example.org/" + strings.Repeat("path/", 40)
	m := newWrapChat(t, 60, 30)
	m.userEcho(para + long)
	m.appendLine(chatLine{kind: lineAssistant, text: para + "\n" + url + "\n```\n" + long + "\n```"})
	m.systemLine("system " + long)
	m.appendTurn(styleError.Render("✗ " + url))
	m.lines = append(m.lines, chatLine{kind: lineLive, think: para, text: long})
	m.flush()

	if w, row := maxRowWidth(transcriptRows(m)); w > 60 {
		t.Fatalf("row is %d cells wide at chatW=60: %q", w, row)
	}
	if w, row := maxRowWidth(m.vp.View()); w > 60 {
		t.Fatalf("viewport row is %d cells wide: %q", w, row)
	}
	if !strings.Contains(ansi.Strip(transcriptRows(m)), codeGutter) {
		t.Fatal("long code line was not hard-wrapped with a continuation gutter")
	}
}

func TestTranscriptReflowsOnResize(t *testing.T) {
	m := newWrapChat(t, 120, 30)
	m.appendLine(chatLine{kind: lineAssistant, text: strings.Repeat("word ", 40)})
	wide := len(strings.Split(transcriptRows(m), "\n"))
	m = m.Resize(80, 30)
	if w, row := maxRowWidth(m.vp.GetContent()); w > 80 {
		t.Fatalf("after resize a row is %d cells wide: %q", w, row)
	}
	if narrow := len(strings.Split(transcriptRows(m), "\n")); narrow <= wide {
		t.Fatalf("narrowing did not reflow: %d rows at 120, %d at 80", wide, narrow)
	}
}

func TestStreamingKeepsScrollPositionWhenReaderScrolledUp(t *testing.T) {
	m := newWrapChat(t, 60, 12)
	for i := 0; i < 40; i++ {
		m.systemLine("history row")
	}
	m.lines = append(m.lines, chatLine{}, chatLine{})
	m.pendIdx = len(m.lines) - 1
	m.flush()
	m.vp.SetYOffset(5)
	for i := 0; i < 20; i++ {
		m.acc += "delta "
		m.replacePending(m.liveView())
	}
	if got := m.vp.YOffset(); got != 5 {
		t.Fatalf("YOffset moved to %d while the reader was scrolled up", got)
	}

	m.vp.GotoBottom()
	m.acc += strings.Repeat("more text ", 30)
	m.replacePending(m.liveView())
	if !m.vp.AtBottom() {
		t.Fatal("follow mode lost: viewport not pinned to the bottom after a delta")
	}
}

func TestWrapStyledReappliesColour(t *testing.T) {
	s := styleError.Render(strings.Repeat("error ", 20))
	rows := strings.Split(wrapStyled(s, 30), "\n")
	if len(rows) < 2 {
		t.Fatalf("expected wrapping, got %d rows", len(rows))
	}
	lead := leadingSGR.FindString(rows[0])
	for i, r := range rows {
		if !strings.HasSuffix(r, "\x1b[m") {
			t.Fatalf("row %d does not reset its style: %q", i, r)
		}
		if lead != "" && !strings.HasPrefix(r, lead) {
			t.Fatalf("row %d lost the leading style: %q", i, r)
		}
	}
}
