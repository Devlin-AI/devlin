package message

import (
	"encoding/json"

	"github.com/devlin-ai/devlin/internal/store"
)

func FromStore(m store.Message) Message {
	return Message{
		ID:         m.ID,
		SessionID:  m.SessionID,
		Role:       Role(m.Role),
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
		ToolName:   m.ToolName,
		Thinking:   m.Thinking,
		Timestamp:  m.Timestamp,
		ToolCalls:  UnmarshalToolCalls(m.ToolCallsJSON),
	}
}

func UnmarshalToolCalls(raw []byte) []ToolCall {
	if len(raw) == 0 {
		return nil
	}
	var calls []ToolCall
	json.Unmarshal(raw, &calls)
	return calls
}

func MarshalToolCalls(calls []ToolCall) []byte {
	if calls == nil {
		return nil
	}
	b, err := json.Marshal(calls)
	if err != nil {
		return nil
	}
	return b
}

func MarshalToolDefs(defs []ToolDef) []byte {
	if defs == nil {
		return nil
	}
	b, err := json.Marshal(defs)
	if err != nil {
		return nil
	}
	return b
}

func MarshalUsage(u *Usage) []byte {
	if u == nil {
		return nil
	}
	b, err := json.Marshal(u)
	if err != nil {
		return nil
	}
	return b
}
