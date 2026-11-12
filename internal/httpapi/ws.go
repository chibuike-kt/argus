package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/chibuike-kt/argus/internal/clock"
	"github.com/chibuike-kt/argus/internal/config"
	"github.com/chibuike-kt/argus/internal/hub"
)

// wsHandler serves GET /ws: the live candle stream
// (docs/briefs/adr-004-ws-contract-draft.md v2). Not part of the
// OpenAPI-generated REST surface -- mounted directly in NewRouter,
// outside /api/v1 -- but authenticated the same way: the same session
// cookie requireSession reads, resolved through the same
// sessionResolver/userLookup interfaces, never a new auth mechanism.
//
// A WebSocket upgrade cannot rely on the cookie alone the way a REST
// request can: any page on any origin can open a cross-origin
// WebSocket carrying the browser's cookies, so this handler also
// checks Origin itself, before the handshake completes (review item
// 1) -- reusing the exact same allow-list csrfProtection already
// enforces for state-changing REST requests (config.AuthPolicy.AllowedOrigins).
type wsHandler struct {
	hub            *hub.Hub
	sessions       sessionResolver
	users          userLookup
	cookieName     string
	allowedOrigins map[string]bool
	trustProxy     bool
	live           config.LiveFeed
	clk            clock.Clock
	logger         *slog.Logger
}

func newWSHandler(h *hub.Hub, sessions sessionResolver, users userLookup, cookieName string, allowedOrigins []string, trustProxy bool, live config.LiveFeed, clk clock.Clock, logger *slog.Logger) *wsHandler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}
	return &wsHandler{
		hub: h, sessions: sessions, users: users, cookieName: cookieName,
		allowedOrigins: allowed, trustProxy: trustProxy, live: live, clk: clk, logger: logger,
	}
}

// wsAuth is what ServeHTTP resolves before the handshake -- carried
// through to the heartbeat loop's own periodic re-check (review item
// 2), which needs the same raw cookie value to call sessions.Resolve
// again, not just the once-resolved session/user.
type wsAuth struct {
	rawToken string
	userID   int64
}

func (h *wsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" || !h.allowedOrigins[origin] {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	auth, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Origin was already checked above, against the same list
		// csrfProtection uses for REST -- InsecureSkipVerify tells the
		// library not to run its own, differently-shaped check
		// (host-pattern matching, not an exact origin allow-list) on
		// top of it.
		InsecureSkipVerify: true,
	})
	if err != nil {
		h.logger.Error("ws: accept", "error", err)
		return
	}
	wsConn.SetReadLimit(h.live.MaxMessageSize)

	sink := &wsSink{conn: wsConn}
	remoteIP := clientIP(r, h.trustProxy)
	conn := hub.NewConnection(sink, auth.userID, "", remoteIP, h.live, h.clk)
	if err := h.hub.Register(conn); err != nil {
		_ = wsConn.Close(websocket.StatusPolicyViolation, "too many connections")
		return
	}
	defer h.hub.Unregister(conn)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go h.heartbeatLoop(ctx, conn, sink, auth)
	h.readLoop(ctx, wsConn, conn, sink)
}

// authenticate resolves the session cookie before the handshake,
// writing the appropriate HTTP status itself on failure (never a
// WebSocket close frame: the handshake has not happened yet).
func (h *wsHandler) authenticate(w http.ResponseWriter, r *http.Request) (wsAuth, bool) {
	cookie, err := r.Cookie(h.cookieName)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return wsAuth{}, false
	}
	sess, err := h.sessions.Resolve(r.Context(), cookie.Value)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return wsAuth{}, false
	}
	user, err := h.users.GetByID(r.Context(), sess.UserID)
	if err != nil || user.Disabled() {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return wsAuth{}, false
	}
	return wsAuth{rawToken: cookie.Value, userID: user.ID}, true
}

// heartbeatLoop sends `heartbeat` on every tick and re-validates the
// session on the same tick (review item 2: "the server re-validates
// the session on every heartbeat tick, and closes the connection with
// a reason when it is no longer valid. The worst case is then one
// heartbeat interval."), rather than only on a write -- a connection
// that only ever reads a busy feed, never writing, would otherwise
// never have its session re-checked at all.
func (h *wsHandler) heartbeatLoop(ctx context.Context, conn *hub.Connection, sink *wsSink, auth wsAuth) {
	interval := h.live.HeartbeatInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sess, err := h.sessions.Resolve(ctx, auth.rawToken)
			if err != nil {
				conn.Close(hub.CloseSessionInvalid, "session no longer valid")
				return
			}
			user, err := h.users.GetByID(ctx, sess.UserID)
			if err != nil || user.Disabled() {
				conn.Close(hub.CloseSessionInvalid, "session no longer valid")
				return
			}
			sendEnvelope(ctx, sink, hub.Envelope{V: 1, Type: hub.TypeHeartbeat, ID: hub.NewEventID(), TS: h.clk.Now()})
		}
	}
}

// subscribeRequest is the `subscribe`/`unsubscribe` payload shape
// (adr-004-ws-contract-draft.md).
type subscribeRequest struct {
	InstrumentID int64  `json:"instrument_id"`
	Interval     string `json:"interval"`
}

func (h *wsHandler) readLoop(ctx context.Context, wsConn *websocket.Conn, conn *hub.Connection, sink *wsSink) {
	for {
		_, data, err := wsConn.Read(ctx)
		if err != nil {
			return
		}
		var env hub.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}

		switch env.Type {
		case hub.TypeSubscribe, hub.TypeUnsubscribe:
			h.handleSubscribeMessage(ctx, conn, sink, env)
		default:
			// Any other type (snapshot, candle, ...) is server->client
			// only; silently ignored from a client, rather than
			// treated as a protocol violation worth disconnecting
			// over.
		}
	}
}

func (h *wsHandler) handleSubscribeMessage(ctx context.Context, conn *hub.Connection, sink *wsSink, env hub.Envelope) {
	if !conn.CheckSubscribeRate() {
		conn.Close(hub.CloseSubscribeFlood, "subscribe/unsubscribe rate exceeded")
		return
	}

	var req subscribeRequest
	if err := json.Unmarshal(env.Payload, &req); err != nil {
		sendError(ctx, sink, "invalid_argument", "malformed subscribe payload")
		return
	}
	interval := hub.Interval(req.Interval)
	if !interval.Valid() {
		sendError(ctx, sink, hub.ErrorCodeNotSubscribable, "unknown interval")
		return
	}

	if env.Type == hub.TypeUnsubscribe {
		h.hub.Unsubscribe(conn, req.InstrumentID, interval)
		return
	}

	snap, err := h.hub.Subscribe(ctx, conn, req.InstrumentID, interval)
	if err != nil {
		switch {
		case errors.Is(err, hub.ErrNotFound), errors.Is(err, hub.ErrNotEnabled):
			sendError(ctx, sink, hub.ErrorCodeNotSubscribable, "instrument not subscribable")
		case errors.Is(err, hub.ErrTooManySubscriptions):
			sendError(ctx, sink, "too_many_subscriptions", "subscription limit reached for this connection")
		default:
			h.logger.Error("ws: subscribe", "instrument_id", req.InstrumentID, "interval", req.Interval, "error", err)
			sendError(ctx, sink, "internal_error", "could not subscribe")
		}
		return
	}

	payload, err := json.Marshal(snap)
	if err != nil {
		return
	}
	sub := req.Interval
	sendEnvelope(ctx, sink, hub.Envelope{
		V: 1, Type: hub.TypeSnapshot, ID: hub.NewEventID(), TS: h.clk.Now(),
		Sub: formatSub(req.InstrumentID, sub), Payload: payload,
	})
}

func formatSub(instrumentID int64, interval string) string {
	return strconv.FormatInt(instrumentID, 10) + ":" + interval
}

func sendError(ctx context.Context, sink *wsSink, code, message string) {
	payload, err := json.Marshal(hub.ErrorPayload{Code: code, Message: message})
	if err != nil {
		return
	}
	sendEnvelope(ctx, sink, hub.Envelope{V: 1, Type: hub.TypeError, ID: hub.NewEventID(), TS: time.Now().UTC(), Payload: payload})
}

func sendEnvelope(ctx context.Context, sink *wsSink, env hub.Envelope) {
	data, err := json.Marshal(env)
	if err != nil {
		return
	}
	_ = sink.Send(ctx, data)
}

// wsSink implements hub.Sink over a real *websocket.Conn.
type wsSink struct {
	conn *websocket.Conn
}

func (s *wsSink) Send(ctx context.Context, data []byte) error {
	return s.conn.Write(ctx, websocket.MessageText, data)
}

func (s *wsSink) Close(code int, reason string) error {
	return s.conn.Close(websocket.StatusCode(code), reason)
}
