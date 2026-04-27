package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/devlin-ai/devlin/internal/config"
	"github.com/devlin-ai/devlin/internal/llm"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/session"

	_ "github.com/devlin-ai/devlin/internal/tool"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

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

		sess, err := session.New(provider, store, "tui", modelName, func(evt session.Event) {
			conn.WriteJSON(outgoingEvent{
				Type:     evt.Type,
				Content:  evt.Content,
				ToolName: evt.ToolName,
				ToolID:   evt.ToolID,
				Display:  evt.Display,
			})
		})
		if err != nil {
			log.Error("failed to create session", "error", err)
			return
		}

		adapter := &wsAdapter{conn: conn, sessionID: sess.ID()}
		conn.WriteJSON(outgoingEvent{
			Type:      "session_created",
			SessionID: sess.ID(),
		})
		ch, err := adapter.Receive()
		if err != nil {
			log.Error("failed to start adapter receive", "error", err)
			return
		}

		for msg := range ch {
			switch msg.Type {
			case "cancel":
				log.Info("cancel requested")
				sess.Cancel()
			case "branch":
				branch, err := sess.Branch(msg.MessageID)
				if err != nil {
					log.Error("branch failed", "error", err)
					conn.WriteJSON(outgoingEvent{Type: "error", Content: err.Error()})
					continue
				}
				sess = branch
				adapter.SetSessionID(branch.ID())
				sess.SetOnEvent(func(evt session.Event) {
					conn.WriteJSON(outgoingEvent{
						Type:     evt.Type,
						Content:  evt.Content,
						ToolName: evt.ToolName,
						ToolID:   evt.ToolID,
						Display:  evt.Display,
					})
				})
				conn.WriteJSON(outgoingEvent{
					Type:      "branch_created",
					SessionID: branch.ID(),
					MessageID: msg.MessageID,
				})
			case "switch_session":
				switched, err := sess.SwitchTo(msg.SessionID)
				if err != nil {
					log.Error("switch session failed", "error", err)
					conn.WriteJSON(outgoingEvent{Type: "error", Content: err.Error()})
					continue
				}
				sess = switched
				adapter.SetSessionID(switched.ID())
				sess.SetOnEvent(func(evt session.Event) {
					conn.WriteJSON(outgoingEvent{
						Type:     evt.Type,
						Content:  evt.Content,
						ToolName: evt.ToolName,
						ToolID:   evt.ToolID,
						Display:  evt.Display,
					})
				})
				conn.WriteJSON(outgoingEvent{
					Type:      "session_switched",
					SessionID: switched.ID(),
				})
			case "list_branches":
				branches, err := sess.ListBranches()
				if err != nil {
					log.Error("list branches failed", "error", err)
					conn.WriteJSON(outgoingEvent{Type: "error", Content: err.Error()})
					continue
				}
				var items []branchListItem
				for _, b := range branches {
					items = append(items, branchListItem{
						SessionID:   b.SessionID,
						ParentMsgID: b.ParentMsgID,
					})
				}
				conn.WriteJSON(outgoingEvent{
					Type:     "branch_list",
					Branches: items,
				})
			case "list_sessions":
				conn.WriteJSON(outgoingEvent{
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
