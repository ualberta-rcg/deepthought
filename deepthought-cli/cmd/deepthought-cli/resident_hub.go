package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"deepthought-cli/internal/app"
	"deepthought-cli/internal/history"
	"deepthought-cli/internal/transwarp"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

type residentHub struct {
	mu      sync.Mutex
	ctx     context.Context
	deps    app.Deps
	store   *history.SQLiteStore
	runners map[string]*app.SessionRunner
}

func (h *residentHub) start(id string) (*app.SessionRunner, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.runners[id]; r != nil {
		select {
		case <-r.Done():
			delete(h.runners, id)
		default:
			return r, nil
		}
	}
	resume := ""
	if id == "" {
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return nil, err
		}
		id = "session_" + hex.EncodeToString(token[:])
	} else {
		var prior app.ResidentState
		if err := h.store.GetRecord("resident", id, &prior); err != nil {
			return nil, fmt.Errorf("unknown resident session")
		}
		resume = prior.CollectiveID
		if resume != "" {
			coll, err := h.store.GetCollective(resume)
			if err != nil {
				return nil, err
			}
			for _, inc := range coll.Incursions {
				if inc.Status == history.IncursionStreaming || inc.Status == history.IncursionDispatching {
					inc.Interrupt("Resident process stopped. Tool outcomes may be unknown; verify side effects before resuming.")
					if err := h.store.SaveObject(inc); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	r, err := app.NewSessionRunner(h.ctx, id, h.deps, h.store, resume)
	if err != nil {
		return nil, err
	}
	h.runners[id] = r
	return r, nil
}
func (h *residentHub) control(request transwarp.Request) (transwarp.Response, bool) {
	switch request.Operation {
	case transwarp.NewSession:
		r, err := h.start("")
		if err != nil {
			return transwarp.Response{Error: err.Error()}, true
		}
		return transwarp.Response{OK: true, Message: r.ID}, true
	case transwarp.ReportStatus:
		rows, err := h.store.Records("resident")
		if err != nil {
			return transwarp.Response{Error: err.Error()}, true
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		response := transwarp.Response{OK: true}
		for _, row := range rows {
			var state app.ResidentState
			if json.Unmarshal(row.Data, &state) != nil {
				continue
			}
			attached := 0
			if r := h.runners[state.ID]; r != nil {
				state, attached = r.Status()
			} else if state.State != "stopped" && state.State != "idle" {
				state.State = "recovery_required"
			}
			response.Sessions = append(response.Sessions, transwarp.SessionStatus{ID: state.ID, State: state.State, Attached: attached, WaitingApproval: state.State == "waiting_approval"})
		}
		return response, true
	case transwarp.Stop:
		h.mu.Lock()
		defer h.mu.Unlock()
		if request.SessionID != "" {
			r := h.runners[request.SessionID]
			if r == nil {
				return transwarp.Response{Error: "active session not found"}, true
			}
			r.Stop()
			delete(h.runners, request.SessionID)
			return transwarp.Response{OK: true}, true
		}
		for _, r := range h.runners {
			r.Stop()
		}
		return transwarp.Response{}, false
	case transwarp.ResolveApproval:
		return transwarp.Response{Error: "attach the session and review its current approval before responding"}, true
	}
	return transwarp.Response{}, false
}
func (h *residentHub) stream(conn net.Conn, request transwarp.Request) bool {
	if request.Operation != transwarp.AttachSession {
		return false
	}
	r, err := h.start(request.SessionID)
	if err != nil {
		json.NewEncoder(conn).Encode(transwarp.Response{Error: err.Error()})
		return true
	}
	if err := json.NewEncoder(conn).Encode(transwarp.Response{OK: true, Message: r.ID}); err != nil {
		return true
	}
	conn.SetDeadline(time.Time{})
	frames, detach := r.Subscribe()
	defer detach()
	done := make(chan struct{})
	defer close(done)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			var input app.RunnerInput
			if json.Unmarshal(scanner.Bytes(), &input) != nil {
				break
			}
			if err := r.Input(input); err != nil {
				continue
			}
			select {
			case <-done:
				return
			default:
			}
		}
		conn.Close()
	}()
	encoder := json.NewEncoder(conn)
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				return true
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := encoder.Encode(frame); err != nil {
				return true
			}
			if frame.Closed {
				return true
			}
		case <-h.ctx.Done():
			return true
		case <-readerDone:
			return true
		}
	}
}
