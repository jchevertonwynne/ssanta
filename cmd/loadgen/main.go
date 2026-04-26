package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type config struct {
	baseURL     string
	numUsers    int
	burstMin    int
	burstMax    int
	msgDelayMin time.Duration
	msgDelayMax time.Duration
	pauseMin    time.Duration
	pauseMax    time.Duration
}

var (
	errUsersMin      = errors.New("-users must be >= 1")
	errBurstMin      = errors.New("-burst-min must be >= 1")
	errBurstMinMax   = errors.New("-burst-min must be <= -burst-max")
	errMsgDelayRange = errors.New("-msg-delay-min must be <= -msg-delay-max")
	errPauseRange    = errors.New("-pause-min must be <= -pause-max")
)

func main() {
	var cfg config
	flag.StringVar(&cfg.baseURL, "url", "http://localhost:8080", "base server URL")
	flag.IntVar(&cfg.numUsers, "users", 10, "number of simulated users")
	flag.IntVar(&cfg.burstMin, "burst-min", 1, "minimum messages per burst")
	flag.IntVar(&cfg.burstMax, "burst-max", 5, "maximum messages per burst")
	flag.DurationVar(&cfg.msgDelayMin, "msg-delay-min", 100*time.Millisecond, "minimum delay between messages in a burst")
	flag.DurationVar(&cfg.msgDelayMax, "msg-delay-max", 500*time.Millisecond, "maximum delay between messages in a burst")
	flag.DurationVar(&cfg.pauseMin, "pause-min", 1*time.Second, "minimum pause between bursts")
	flag.DurationVar(&cfg.pauseMax, "pause-max", 10*time.Second, "maximum pause between bursts")
	flag.Parse()

	if err := validateConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "invalid config: %v\n", err)
		os.Exit(1)
	}

	var clients []*userClient
	var roomID int64
	var setupErr error
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	_, _ = fmt.Fprintf(os.Stdout, "setting up %d users...\n", cfg.numUsers)
	clients, roomID, setupErr = setup(ctx, cfg)
	if setupErr != nil {
		// Attempt cleanup of any clients that were created before setup failed
		if len(clients) > 0 {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cleanup(cleanupCtx, clients)
		}
		fmt.Fprintf(os.Stderr, "setup failed: %v\n", setupErr)
		os.Exit(1)
	}

	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cleanup(cleanupCtx, clients)
	}()

	_, _ = fmt.Fprintf(os.Stdout, "setup complete, room ID: %d — starting simulation\n", roomID)

	stats := make([]*userStats, cfg.numUsers)
	for i, c := range clients {
		stats[i] = &userStats{username: c.username}
	}

	var wg sync.WaitGroup
	for i, c := range clients {
		s := stats[i]
		wg.Go(func() {
			simulate(ctx, c, roomID, cfg, s)
		})
	}

	go printStatsPeriodically(ctx, stats, 10*time.Second)

	<-ctx.Done()
	_, _ = fmt.Fprintf(os.Stdout, "\nshutting down...\n")
	wg.Wait()

	printStats(stats)
}

func validateConfig(cfg config) error {
	if cfg.numUsers < 1 {
		return errUsersMin
	}
	if cfg.burstMin < 1 {
		return errBurstMin
	}
	if cfg.burstMin > cfg.burstMax {
		return errBurstMinMax
	}
	if cfg.msgDelayMin > cfg.msgDelayMax {
		return errMsgDelayRange
	}
	if cfg.pauseMin > cfg.pauseMax {
		return errPauseRange
	}
	return nil
}

func setup(ctx context.Context, cfg config) ([]*userClient, int64, error) {
	clients := make([]*userClient, cfg.numUsers)
	rng := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec
	for i := range cfg.numUsers {
		name := fmt.Sprintf("loadgen_%06x", rng.Int31n(1<<24))
		c, err := newUserClient(cfg.baseURL, name, "loadgen-pass-"+name)
		if err != nil {
			return nil, 0, fmt.Errorf("create client %d: %w", i, err)
		}
		clients[i] = c
	}

	if err := registerClients(ctx, clients); err != nil {
		return clients, 0, err
	}

	roomName := fmt.Sprintf("loadgen_%06x", rng.Int31n(1<<24))
	roomID, err := clients[0].createRoom(ctx, roomName)
	if err != nil {
		return clients, 0, fmt.Errorf("create room: %w", err)
	}

	for _, c := range clients[1:] {
		if err := clients[0].inviteUser(ctx, roomID, c.username); err != nil {
			return clients, roomID, fmt.Errorf("invite %s: %w", c.username, err)
		}
	}

	if cfg.numUsers > 1 {
		if err := acceptAllInvites(ctx, clients[1:]); err != nil {
			return clients, roomID, err
		}
	}

	return clients, roomID, nil
}

func registerClients(ctx context.Context, clients []*userClient) error {
	errs := make([]error, len(clients))
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Go(func() {
			if err := c.seedCSRF(ctx); err != nil {
				errs[i] = fmt.Errorf("seed csrf [%s]: %w", c.username, err)
				return
			}
			if err := c.register(ctx); err != nil {
				errs[i] = fmt.Errorf("register [%s]: %w", c.username, err)
				return
			}
			// Re-seed: token changes once the session cookie is present.
			if err := c.seedCSRF(ctx); err != nil {
				errs[i] = fmt.Errorf("re-seed csrf [%s]: %w", c.username, err)
			}
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func acceptAllInvites(ctx context.Context, clients []*userClient) error {
	errs := make([]error, len(clients))
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Go(func() {
			inviteID, err := c.getInviteID(ctx)
			if err != nil {
				errs[i] = fmt.Errorf("get invite id [%s]: %w", c.username, err)
				return
			}
			if err := c.acceptInvite(ctx, inviteID); err != nil {
				errs[i] = fmt.Errorf("accept invite [%s]: %w", c.username, err)
			}
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func cleanup(ctx context.Context, clients []*userClient) {
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Go(func() {
			// Re-seed CSRF as the session may have been invalidated by the WS close.
			_ = c.seedCSRF(ctx)
			if err := c.deleteUser(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "[%s] cleanup error: %v\n", c.username, err)
			}
		})
	}
	wg.Wait()
}

func printStatsPeriodically(ctx context.Context, stats []*userStats, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			printStats(stats)
		}
	}
}

func printStats(stats []*userStats) {
	_, _ = fmt.Fprintf(os.Stdout, "--- stats ---\n")
	total := int64(0)
	for _, s := range stats {
		sent := s.sent.Load()
		errs := s.errors.Load()
		total += sent
		_, _ = fmt.Fprintf(os.Stdout, "  %-24s  sent=%-6d errors=%d\n", s.username, sent, errs)
	}
	_, _ = fmt.Fprintf(os.Stdout, "  total sent: %d\n", total)
}
