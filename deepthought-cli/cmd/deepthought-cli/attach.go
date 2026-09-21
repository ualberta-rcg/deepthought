package main

import (
	tea "charm.land/bubbletea/v2"
	"deepthought-cli/internal/app"
	"deepthought-cli/internal/transwarp"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

type attachment struct {
	conn    net.Conn
	decoder *json.Decoder
	frame   app.RunnerFrame
	err     error
}
type attachError struct{ err error }

func (m attachment) next() tea.Cmd {
	return func() tea.Msg {
		var frame app.RunnerFrame
		if err := m.decoder.Decode(&frame); err != nil {
			return attachError{err}
		}
		return frame
	}
}
func (m attachment) Init() tea.Cmd { return m.next() }
func (m attachment) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var input app.RunnerInput
	switch msg := msg.(type) {
	case app.RunnerFrame:
		m.frame = msg
		if msg.Closed {
			return m, tea.Quit
		}
		return m, m.next()
	case attachError:
		m.err = msg.err
		return m, tea.Quit
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+\\" {
			return m, tea.Quit
		}
		input.Key = &msg
		input.ApprovalID = m.frame.ApprovalID
	case tea.WindowSizeMsg:
		input.Width, input.Height = msg.Width, msg.Height
	case tea.PasteMsg:
		input.Paste = &msg.Content
		input.ApprovalID = m.frame.ApprovalID
	default:
		return m, nil
	}
	m.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(m.conn).Encode(input); err != nil {
		m.err = err
		return m, tea.Quit
	}
	return m, nil
}
func (m attachment) View() tea.View {
	v := tea.NewView(m.frame.View)
	v.AltScreen = true
	if m.frame.CursorVisible {
		v.Cursor = tea.NewCursor(m.frame.CursorX, m.frame.CursorY)
	}
	return v
}
func runAttached(id string) error {
	conn, err := net.DialTimeout("unix", transwarp.SocketPath(), 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(conn).Encode(transwarp.Request{Operation: transwarp.AttachSession, SessionID: id}); err != nil {
		return err
	}
	decoder := json.NewDecoder(conn)
	var response transwarp.Response
	if err := decoder.Decode(&response); err != nil {
		return err
	}
	if !response.OK {
		return fmt.Errorf("%s", response.Error)
	}
	conn.SetDeadline(time.Time{})
	program := tea.NewProgram(attachment{conn: conn, decoder: decoder}, app.ProgramColorOpts(os.Environ())...)
	final, runErr := program.Run()
	err = runErr
	if state, ok := final.(attachment); ok && err == nil {
		err = state.err
	}
	fmt.Printf("Detached. Resume with: deepthought-cli attach %s\n", response.Message)
	return err
}
