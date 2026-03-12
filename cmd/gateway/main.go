package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gateway/internal/api"
	"gateway/internal/config"
	"gateway/internal/logstore"
	"gateway/internal/middleware"
	"gateway/internal/proxy"
	"gateway/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	// Load variables from .env if present; ignore errors so production can rely on real env.
	_ = godotenv.Load()

	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		slog.Error("database ping", "err", err)
		os.Exit(1)
	}

	st := store.New(pool)
	apiHandler := &api.Handler{Store: st, Log: slog.Default()}
	logStore := logstore.New(pool, slog.Default())
	proxyHandler := &proxy.Handler{
		Store:  st,
		Logs:   logStore,
		Log:    slog.Default(),
		Client: &http.Client{Timeout: 30 * time.Second},
	}

	addr := fmt.Sprintf(":%d", cfg.ServerPort)
	r := chi.NewRouter()
	r.Use(middleware.CORS(cfg.CORSAllowedOrigin))
	r.Use(middleware.RateLimitByPath("/api",
		middleware.NewRateLimiter(20, time.Hour, middleware.GetIP),
		middleware.NewRateLimiter(100, time.Hour, middleware.GetIP),
	))
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("gateway ok\n"))
	})
	apiHandler.Mount(r)
	proxyHandler.Mount(r)

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		slog.Info("server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
		os.Exit(1)
	}
	slog.Info("server stopped")
}
