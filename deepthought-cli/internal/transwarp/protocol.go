// Package transwarp defines the fixed control vocabulary used by resident
// DeepThought processes. Request deliberately has no command or argv field.
package transwarp

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Operation string

const (
	RunSchedule     Operation = "run_schedule"
	RefreshConfig   Operation = "refresh_config"
	RefreshSkills   Operation = "refresh_skills"
	ReportStatus    Operation = "report_status"
	DisplayMessage  Operation = "display_message"
	Drain           Operation = "drain"
	Stop            Operation = "stop"
	ResolveApproval Operation = "resolve_approval"
	NewSession      Operation = "new_session"
	AttachSession   Operation = "attach_session"
)

func (o Operation) Valid() bool {
	switch o {
	case RunSchedule, RefreshConfig, RefreshSkills, ReportStatus, DisplayMessage, Drain, Stop, ResolveApproval, NewSession, AttachSession:
		return true
	default:
		return false
	}
}

type Request struct {
	Operation Operation `json:"operation"`
	SessionID string    `json:"session_id,omitempty"`
	Message   string    `json:"message,omitempty"`
	Allowed   *bool     `json:"allowed,omitempty"`
}

type SessionStatus struct {
	ID              string `json:"id"`
	State           string `json:"state"`
	Attached        int    `json:"attached"`
	WaitingApproval bool   `json:"waiting_approval"`
}

type Response struct {
	OK       bool            `json:"ok"`
	Error    string          `json:"error,omitempty"`
	Sessions []SessionStatus `json:"sessions,omitempty"`
	Message  string          `json:"message,omitempty"`
}

func DecodeRequest(r io.Reader) (Request, error) {
	var request Request
	decoder := json.NewDecoder(io.LimitReader(r, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return Request{}, fmt.Errorf("transwarp: decode: %w", err)
	}
	if !request.Operation.Valid() {
		return Request{}, fmt.Errorf("transwarp: invalid operation %q", request.Operation)
	}
	if strings.ContainsRune(request.SessionID, '/') {
		return Request{}, fmt.Errorf("transwarp: invalid session id")
	}
	return request, nil
}
