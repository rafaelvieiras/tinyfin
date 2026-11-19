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
func (w *wsConn) take(typ string, accept func(map[string]any) bool) (map[string]any, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := w.next; i < len(w.msgs); i++ {
		if (typ == "" || w.msgs[i]["MessageType"] == typ) && (accept == nil || accept(w.msgs[i])) {
			w.next = i + 1
			return w.msgs[i], true
		}
	}
	return nil, false
}

// findInHistory returns the first message of the given type anywhere in this
// socket's buffer, including the ones a previous wait already stepped over,
// without consuming anything. A scenario that asserts "this change was pushed
// at some point" needs it: the push can arrive before the step that looks for
// it starts, and a wait only looks forward from the last message it matched.
func (w *wsConn) findInHistory(typ string, accept func(map[string]any) bool) (map[string]any, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, m := range w.msgs {
		if (typ == "" || m["MessageType"] == typ) && (accept == nil || accept(m)) {
			return m, true
		}
	}
	return nil, false
}

// waitFor blocks until take succeeds or the timeout elapses.
func (w *wsConn) waitFor(ctx context.Context, typ string, accept func(map[string]any) bool, timeout time.Duration) (map[string]any, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		if m, ok := w.take(typ, accept); ok {
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

func (c *Client) wsURL(param, token, deviceID string) (string, error) {
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
	q.Set(param, token)
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
		param := ws.Param
		if param == "" {
			param = "ApiKey"
		}
		u, err := c.wsURL(param, tok, "conf-ws-"+ws.Open+c.DeviceSuffix)
		if err != nil {
			return nil, err
		}
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		conn, hresp, err := websocket.Dial(dctx, u, nil)
		cancel()
		if ws.RefuseStatus != 0 {
			if err == nil {
				conn.CloseNow()
				return nil, fmt.Errorf("socket %s upgraded, want refusal with %d", ws.Open, ws.RefuseStatus)
			}
			if hresp == nil || hresp.StatusCode != ws.RefuseStatus {
				got := 0
				if hresp != nil {
					got = hresp.StatusCode
				}
				return nil, fmt.Errorf("socket %s refused with %d, want %d", ws.Open, got, ws.RefuseStatus)
			}
			return nil, nil
		}
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
		var accept func(map[string]any) bool
		if len(ws.Where) > 0 {
			accept = func(m map[string]any) bool {
				b, _ := json.Marshal(m)
				r := &Response{Status: 200, Body: b, JSON: m}
				for _, a := range ws.Where {
					if c.checkAssert(r, a, rs.scratch) != nil {
						return false
					}
				}
				return true
			}
		}
		var m map[string]any
		var ok bool
		if ws.Lookback {
			m, ok = w.findInHistory(ws.Type, accept)
		}
		if !ok {
			m, ok = w.waitFor(ctx, ws.Type, accept, timeout)
		}
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
