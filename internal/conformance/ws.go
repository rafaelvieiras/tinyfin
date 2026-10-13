package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// wsConn is one open WebSocket client with a background reader that records
// every JSON message so steps can wait for (or rule out) a message type.
type wsConn struct {
	conn   *websocket.Conn
	cancel context.CancelFunc

	mu     sync.Mutex
	msgs   []map[string]any
	next   int // first message not yet consumed by a wait
	notify chan struct{}
}

func (w *wsConn) close() {
	w.cancel()
	_ = w.conn.CloseNow()
}

func (w *wsConn) readLoop(ctx context.Context) {
	for {
		_, data, err := w.conn.Read(ctx)
		if err != nil {
			return
		}
		var m map[string]any
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		w.mu.Lock()
		w.msgs = append(w.msgs, m)
		w.mu.Unlock()
		select {
		case w.notify <- struct{}{}:
		default:
		}
	}
}

// take returns the first unconsumed message of the given type (any type when
// empty) and consumes everything up to and including it.
func (w *wsConn) take(typ string) (map[string]any, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := w.next; i < len(w.msgs); i++ {
		if typ == "" || w.msgs[i]["MessageType"] == typ {
			w.next = i + 1
			return w.msgs[i], true
		}
	}
	return nil, false
}

// waitFor blocks until take succeeds or the timeout elapses.
func (w *wsConn) waitFor(ctx context.Context, typ string, timeout time.Duration) (map[string]any, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		if m, ok := w.take(typ); ok {
			return m, true
		}
		select {
		case <-w.notify:
		case <-deadline.C:
			return nil, false
		case <-ctx.Done():
			return nil, false
		}
	}
}

func (c *Client) wsURL(token, deviceID string) (string, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/socket"
	q := url.Values{}
	q.Set("ApiKey", token)
	q.Set("deviceId", deviceID)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// runWS executes one WebSocket action. For a successful Wait it returns the
// received message as a Response so the step's assertions and captures apply
// to it; for the other actions it returns nil.
func (c *Client) runWS(ctx context.Context, ws *WSStep, rs *runState) (*Response, error) {
	switch {
	case ws.Open != "":
		as := ws.As
		if as == "" {
			as = "admin"
		}
		tok, err := c.resolveToken(as, rs.scratch)
		if err != nil {
			return nil, err
		}
		u, err := c.wsURL(tok, "conf-ws-"+ws.Open+c.DeviceSuffix)
		if err != nil {
			return nil, err
		}
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		conn, _, err := websocket.Dial(dctx, u, nil)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("open socket %s: %v", ws.Open, redactURL(err.Error(), tok))
		}
		conn.SetReadLimit(4 << 20)
		rctx, rcancel := context.WithCancel(context.Background())
		w := &wsConn{conn: conn, cancel: rcancel, notify: make(chan struct{}, 1)}
		if old := rs.sockets[ws.Open]; old != nil {
			old.close()
		}
		rs.sockets[ws.Open] = w
		go w.readLoop(rctx)
		return nil, nil
	case ws.Close != "":
		if w := rs.sockets[ws.Close]; w != nil {
			w.close()
			delete(rs.sockets, ws.Close)
		}
		return nil, nil
	case ws.Wait != "":
		w := rs.sockets[ws.Wait]
		if w == nil {
			return nil, fmt.Errorf("socket %q is not open", ws.Wait)
		}
		timeout := time.Duration(ws.TimeoutMs) * time.Millisecond
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		m, ok := w.waitFor(ctx, ws.Type, timeout)
		if ws.Absent {
			if ok {
				return nil, fmt.Errorf("socket %s: unexpected %q message %s", ws.Wait, ws.Type, show(m))
			}
			return nil, nil
		}
		if !ok {
			return nil, fmt.Errorf("socket %s: no %q message within %v", ws.Wait, ws.Type, timeout)
		}
		b, _ := json.Marshal(m)
		return &Response{Status: 200, Body: b, JSON: m}, nil
	}
	return nil, fmt.Errorf("ws step needs one of open, wait, close")
}

func redactURL(msg, token string) string {
	if token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, token, "<token>")
}
