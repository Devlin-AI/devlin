package main

import (
	"encoding/json"
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

		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				log.Error("websocket read failed", "error", err)
				return
			}

			var msg incomingMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				log.Warn("failed to unmarshal message", "error", err)
				continue
			}

			if msg.Type == "cancel" {
				log.Info("cancel requested")
				sess.Cancel()
				continue
			}

			go sess.ProcessMessage(msg.Content)
		}
	})

	addr := fmt.Sprintf(":%d", cfg.Gateway.Port)
	log.Info("gateway starting", "addr", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Error("server exited", "error", err)
		os.Exit(1)
	}
}
