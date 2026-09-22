package babel

import (
	"deepthought-cli/internal/credential"
	"encoding/json"
	"strings"
	"testing"
)

func TestCredentialsDoNotEnterModelContext(t *testing.T) {
	secret := `test-private-"quoted-token`
	credential.Register("test-privacy", secret)
	args, _ := json.Marshal(map[string]string{"value": secret})
	req := ChatRequest{Messages: []Message{{Role: "tool", Content: "output " + secret, ToolCalls: []ToolCall{{Function: FunctionCall{Arguments: string(args)}}}}}}
	clean := redactRequest(req)
	if strings.Contains(clean.Messages[0].Content, secret) {
		t.Fatal("credential entered context")
	}
	var decoded map[string]string
	if json.Unmarshal([]byte(clean.Messages[0].ToolCalls[0].Function.Arguments), &decoded) != nil || decoded["value"] != "[redacted]" {
		t.Fatal("argument redaction corrupted JSON or leaked credential")
	}
	if req.Messages[0].Content != "output "+secret {
		t.Fatal("redaction mutated shared history")
	}
}
