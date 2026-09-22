package babel

import "deepthought-cli/internal/credential"

// Scrub known local credentials at the final boundary to every model adapter.
// Copy the messages: request assembly may share slices with persisted history.
func redactRequest(req ChatRequest) ChatRequest {
	req.Messages = append([]Message(nil), req.Messages...)
	for i := range req.Messages {
		m := &req.Messages[i]
		m.Content = credential.Redact(m.Content)
		m.ToolCalls = append([]ToolCall(nil), m.ToolCalls...)
		for j := range m.ToolCalls {
			m.ToolCalls[j].Function.Arguments = credential.RedactJSON(m.ToolCalls[j].Function.Arguments)
		}
	}
	return req
}
