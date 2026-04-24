 Group F — Correctness polish

 F1. GetOrCreateDMRoom race (#9)

 internal/service/service.go:650 — currently GetRoomByDisplayName → CreateRoom can race. Replace with an atomic store method GetOrCreateDMRoom(ctx, displayName, creatorID)
 (RoomID, error) on RoomStore that does INSERT ... ON CONFLICT (display_name) DO NOTHING RETURNING id and on empty-return falls back to a SELECT id FROM rooms WHERE display_name
 = $1. Keep the subsequent JoinRoom calls for both participants.

 F2. Typing-session goroutines in wg (#10)

 internal/server/websocket.go:543 SetTypingStatus — the go func() spawned for auto-timeout is not tracked. Add h.wg.Add(1) before go func() and defer h.wg.Done() inside. Stop()
 already waits on wg with a 5s timeout — this makes the typing sweep bounded.

 F3. CSRF token binding to session (#13)

 Bind the CSRF token to the session cookie in addition to csrf_id. internal/server/csrf.go:92 computeCSRFToken — include the current userID (or session cookie value when present)
  in the HMAC input: HMAC(secret, csrf_id + "|" + userID). When no session, fall back to csrf_id only so anon GET-before-login still works.
 - On login/logout the client reloads from /content which pulls a fresh nonce+token anyway. Nothing else in the flow breaks.

 F4. Drop unused RequireAuth / WithOptionalAuth (#14)

 Delete both functions from internal/server/middleware.go:59-88. They're unreferenced and misleading (implying routes are middleware-protected when they aren't). Update
 interfaces.go docs if any mention them.

 F5. Dockerfile Go version alignment (#15)

 Dockerfile:2 — change golang:1.26-alpine → golang:1.25-alpine to match go.mod:3 (go 1.25.5). Avoids reproducibility drift.

 Files touched: internal/service/service.go, internal/store/room_store.go, internal/server/websocket.go, internal/server/csrf.go, internal/server/middleware.go, Dockerfile,
 relevant tests (service_integration_test.go, csrf_test.go).

 Verify: concurrent DM-create test hitting GetOrCreateDMRoom from N goroutines — all return the same RoomID, no duplicate rows. CSRF test confirms token issued pre-login is
 invalid post-login. docker build . succeeds.

 ---
 PR slicing

 1. PR 1 — WS lifecycle (Group A): biggest surface change, gates everything else.
 2. PR 2 — Rate limiter (Group B): small, self-contained.
 3. PR 3 — Session rotation (Group C): migration + cross-cutting; all users re-login once.
 4. PR 4 — Search hardening (Group D).
 5. PR 5 — Headers/CSP/metrics (Group E).
 6. PR 6 — Correctness polish (Group F).