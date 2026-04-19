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
	"github.com/jchevertonwynne/ssanta/internal/observability"
	"github.com/jchevertonwynne/ssanta/internal/server"
	"github.com/jchevertonwynne/ssanta/internal/service"
	"github.com/jchevertonwynne/ssanta/internal/session"
	"github.com/jchevertonwynne/ssanta/internal/store"
)

func main() {
	// Setup initial text logger for bootstrapping
	textLogger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(textLogger)

	if err := run(); err != nil {
		textLogger.Error("fatal", "err", err)
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

	// Initialize observability (traces, metrics, logs)
	otelResult, err := observability.Init(ctx, observability.Config{
		OTLPEndpoint: cfg.OTLPEndpoint,
		OTLPInsecure: cfg.OTLPInsecure,
		ServiceName:  cfg.ServiceName,
		Environment:  cfg.Environment,
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelResult.Shutdown(shutdownCtx); err != nil {
			slog.Error("observability shutdown", "err", err)
		}
	}()

	// Initialize metrics
	metrics, err := observability.InitMetrics(ctx, cfg.ServiceName)
	if err != nil {
		return err
	}
	observability.SetGlobalMetrics(metrics)

	// Replace logger with OTLP-enabled handler plus JSON to stdout
	otelHandler := observability.NewSlogHandler(cfg.ServiceName)
	jsonHandler := slog.NewJSONHandler(os.Stdout, nil)
	multiHandler := &multiHandler{handlers: []slog.Handler{otelHandler, jsonHandler}}
	logger := slog.New(multiHandler)
	slog.SetDefault(logger)

	runtimeURL := cfg.DatabaseURL
	if cfg.DatabaseSchema != "" {
		runtimeURL, err = db.WithSearchPath(runtimeURL, cfg.DatabaseSchema)
		if err != nil {
			return err
		}
	}

	pool, err := db.Connect(ctx, runtimeURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	sessions := session.NewManager(cfg.SessionSecret, cfg.SecureCookies, cfg.SessionTTL)
	st := store.New(pool)
	svc := service.New(st)
	svc.SetInviteMaxAge(cfg.InviteMaxAge)
	startJanitor(ctx, svc, cfg)

	handler, closeHub := server.New(svc, sessions, cfg.ServiceName, otelResult.MetricsHandler)
	defer closeHub()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
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

// multiHandler fans out slog records to multiple handlers
type multiHandler struct {
	handlers []slog.Handler
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, h := range m.handlers {
		if err := h.Handle(ctx, record.Clone()); err != nil {
			return err
		}
	}
	return nil
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: handlers}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: handlers}
}
