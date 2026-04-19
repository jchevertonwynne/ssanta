package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jchevertonwynne/ssanta/internal/config"
	"github.com/jchevertonwynne/ssanta/internal/db"
	"github.com/jchevertonwynne/ssanta/internal/server"
	"github.com/jchevertonwynne/ssanta/internal/service"
	"github.com/jchevertonwynne/ssanta/internal/session"
	"github.com/jchevertonwynne/ssanta/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(cfg.DatabaseURL, cfg.MigrationsDir); err != nil {
		return err
	}

	sessions := session.NewManager(cfg.SessionSecret, cfg.SecureCookies)
	st := store.New(pool)
	svc := service.New(st)
	svc.SetInviteMaxAge(cfg.InviteMaxAge)
	startJanitor(ctx, svc, cfg)

	handler, closeHub := server.New(svc, sessions)
	defer closeHub()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http server shutdown", "err", err)
	}

	return nil
}

func startJanitor(ctx context.Context, svc *service.Service, cfg config.Config) {
	if cfg.JanitorInterval <= 0 {
		return
	}

	ticker := time.NewTicker(cfg.JanitorInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				now := time.Now()
				deletedInvites, clearedChallenges, err := svc.Cleanup(cleanupCtx, now)
				cancel()
				if err != nil {
					slog.Error("janitor cleanup failed", "err", err)
					continue
				}
				if deletedInvites > 0 || clearedChallenges > 0 {
					slog.Info("janitor cleanup", "deleted_invites", deletedInvites, "cleared_room_pgp_challenges", clearedChallenges)
				}
			}
		}
	}()
}
