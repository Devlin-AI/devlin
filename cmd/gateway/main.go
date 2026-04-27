package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

func send(conn *websocket.Conn, msg channel.OutboundMessage) {
	conn.WriteJSON(msg)
}

func makeOnEvent(conn *websocket.Conn) func(session.Event) {
	return func(evt session.Event) {
		send(conn, evt)
	}
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

	provider, err := llm.NewProvider(providerName, providerCfg.APIKey, modelName)
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

		sess, err := session.New(provider, store, "tui", modelName, makeOnEvent(conn))
		if err != nil {
			log.Error("failed to create session", "error", err)
			return
		}

		send(conn, channel.OutboundMessage{
			Type:      "session_created",
			SessionID: sess.ID(),
		})

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
			case "cancel":
				log.Info("cancel requested")
				sess.Cancel()
			case "branch":
				branch, err := sess.Branch(msg.MessageID)
				if err != nil {
					log.Error("branch failed", "error", err)
					send(conn, channel.OutboundMessage{Type: "error", Content: err.Error()})
					continue
				}
				sess = branch
				sess.SetOnEvent(makeOnEvent(conn))
				send(conn, channel.OutboundMessage{
					Type:      "branch_created",
					SessionID: branch.ID(),
					MessageID: msg.MessageID,
				})
			case "switch_session":
				switched, err := sess.SwitchTo(msg.SessionID)
				if err != nil {
					log.Error("switch session failed", "error", err)
					send(conn, channel.OutboundMessage{Type: "error", Content: err.Error()})
					continue
				}
				sess = switched
				sess.SetOnEvent(makeOnEvent(conn))
				send(conn, channel.OutboundMessage{
					Type:      "session_switched",
					SessionID: switched.ID(),
				})
			case "list_branches":
				branchMetas, err := sess.ListBranches()
				if err != nil {
					log.Error("list branches failed", "error", err)
					send(conn, channel.OutboundMessage{Type: "error", Content: err.Error()})
					continue
				}
				infos := make([]channel.BranchInfo, len(branchMetas))
				for i, b := range branchMetas {
					infos[i] = channel.BranchInfo{
						SessionID:   b.SessionID,
						ParentMsgID: b.ParentMsgID,
					}
				}
				send(conn, channel.OutboundMessage{
					Type:     "branch_list",
					Branches: infos,
				})
			case "list_sessions":
				send(conn, channel.OutboundMessage{
					Type: "session_list",
				})
			default:
				go sess.ProcessMessage(msg.Content)
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
