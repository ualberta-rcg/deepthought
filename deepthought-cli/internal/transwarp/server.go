package transwarp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Session struct {
	ID       string
	State    string
	Attached int
	Approval chan bool
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	draining bool
	stop     chan struct{}
	once     sync.Once
	Audit    func(Request)
	// OnRefresh is the live-editing hook: re-read config (refresh_config) or
	// skills (refresh_skills) without a restart. Wired by the host process.
	OnRefresh func(operation string) error
}

func NewManager() *Manager {
	return &Manager{sessions: map[string]*Session{}, stop: make(chan struct{})}
}

func (m *Manager) Ensure(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session := m.sessions[id]; session != nil {
		return session
	}
	session := &Session{ID: id, State: "resident", Approval: make(chan bool, 1)}
	m.sessions[id] = session
	return session
}

// WaitApproval blocks independently of any TUI connection. Another process
// resolves it through the fixed ResolveApproval operation.
func (m *Manager) WaitApproval(ctx context.Context, sessionID string) (bool, error) {
	session := m.Ensure(sessionID)
	m.mu.Lock()
	session.State = "waiting_approval"
	m.mu.Unlock()
	select {
	case allowed := <-session.Approval:
		m.mu.Lock()
		session.State = "resident"
		m.mu.Unlock()
		return allowed, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (m *Manager) Handle(request Request) Response {
	if m.Audit != nil {
		m.Audit(request)
	}
	switch request.Operation {
	case ReportStatus:
		m.mu.RLock()
		defer m.mu.RUnlock()
		out := Response{OK: true}
		for _, session := range m.sessions {
			out.Sessions = append(out.Sessions, SessionStatus{
				ID: session.ID, State: session.State, Attached: session.Attached,
				WaitingApproval: len(session.Approval) == 0 && session.State == "waiting_approval",
			})
		}
		return out
	case DisplayMessage:
		if request.SessionID == "" {
			return Response{Error: "session_id required"}
		}
		m.Ensure(request.SessionID)
		return Response{OK: true}
	case ResolveApproval:
		if request.Allowed == nil {
			return Response{Error: "allowed required"}
		}
		m.mu.RLock()
		session := m.sessions[request.SessionID]
		m.mu.RUnlock()
		if session == nil {
			return Response{Error: "session not found"}
		}
		select {
		case session.Approval <- *request.Allowed:
			m.mu.Lock()
			session.State = "resident"
			m.mu.Unlock()
			return Response{OK: true}
		default:
			return Response{Error: "session is not waiting for approval"}
		}
	case Drain:
		m.mu.Lock()
		m.draining = true
		m.mu.Unlock()
		return Response{OK: true, Message: "draining"}
	case Stop:
		if request.SessionID != "" {
			m.mu.Lock()
			if _, ok := m.sessions[request.SessionID]; !ok {
				m.mu.Unlock()
				return Response{Error: "session not found"}
			}
			delete(m.sessions, request.SessionID)
			m.mu.Unlock()
			return Response{OK: true}
		}
		m.once.Do(func() { close(m.stop) })
		return Response{OK: true, Message: "daemon stopping"}
	case RefreshConfig, RefreshSkills:
		// Live editing: the daemon re-reads config/skills through the hook
		// its host wires at construction (nil = nothing to reload yet).
		if m.OnRefresh != nil {
			if err := m.OnRefresh(string(request.Operation)); err != nil {
				return Response{Error: err.Error()}
			}
		}
		return Response{OK: true, Message: string(request.Operation) + " reloaded"}
	case RunSchedule:
		return Response{OK: true, Message: string(request.Operation) + " accepted"}
	default:
		return Response{Error: "unsupported operation"}
	}
}

func SocketPath() string {
	root := os.Getenv("XDG_RUNTIME_DIR")
	if root == "" {
		root = filepath.Join(os.TempDir(), fmt.Sprintf("deepthought-cli-%d", os.Getuid()))
	}
	return filepath.Join(root, "deepthought-cli.sock")
}

func (m *Manager) Serve(ctx context.Context, socket string) error {
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	// Clear a stale socket from a crashed prior run; failure here is fine
	// (no socket = nothing to clear), and a real conflict surfaces from
	// net.Listen below.
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}
	go func() {
		select {
		case <-ctx.Done():
		case <-m.stop:
		}
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || isClosed(m.stop) {
				return nil
			}
			return err
		}
		go m.serveConn(conn)
	}
}

func (m *Manager) serveConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	request, err := DecodeRequest(conn)
	if err != nil {
		_ = json.NewEncoder(conn).Encode(Response{Error: err.Error()})
		return
	}
	_ = json.NewEncoder(conn).Encode(m.Handle(request))
}

func Send(ctx context.Context, socket string, request Request) (Response, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return Response{}, err
	}
	var response Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return Response{}, err
	}
	if !response.OK {
		return response, errors.New(response.Error)
	}
	return response, nil
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
