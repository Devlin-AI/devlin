package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/devlin-ai/devlin/internal/config"
	"github.com/devlin-ai/devlin/internal/llm"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/message"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type incomingMessage struct {
	Content string `json:"content"`
}

type outgoingEvent struct {
	Type    string `json:"type"`
	Content string `json:"content"`
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

		var history []message.Message
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

			history = append(history, message.Message{
				Role:      message.RoleUser,
				Content:   msg.Content,
				Timestamp: time.Now(),
			})

			ch, err := provider.Stream(context.Background(), history)
			if err != nil {
				log.Error("stream failed", "error", err)
				conn.WriteJSON(outgoingEvent{
					Type:    "error",
					Content: err.Error(),
				})
				continue
			}

			var assistantText string
			for evt := range ch {
				switch evt.Type {
				case message.StreamEventThinking:
					conn.WriteJSON(outgoingEvent{
						Type:    "thinking",
						Content: evt.Token,
					})
				case message.StreamEventToken:
					assistantText += evt.Token
					conn.WriteJSON(outgoingEvent{
						Type:    "token",
						Content: evt.Token,
					})
				case message.StreamEventDone:
					conn.WriteJSON(outgoingEvent{
						Type: "done",
					})
				case message.StreamEventError:
					log.Error("stream event error", "error", evt.Error)
					conn.WriteJSON(outgoingEvent{
						Type:    "error",
						Content: evt.Error,
					})
				}

			}

			history = append(history, message.Message{
				Role:      message.RoleAssistant,
				Content:   assistantText,
				Timestamp: time.Now(),
			})

		}
	})

	addr := fmt.Sprintf(":%d", cfg.Gateway.Port)
	log.Info("gateway starting", "addr", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Error("server exited", "error", err)
		os.Exit(1)
	}
}
