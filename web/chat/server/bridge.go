package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var kaiInstanceGVR = schema.GroupVersionResource{
	Group:    "swarm.emai.io",
	Version:  "v1alpha2",
	Resource: "kaiinstances",
}

// bridgePool will hold per-customer connection caches in v2; for v1 each browser
// session opens a fresh upstream so a struct is overkill but the indirection
// keeps room for caching.
type bridgePool struct {
	s *server
}

func newBridgePool(s *server) *bridgePool {
	return &bridgePool{s: s}
}

// bridgeConfig collects everything needed to bring an upstream connection up.
//
// The bridge connects via the gateway's controlUi mode with
// `dangerouslyDisableDeviceAuth: true` (set by the operator template), so
// per-bridge ed25519 device identity is no longer needed — only the gateway
// auth token is sent. The chat-bridge Secret still holds a JWT-signing key,
// but that is read by auth.go for cookie sessions, not here.
type bridgeConfig struct {
	GatewayURL   string // ws://host:port
	GatewayToken string
}

// loadBridgeConfig reads the gateway URL + token from the KaiInstance for slug.
func (p *bridgePool) loadBridgeConfig(ctx context.Context, slug string) (*bridgeConfig, error) {
	if p.s.dyn == nil {
		return nil, errors.New("no kube client")
	}

	obj, err := p.s.dyn.Resource(kaiInstanceGVR).Namespace(p.s.namespace).Get(ctx, "kai-"+slug, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("load kaiinstance: %w", err)
	}
	gatewayToken, _, _ := unstructured.NestedString(obj.Object, "spec", "gatewayAuth", "token")
	if gatewayToken == "" {
		return nil, errors.New("kaiinstance missing gatewayAuth.token")
	}
	gatewayHost, _, _ := unstructured.NestedString(obj.Object, "status", "gatewayURL")
	if gatewayHost == "" {
		gatewayHost = fmt.Sprintf("kai-%s.%s.svc:18789", slug, p.s.namespace)
	}
	gatewayURL := "ws://" + gatewayHost
	// Allow override for local dev.
	if tpl := os.Getenv("GATEWAY_URL_TEMPLATE"); tpl != "" {
		gatewayURL = strings.ReplaceAll(tpl, "{slug}", slug)
	}

	return &bridgeConfig{
		GatewayURL:   gatewayURL,
		GatewayToken: gatewayToken,
	}, nil
}

// upstreamConn wraps the OpenClaw WS connection plus the per-connection session state.
type upstreamConn struct {
	ws         *websocket.Conn
	cfg        *bridgeConfig
	email      string
	sessionKey string
	pendingReq map[string]chan json.RawMessage
	reqCounter int
}

const (
	// OpenClaw classifies clients with id "openclaw-control-ui" or
	// "openclaw-tui" as operator-UI clients (see message-channel.ts:isOperatorUiClient).
	// Only these IDs activate the gateway's controlUi auth policy, including
	// dangerouslyDisableDeviceAuth. A "webchat"-id client is treated as an
	// untrusted browser session: the bypass policy doesn't apply, and the
	// server clears the requested scopes if no device identity is presented.
	// We pose as openclaw-tui (the programmatic-CLI variant) so the bridge
	// is on the controlUi auth path without triggering browser-only checks.
	clientID    = "openclaw-tui"
	clientMode  = "cli"
	role        = "operator"
	openClawVer = 3
)

// Scopes the bridge requests on connect. The gateway preserves these as long
// as we authenticate via the controlUi bypass path (see clientID comment).
//   - operator.read  → sessions.list, chat.history
//   - operator.write → sessions.create, chat.send, chat.abort
var clientScopes = []string{
	"operator.read",
	"operator.write",
}

// dialUpstream opens the upstream WS, performs the connect handshake, ensures a session.
func (p *bridgePool) dialUpstream(ctx context.Context, slug, email string) (*upstreamConn, error) {
	cfg, err := p.loadBridgeConfig(ctx, slug)
	if err != nil {
		return nil, err
	}

	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// OpenClaw's gateway.controlUi.allowedOrigins rejects connections with a
	// missing or unlisted Origin. Send the public chat host so customers' gateway
	// configs (which already allow https://<host>) accept the bridge.
	bridgeOrigin := os.Getenv("BRIDGE_ORIGIN")
	if bridgeOrigin == "" {
		bridgeOrigin = "https://kai.emai.dev"
	}
	ws, _, err := websocket.Dial(dialCtx, cfg.GatewayURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{bridgeOrigin}},
	})
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", cfg.GatewayURL, err)
	}
	ws.SetReadLimit(1 << 20) // 1 MiB

	c := &upstreamConn{
		ws:         ws,
		cfg:        cfg,
		email:      email,
		pendingReq: make(map[string]chan json.RawMessage),
	}

	// Wait for connect.challenge event, then send connect req.
	if err := c.handshake(ctx); err != nil {
		ws.Close(websocket.StatusInternalError, "handshake failed")
		return nil, err
	}
	if err := c.ensureSession(ctx); err != nil {
		ws.Close(websocket.StatusInternalError, "session failed")
		return nil, err
	}
	return c, nil
}

// handshake awaits the connect.challenge event then sends a connect request
// without device identity. The gateway is configured with
// `controlUi.dangerouslyDisableDeviceAuth: true`, so connections from
// role:operator clients without a `device` field skip the pairing-required
// check and authenticate purely with the gateway token.
func (c *upstreamConn) handshake(ctx context.Context) error {
	hsCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Wait for the challenge event so we know the gateway is past handshake init.
	// The nonce is intentionally ignored — there is no signature to compute.
	for {
		_, raw, err := c.ws.Read(hsCtx)
		if err != nil {
			return fmt.Errorf("read challenge: %w", err)
		}
		var msg struct {
			Type  string `json:"type"`
			Event string `json:"event"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg.Type == "event" && msg.Event == "connect.challenge" {
			break
		}
	}

	connectReq := map[string]any{
		"minProtocol": openClawVer,
		"maxProtocol": openClawVer,
		"client": map[string]any{
			"id":       clientID,
			"version":  "1.0.0",
			"platform": "go-bridge",
			"mode":     clientMode,
		},
		"role":      role,
		"scopes":    clientScopes,
		"caps":      []any{},
		"auth":      map[string]any{"token": c.cfg.GatewayToken},
		"locale":    "en",
		"userAgent": "emai-chat-bridge/1.0",
	}

	id := c.nextReqID("connect")
	resCh := make(chan json.RawMessage, 1)
	c.pendingReq[id] = resCh
	if err := c.send(hsCtx, map[string]any{"type": "req", "id": id, "method": "connect", "params": connectReq}); err != nil {
		return fmt.Errorf("send connect: %w", err)
	}

	// Read until we see our connect response.
	for {
		_, raw, err := c.ws.Read(hsCtx)
		if err != nil {
			return fmt.Errorf("read connect res: %w", err)
		}
		var msg struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			OK      bool            `json:"ok"`
			Error   json.RawMessage `json:"error"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg.Type == "res" && msg.ID == id {
			delete(c.pendingReq, id)
			if !msg.OK {
				return fmt.Errorf("connect rejected: %s", string(msg.Error))
			}
			return nil
		}
	}
}

// ensureSession finds an existing webchat session or creates one.
// v1 quirk: OpenClaw's per-sender scope is keyed by client.id; since we use one
// shared client.id all users share one session. Acceptable for v1; documented.
// v2: thread an email-derived alias once protocol support is confirmed.
func (c *upstreamConn) ensureSession(ctx context.Context) error {
	listID := c.nextReqID("sess-list")
	listCh := make(chan json.RawMessage, 1)
	c.pendingReq[listID] = listCh
	if err := c.send(ctx, map[string]any{"type": "req", "id": listID, "method": "sessions.list", "params": map[string]any{}}); err != nil {
		return err
	}
	res, err := c.readUntilRes(ctx, listID)
	if err != nil {
		return fmt.Errorf("sessions.list: %w", err)
	}
	if res.OK {
		var p struct {
			Sessions []struct {
				Key string `json:"key"`
			} `json:"sessions"`
		}
		_ = json.Unmarshal(res.Payload, &p)
		if len(p.Sessions) > 0 {
			c.sessionKey = p.Sessions[0].Key
			return nil
		}
	}

	createID := c.nextReqID("sess-create")
	c.pendingReq[createID] = make(chan json.RawMessage, 1)
	if err := c.send(ctx, map[string]any{
		"type":   "req",
		"id":     createID,
		"method": "sessions.create",
		"params": map[string]any{"channel": "webchat"},
	}); err != nil {
		return err
	}
	res, err = c.readUntilRes(ctx, createID)
	if err != nil {
		return fmt.Errorf("sessions.create: %w", err)
	}
	if !res.OK {
		return fmt.Errorf("sessions.create rejected: %s", string(res.Error))
	}
	var p struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(res.Payload, &p); err == nil && p.Key != "" {
		c.sessionKey = p.Key
		return nil
	}
	c.sessionKey = "agent:main:main" // fallback used by current webchat
	return nil
}

type wsRes struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	OK      bool            `json:"ok"`
	Error   json.RawMessage `json:"error"`
	Payload json.RawMessage `json:"payload"`
}

// readUntilRes reads frames from the upstream until it sees a res with the given id.
// Other frames (events, other-id responses) are dropped — the caller of this method
// is sequential bootstrap code, not the runtime pump.
func (c *upstreamConn) readUntilRes(ctx context.Context, id string) (*wsRes, error) {
	rdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		_, raw, err := c.ws.Read(rdCtx)
		if err != nil {
			return nil, err
		}
		var r wsRes
		if err := json.Unmarshal(raw, &r); err != nil {
			continue
		}
		if r.Type == "res" && r.ID == id {
			return &r, nil
		}
	}
}

func (c *upstreamConn) send(ctx context.Context, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageText, data)
}

func (c *upstreamConn) nextReqID(prefix string) string {
	c.reqCounter++
	return fmt.Sprintf("%s-%d", prefix, c.reqCounter)
}

// sendChat sends a chat.send request without blocking on the response — the
// runtime pump forwards the streaming chat events back to the browser.
func (c *upstreamConn) sendChat(ctx context.Context, text string) error {
	id := c.nextReqID("chat")
	return c.send(ctx, map[string]any{
		"type":   "req",
		"id":     id,
		"method": "chat.send",
		"params": map[string]any{
			"sessionKey":     c.sessionKey,
			"message":        text,
			"deliver":        false,
			"idempotencyKey": fmt.Sprintf("msg-%d-%s", time.Now().UnixMilli(), id),
		},
	})
}

// requestHistory issues chat.history and returns the parsed messages.
func (c *upstreamConn) requestHistory(ctx context.Context) ([]chatHistoryMessage, error) {
	id := c.nextReqID("history")
	c.pendingReq[id] = make(chan json.RawMessage, 1)
	if err := c.send(ctx, map[string]any{
		"type":   "req",
		"id":     id,
		"method": "chat.history",
		"params": map[string]any{
			"sessionKey": c.sessionKey,
			"limit":      50,
		},
	}); err != nil {
		return nil, err
	}
	res, err := c.readUntilRes(ctx, id)
	if err != nil {
		return nil, err
	}
	if !res.OK {
		return nil, fmt.Errorf("history rejected: %s", string(res.Error))
	}
	var p struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(res.Payload, &p); err != nil {
		return nil, err
	}
	out := []chatHistoryMessage{}
	for _, m := range p.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		var sb strings.Builder
		for _, c := range m.Content {
			if c.Type == "text" && c.Text != "" && !strings.HasPrefix(c.Text, "{") {
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(c.Text)
			}
		}
		text := strings.TrimSpace(sb.String())
		if text == "" {
			continue
		}
		out = append(out, chatHistoryMessage{Role: m.Role, Text: text})
	}
	return out, nil
}

type chatHistoryMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// pumpUpstreamToBrowser reads upstream events and forwards chat deltas/finals to the
// browser via the supplied callback. Returns when upstream closes or context is done.
func (c *upstreamConn) pumpUpstreamToBrowser(ctx context.Context, send func(any) error) error {
	for {
		_, raw, err := c.ws.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[bridge] upstream read error: %v", err)
			}
			return err
		}
		var env struct {
			Type    string          `json:"type"`
			Event   string          `json:"event"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		if env.Type != "event" || env.Event != "chat" {
			continue
		}
		var p struct {
			State        string `json:"state"`
			RunID        string `json:"runId"`
			ErrorMessage string `json:"errorMessage"`
			Message      struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			continue
		}
		var sb strings.Builder
		for _, blk := range p.Message.Content {
			if blk.Type == "text" && blk.Text != "" {
				sb.WriteString(blk.Text)
			}
		}
		switch p.State {
		case "delta":
			if sb.Len() > 0 {
				if err := send(map[string]any{"type": "delta", "text": sb.String(), "runId": p.RunID}); err != nil {
					return err
				}
			}
		case "final":
			if err := send(map[string]any{"type": "final", "text": sb.String(), "runId": p.RunID}); err != nil {
				return err
			}
		case "error":
			msg := p.ErrorMessage
			if msg == "" {
				msg = "agent error"
			}
			if err := send(map[string]any{"type": "error", "message": msg}); err != nil {
				return err
			}
		}
	}
}

func (c *upstreamConn) close() {
	if c.ws != nil {
		_ = c.ws.Close(websocket.StatusNormalClosure, "bye")
	}
}

// handleWS upgrades the browser connection and runs the bridge.
func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		http.Error(w, "bad slug", http.StatusBadRequest)
		return
	}
	claims, ok := s.authedClaims(r, slug)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Origin check — accept same-origin only by default. Override via env for dev.
	allowed := []string{}
	if extra := os.Getenv("ALLOWED_ORIGINS"); extra != "" {
		allowed = strings.Split(extra, ",")
	}

	browserWS, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: allowed,
	})
	if err != nil {
		log.Printf("[bridge] accept browser ws: %v", err)
		return
	}
	browserWS.SetReadLimit(1 << 20)
	defer browserWS.Close(websocket.StatusInternalError, "internal")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if s.demoMode {
		s.runDemoLoop(ctx, browserWS, claims.Sub)
		return
	}

	upstream, err := s.bridges.dialUpstream(ctx, slug, claims.Sub)
	if err != nil {
		log.Printf("[bridge] dialUpstream %s for %s: %v", slug, claims.Sub, err)
		_ = sendJSON(ctx, browserWS, map[string]any{"type": "error", "message": "Cannot reach the agent right now."})
		return
	}
	defer upstream.close()

	// Tell the browser we're ready, and shoot back history.
	if err := sendJSON(ctx, browserWS, map[string]any{"type": "ready", "email": claims.Sub}); err != nil {
		return
	}
	if hist, err := upstream.requestHistory(ctx); err == nil && len(hist) > 0 {
		_ = sendJSON(ctx, browserWS, map[string]any{"type": "history", "messages": hist})
	}

	// Pump upstream → browser in a goroutine.
	upstreamErr := make(chan error, 1)
	go func() {
		upstreamErr <- upstream.pumpUpstreamToBrowser(ctx, func(m any) error {
			return sendJSON(ctx, browserWS, m)
		})
	}()

	// Pump browser → upstream in this goroutine.
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-upstreamErr:
			if err != nil {
				log.Printf("[bridge] upstream pump ended: %v", err)
			}
			return
		default:
		}
		_, raw, err := browserWS.Read(ctx)
		if err != nil {
			return
		}
		var msg struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "send":
			if strings.TrimSpace(msg.Text) == "" {
				continue
			}
			if err := upstream.sendChat(ctx, msg.Text); err != nil {
				log.Printf("[bridge] sendChat: %v", err)
				_ = sendJSON(ctx, browserWS, map[string]any{"type": "error", "message": "Failed to send."})
			}
		case "history":
			if hist, err := upstream.requestHistory(ctx); err == nil {
				_ = sendJSON(ctx, browserWS, map[string]any{"type": "history", "messages": hist})
			}
		}
	}
}

func sendJSON(ctx context.Context, ws *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageText, data)
}

// runDemoLoop fakes a Kai conversation locally — no upstream needed. Used when
// SWARM_INSECURE_DEV_AUTH=1 so the UI can be tested without K8s.
func (s *server) runDemoLoop(ctx context.Context, ws *websocket.Conn, email string) {
	if err := sendJSON(ctx, ws, map[string]any{"type": "ready", "email": email}); err != nil {
		return
	}
	_ = sendJSON(ctx, ws, map[string]any{"type": "history", "messages": []chatHistoryMessage{
		{Role: "assistant", Text: "Hi " + email + " — this is a local demo. Send anything; I'll echo it back with a smile."},
	}})
	for {
		_, raw, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var msg struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg.Type != "send" || strings.TrimSpace(msg.Text) == "" {
			continue
		}
		runID := fmt.Sprintf("demo-%d", time.Now().UnixMilli())
		// Stream a few deltas to simulate real chat.
		go func(text, runID string) {
			parts := []string{
				"You said: ",
				text,
				"\n\n_(Demo mode — no upstream agent.)_",
			}
			for _, p := range parts {
				select {
				case <-ctx.Done():
					return
				case <-time.After(120 * time.Millisecond):
				}
				_ = sendJSON(ctx, ws, map[string]any{"type": "delta", "text": p, "runId": runID})
			}
			_ = sendJSON(ctx, ws, map[string]any{"type": "final", "text": "", "runId": runID})
		}(msg.Text, runID)
	}
}
