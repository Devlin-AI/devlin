package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/devlin-ai/devlin/internal/channel"
	"github.com/devlin-ai/devlin/internal/config"
	"github.com/devlin-ai/devlin/internal/llm"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/session"

	_ "github.com/devlin-ai/devlin/internal/tool"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
)

type connState struct {
	conn     *websocket.Conn
	writeMu  sync.Mutex
	sess     *session.Session
	store    *session.Store
	provider llm.Provider
	model    string
	channel  string
}

func (cs *connState) send(msg channel.OutboundMessage) {
	cs.writeMu.Lock()
	defer cs.writeMu.Unlock()
	cs.conn.WriteJSON(msg)
}

func makeOnEvent(cs *connState) func(session.Event) {
	return func(evt session.Event) {
		cs.send(evt)
	}
}

func (cs *connState) handleNew(msg channel.InboundMessage) {
	sess, err := session.New(cs.provider, cs.store, msg.Channel, msg.Mode, cs.model, makeOnEvent(cs))
	if err != nil {
		logger.L().Error("failed to create session", "error", err)
		cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	cs.sess = sess
	cs.channel = msg.Channel
	cs.send(channel.OutboundMessage{
		Type:      "session_created",
		SessionID: sess.ID(),
		Mode:      sess.Mode(),
	})
}

func (cs *connState) handleContinue(msg channel.InboundMessage) {
	lastID, err := cs.store.GetLastSession(msg.Channel, msg.Mode)
	if err != nil {
		logger.L().Error("failed to get last session", "error", err)
		cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}

	if lastID == "" {
		cs.handleNew(msg)
		return
	}

	sess, err := session.Load(cs.provider, cs.store, lastID, cs.model, makeOnEvent(cs))
	if err != nil {
		logger.L().Error("failed to load session", "error", err)
		cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
		return
	}
	cs.sess = sess
	cs.channel = msg.Channel
	cs.send(channel.OutboundMessage{
		Type:      "session_continued",
		SessionID: sess.ID(),
		Mode:      sess.Mode(),
	})
}

func (cs *connState) requireSession() bool {
	return cs.sess != nil
}

func main() {
	logger.Init()

	log := logger.L()

	cfg, err := config.Load()
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	parts := strings.SplitN(cfg.LLM.Model, "/", 2)
	providerName := parts[0]
	modelName := parts[1]

	providerCfg, ok := cfg.LLM.Providers[providerName]
	if !ok {
		log.Error("provider not found", "provider", providerName)
		os.Exit(1)
	}

	provider, err := llm.NewProvider(providerName, providerCfg.APIKey, modelName, providerCfg.BaseURL)
	if err != nil {
		log.Error("failed to create provider", "provider", providerName, "error", err)
		os.Exit(1)
	}

	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".devlin", "devlin.db")
	store, err := session.NewStore(dbPath)
	if err != nil {
		log.Error("failed to open store", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Error("websocket upgrade failed", "error", err)
			return
		}
		defer conn.Close()

		cs := &connState{
			conn:     conn,
			store:    store,
			provider: provider,
			model:    modelName,
		}

		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var msg channel.InboundMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}

			switch msg.Type {
			case "new":
				cs.handleNew(msg)
			case "continue":
				cs.handleContinue(msg)
			case "cancel":
				if !cs.requireSession() {
					continue
				}
				log.Info("cancel requested")
				cs.sess.Cancel()
			case "branch":
				if !cs.requireSession() {
					continue
				}
				branch, err := cs.sess.Branch(msg.MessageID)
				if err != nil {
					log.Error("branch failed", "error", err)
					cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
					continue
				}
				cs.sess = branch
				cs.sess.SetOnEvent(makeOnEvent(cs))
				cs.send(channel.OutboundMessage{
					Type:      "branch_created",
					SessionID: branch.ID(),
					MessageID: msg.MessageID,
				})
			case "switch_session":
				if !cs.requireSession() {
					continue
				}
				switched, err := cs.sess.SwitchTo(msg.SessionID)
				if err != nil {
					log.Error("switch session failed", "error", err)
					cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
					continue
				}
				cs.sess = switched
				cs.sess.SetOnEvent(makeOnEvent(cs))
				cs.send(channel.OutboundMessage{
					Type:      "session_switched",
					SessionID: switched.ID(),
				})
			case "list_branches":
				if !cs.requireSession() {
					continue
				}
				branchMetas, err := cs.sess.ListBranches()
				if err != nil {
					log.Error("list branches failed", "error", err)
					cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
					continue
				}
				infos := make([]channel.BranchInfo, len(branchMetas))
				for i, b := range branchMetas {
					infos[i] = channel.BranchInfo{
						SessionID:   b.SessionID,
						ParentMsgID: b.ParentMsgID,
					}
				}
				var parent *channel.BranchInfo
				parentMeta, err := cs.sess.GetParentBranch()
				if err != nil {
					log.Error("get parent branch failed", "error", err)
				} else if parentMeta != nil {
					parent = &channel.BranchInfo{
						SessionID:   parentMeta.ParentID,
						ParentMsgID: parentMeta.ParentMsgID,
					}
				}
				cs.send(channel.OutboundMessage{
					Type:     "branch_list",
					Parent:   parent,
					Branches: infos,
				})
			case "list_sessions":
				ch := cs.channel
				if ch == "" {
					ch = msg.Channel
				}
				if ch == "" {
					cs.send(channel.OutboundMessage{Type: "session_list"})
					continue
				}
				sessionMetas, err := store.ListSessions(ch)
				if err != nil {
					log.Error("list sessions failed", "error", err)
					cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
					continue
				}
				infos := make([]channel.SessionInfo, len(sessionMetas))
				for i, sm := range sessionMetas {
					infos[i] = channel.SessionInfo{
						ID:        sm.ID,
						Channel:   sm.Channel,
						Mode:      sm.Mode,
						CreatedAt: sm.CreatedAt,
						UpdatedAt: sm.UpdatedAt,
					}
				}
				cs.send(channel.OutboundMessage{
					Type:     "session_list",
					Sessions: infos,
				})
			default:
				if !cs.requireSession() {
					continue
				}
				go cs.sess.ProcessMessage(msg.Content)
			}
		}
	})

	addr := fmt.Sprintf(":%d", cfg.Gateway.Port)
	log.Info("gateway starting", "addr", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Error("server exited", "error", err)
		os.Exit(1)
	}
}
