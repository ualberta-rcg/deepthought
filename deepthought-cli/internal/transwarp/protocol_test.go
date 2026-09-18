package transwarp

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProtocolRejectsShellShapedUnknownFields(t *testing.T) {
	_, err := DecodeRequest(strings.NewReader(`{"operation":"stop","command":"rm -rf /"}`))
	if err == nil {
		t.Fatal("unknown command field was accepted")
	}
}

func TestAsyncApprovalWithoutAttachedView(t *testing.T) {
	manager := NewManager()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan bool, 1)
	go func() {
		allowed, _ := manager.WaitApproval(ctx, "session")
		done <- allowed
	}()
	for i := 0; i < 100; i++ {
		manager.mu.RLock()
		session := manager.sessions["session"]
		waiting := session != nil && session.State == "waiting_approval"
		manager.mu.RUnlock()
		if waiting {
			break
		}
		time.Sleep(time.Millisecond)
	}
	allow := true
	response := manager.Handle(Request{Operation: ResolveApproval, SessionID: "session", Allowed: &allow})
	if !response.OK || !<-done {
		t.Fatalf("approval failed: %+v", response)
	}
}
