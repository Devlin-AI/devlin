package session

import "github.com/devlin-ai/devlin/internal/channel"

type Event = channel.OutboundMessage

type EventEmitter interface {
	SendEvent(Event)
	SendToolStart(toolCall)
}

type SubagentEmitter struct {
	parent  func(Event)
	depth   int
	desc    string
	toolMap map[string]bool
}

func NewSubagentEmitter(parent func(Event), depth int, desc string) *SubagentEmitter {
	return &SubagentEmitter{
		parent:  parent,
		depth:   depth,
		desc:    desc,
		toolMap: make(map[string]bool),
	}
}

func (e *SubagentEmitter) SendEvent(evt Event) {
	evt.SubagentDepth = e.depth
	evt.SubagentDesc = e.desc
	e.parent(evt)
}

func (e *SubagentEmitter) SendToolStart(tc toolCall) {
	e.toolMap[tc.ID] = true
	e.SendEvent(Event{
		Type:     "tool_start",
		ToolID:   tc.ID,
		ToolName: tc.Name,
	})
}
