package session

import (
	"time"

	"github.com/devlin-ai/devlin/internal/store"
)

type SessionMeta struct {
	ID        string
	Channel   string
	Mode      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func FromStoreMeta(s store.SessionMeta) SessionMeta {
	return SessionMeta{
		ID:        s.ID,
		Channel:   s.Channel,
		Mode:      s.Mode,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}
}
