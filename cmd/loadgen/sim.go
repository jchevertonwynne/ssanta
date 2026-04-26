package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
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

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		fmt.Printf("[%s] websocket dial error: %v\n", client.username, err)
		stats.errors.Add(1)
		return
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		drainReads(conn)
	})

	msgCount := 0
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

loop:
	for {
		burstSize := randInt(rng, cfg.burstMin, cfg.burstMax)
		for i := range burstSize {
			select {
			case <-ctx.Done():
				break loop
			default:
			}

			msgCount++
			msg := wsMessage{
				Type:        "message",
				Message:     fmt.Sprintf("hello from %s (msg %d)", client.username, msgCount),
				ClientMsgID: fmt.Sprintf("%x-%x-%d", rng.Int63(), rng.Int63(), i),
			}
			data, _ := json.Marshal(msg)
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				stats.errors.Add(1)
				break loop
			}
			stats.sent.Add(1)

			select {
			case <-ctx.Done():
				break loop
			case <-time.After(randDuration(rng, cfg.msgDelayMin, cfg.msgDelayMax)):
			}
		}

		select {
		case <-ctx.Done():
			break loop
		case <-time.After(randDuration(rng, cfg.pauseMin, cfg.pauseMax)):
		}
	}

	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	conn.Close()
	wg.Wait()
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

func randInt(rng *rand.Rand, min, max int) int {
	if min >= max {
		return min
	}
	return min + rng.Intn(max-min+1)
}

func randDuration(rng *rand.Rand, min, max time.Duration) time.Duration {
	if min >= max {
		return min
	}
	return min + time.Duration(rng.Int63n(int64(max-min+1)))
}
