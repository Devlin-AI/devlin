package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/devlin-ai/devlin/internal/config"
	"github.com/devlin-ai/devlin/internal/llm"
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
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	parts := strings.SplitN(cfg.LLM.Model, "/", 2)
	providerName := parts[0]
	modelName := parts[1]

	providerCfg, ok := cfg.LLM.Providers[providerName]
	if !ok {
		log.Fatalf("provider not found: %s", providerName)
	}

	provider, err := llm.NewProvider(providerName, providerCfg.APIKey, modelName)
	if err != nil {
		log.Fatalf("provider: %v", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println(err)
			return
		}
		defer conn.Close()

		var history []message.Message
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				log.Println(err)
				return
			}

			var msg incomingMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				log.Println(err)
				continue
			}

			history = append(history, message.Message{
				Role:      message.RoleUser,
				Content:   msg.Content,
				Timestamp: time.Now(),
			})

			ch, err := provider.Stream(context.Background(), history)
			if err != nil {
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

	http.ListenAndServe(fmt.Sprintf(":%d", cfg.Gateway.Port), r)
}
