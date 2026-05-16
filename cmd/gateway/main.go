package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
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

type gateway struct {
	store    *store.Store
	provider llm.Provider
	model    string
	sessions sync.Map
}

func (g *gateway) resolve(sessionID string) (*agent.Session, error) {
	if v, ok := g.sessions.Load(sessionID); ok {
		return v.(*agent.Session), nil
	}
	sess, err := agent.Load(g.provider, g.store, sessionID, g.model)
	if err != nil {
		return nil, err
	}
	g.sessions.Store(sessionID, sess)
	return sess, nil
}

func setupGateway() (*gateway, int, error) {
	logger.Init()
	log := logger.Default()

	cfg, err := config.Load()
	if err != nil {
		log.Error("failed to load config", "error", err)
		return nil, 0, err
	}

	providerName, modelName := cfg.LLM.ModelParts()
	providerCfg := cfg.LLM.Providers[providerName]

	provider, err := llm.NewProvider(providerName, providerCfg.APIKey, modelName, providerCfg.BaseURL)
	if err != nil {
		log.Error("failed to create provider", "provider", providerName, "error", err)
		return nil, 0, err
	}

	db, err := store.NewStore(cfg.Database.ResolvePath())
	if err != nil {
		log.Error("failed to open store", "error", err)
		return nil, 0, err
	}

	llm.SetDefaultStallTimeout(cfg.LLM.StallTimeoutDuration())
	process.SetDefaultBackgroundTimeout(cfg.Session.BackgroundTimeoutDuration())
	agent.SetDefaultMaxDepth(cfg.Session.MaxDepth)

	gw := &gateway{
		store:    db,
		provider: provider,
		model:    modelName,
	}

	return gw, cfg.Gateway.Port, nil
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
	gw, port, err := setupGateway()
	if err != nil {
		os.Exit(1)
	}
	defer gw.store.Close()

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
			conn: conn,
			gw:   gw,
		}
		cs.handleConnection()
	})

	runServer(r, port)
}
