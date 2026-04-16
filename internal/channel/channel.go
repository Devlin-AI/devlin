package channel

import "time"

type InboundMessage struct {
	Content  string `json:"content"`
	Timstamp time.Time
}

type OutboundMessage struct {
	Content string
}

type Adapter interface {
	Name() string
	Receive() (<-chan InboundMessage, error)
	Send(OutboundMessage) error
}
