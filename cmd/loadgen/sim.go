package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type userStats struct {
	username string
	sent     atomic.Int64
	errors   atomic.Int64
}

type wsMessage struct {
	Type        string `json:"type"`
	Message     string `json:"message"`
	ClientMsgID string `json:"client_message_id"`
}

func simulate(ctx context.Context, client *userClient, roomID int64, cfg config, stats *userStats) {
	wsURL := toWSURL(client.baseURL) + fmt.Sprintf("/rooms/%d/ws", roomID)

	httpURL, _ := url.Parse(client.baseURL)
	var cookieParts []string
	for _, c := range client.http.Jar.Cookies(httpURL) {
		cookieParts = append(cookieParts, c.Name+"="+c.Value)
	}
	header := http.Header{}
	if len(cookieParts) > 0 {
		header.Set("Cookie", strings.Join(cookieParts, "; "))
	}

	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		_, _ = fmt.Fprintf(os.Stdout, "[%s] websocket dial error: %v\n", client.username, err)
		stats.errors.Add(1)
		return
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		drainReads(conn)
	})

	msgCount := 0
	rng := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec

loop:
	for {
		if !sendBurst(ctx, conn, client, cfg, rng, &msgCount, stats) {
			break loop
		}

		select {
		case <-ctx.Done():
			break loop
		case <-time.After(randDuration(rng, cfg.pauseMin, cfg.pauseMax)):
		}
	}

	_ = conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	_ = conn.Close()
	wg.Wait()
}

func sendBurst(ctx context.Context, conn *websocket.Conn, client *userClient, cfg config, rng *rand.Rand, msgCount *int, stats *userStats) bool {
	burstSize := randInt(rng, cfg.burstMin, cfg.burstMax)
	for i := range burstSize {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		*msgCount++
		msg := wsMessage{
			Type:        "message",
			Message:     fmt.Sprintf("hello from %s (msg %d)", client.username, *msgCount),
			ClientMsgID: fmt.Sprintf("%x-%x-%d", rng.Int63(), rng.Int63(), i),
		}
		data, err := json.Marshal(msg)
		if err != nil {
			stats.errors.Add(1)
			return false
		}
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			stats.errors.Add(1)
			return false
		}
		stats.sent.Add(1)

		select {
		case <-ctx.Done():
			return false
		case <-time.After(randDuration(rng, cfg.msgDelayMin, cfg.msgDelayMax)):
		}
	}
	return true
}

func drainReads(conn *websocket.Conn) {
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func toWSURL(baseURL string) string {
	s := strings.Replace(baseURL, "https://", "wss://", 1)
	return strings.Replace(s, "http://", "ws://", 1)
}

func randInt(rng *rand.Rand, lo, hi int) int {
	if lo >= hi {
		return lo
	}
	return lo + rng.Intn(hi-lo+1)
}

func randDuration(rng *rand.Rand, lo, hi time.Duration) time.Duration {
	if lo >= hi {
		return lo
	}
	return lo + time.Duration(rng.Int63n(int64(hi-lo+1)))
}
