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
			case "session_state":
				if !cs.requireSession() {
					continue
				}
				targetID := msg.SessionID
				if targetID == "" {
					targetID = cs.sess.ID()
				}
				msgs, err := store.LoadFullHistory(targetID)
				if err != nil {
					log.Error("load history failed", "error", err)
					cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
					continue
				}

				toolCallArgs := make(map[string]string)
				for _, m := range msgs {
					if m.Role == "assistant" {
						for _, tc := range m.ToolCalls {
							toolCallArgs[tc.ID] = tc.Function.Arguments
						}
					}
				}

				histMsgs := make([]channel.HistoryMessage, 0, len(msgs))
				for _, m := range msgs {
					var toolCallsJSON string
					if len(m.ToolCalls) > 0 {
						if b, err := json.Marshal(m.ToolCalls); err == nil {
							toolCallsJSON = string(b)
						}
					}
					hm := channel.HistoryMessage{
						ID:        m.ID,
						Role:      string(m.Role),
						Content:   m.Content,
						ToolName:  m.ToolName,
						ToolCalls: toolCallsJSON,
					}
					if m.Role == "tool" {
						hm.ToolArgs = toolCallArgs[m.ToolCallID]
					}
					histMsgs = append(histMsgs, hm)
				}

				chain, err := store.LoadBranchChain(targetID)
				if err != nil {
					log.Error("load branch chain failed", "error", err)
					cs.send(channel.OutboundMessage{Type: "error", Content: err.Error()})
					continue
				}
				points := make([]channel.BranchPoint, 0, len(chain))
				for _, c := range chain {
					points = append(points, channel.BranchPoint{
						MsgID:     c.ParentMsgID,
						SessionID: c.SessionID,
					})
				}

				var parent *channel.BranchInfo
				var siblings []channel.BranchInfo
				var siblingIdx int
				currentMeta, err := store.LoadBranchMeta(targetID)
				if err != nil {
					log.Error("load branch meta failed", "session_id", targetID, "error", err)
				}
				if currentMeta != nil && currentMeta.ParentID != "" {
					parent = &channel.BranchInfo{
						SessionID:   currentMeta.ParentID,
						ParentMsgID: currentMeta.ParentMsgID,
					}
					parentChildren, err := store.ListBranches(currentMeta.ParentID)
					if err != nil {
						log.Error("list parent branches failed", "parent_id", currentMeta.ParentID, "error", err)
					}
					for i, pc := range parentChildren {
						firstMsg, err := store.GetFirstUserMessage(pc.SessionID)
						if err != nil {
							log.Error("get first user message failed", "session_id", pc.SessionID, "error", err)
						}
						siblings = append(siblings, channel.BranchInfo{
							SessionID:    pc.SessionID,
							ParentMsgID:  pc.ParentMsgID,
							FirstMessage: firstMsg,
						})
						if pc.SessionID == targetID {
							siblingIdx = i
						}
					}
				}

				childMetas, err := store.ListBranches(targetID)
				if err != nil {
					log.Error("list child branches failed", "session_id", targetID, "error", err)
				}
				children := make([]channel.BranchInfo, len(childMetas))
				for i, b := range childMetas {
					firstMsg, err := store.GetFirstUserMessage(b.SessionID)
					if err != nil {
						log.Error("get first user message failed", "session_id", b.SessionID, "error", err)
					}
					children[i] = channel.BranchInfo{
						SessionID:    b.SessionID,
						ParentMsgID:  b.ParentMsgID,
						FirstMessage: firstMsg,
					}
				}

				cs.send(channel.OutboundMessage{
					Type:         "session_state",
					SessionID:    targetID,
					Messages:     histMsgs,
					BranchPoints: points,
					Parent:       parent,
					Branches:     children,
					Siblings:     siblings,
					SiblingIdx:   siblingIdx,
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
