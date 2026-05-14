package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/devlin-ai/devlin/internal/agent"
	"github.com/devlin-ai/devlin/internal/config"
	"github.com/devlin-ai/devlin/internal/llm"
	"github.com/devlin-ai/devlin/internal/logger"
	"github.com/devlin-ai/devlin/internal/process"
	"github.com/devlin-ai/devlin/internal/store"

	_ "github.com/devlin-ai/devlin/internal/tool"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func setupGateway() (llm.Provider, *store.Store, string, int, error) {
	logger.Init()
	log := logger.Default()

	cfg, err := config.Load()
	if err != nil {
		log.Error("failed to load config", "error", err)
		return nil, nil, "", 0, err
	}

	parts := strings.SplitN(cfg.LLM.Model, "/", 2)
	providerName := parts[0]
	modelName := parts[1]

	providerCfg, ok := cfg.LLM.Providers[providerName]
	if !ok {
		log.Error("provider not found", "provider", providerName)
		return nil, nil, "", 0, fmt.Errorf("provider %q not found", providerName)
	}

	provider, err := llm.NewProvider(providerName, providerCfg.APIKey, modelName, providerCfg.BaseURL)
	if err != nil {
		log.Error("failed to create provider", "provider", providerName, "error", err)
		return nil, nil, "", 0, err
	}

	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".devlin", "devlin.db")
	store, err := store.NewStore(dbPath)
	if err != nil {
		log.Error("failed to open store", "error", err)
		return nil, nil, "", 0, err
	}

	llm.SetDefaultStallTimeout(cfg.LLM.StallTimeoutDuration())
	process.SetDefaultBackgroundTimeout(cfg.Session.BackgroundTimeoutDuration())
	agent.SetDefaultMaxDepth(cfg.Session.MaxDepth)

	return provider, store, modelName, cfg.Gateway.Port, nil
}

func runServer(r *chi.Mux, port int) {
	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{Addr: addr, Handler: r}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.Default().Info("shutting down gateway")
		process.KillAll()
		srv.Close()
	}()

	logger.Default().Info("gateway starting", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Default().Error("server exited", "error", err)
		os.Exit(1)
	}
}

func main() {
	provider, store, modelName, port, err := setupGateway()
	if err != nil {
		os.Exit(1)
	}
	defer store.Close()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Default().Error("websocket upgrade failed", "error", err)
			return
		}
		defer conn.Close()

		cs := &connState{
			conn:     conn,
			store:    store,
			provider: provider,
			model:    modelName,
		}
		defer func() {
			if cs.sess != nil {
				cs.sess.Cancel()
			}
		}()

		cs.handleConnection()
	})

	runServer(r, port)
}
