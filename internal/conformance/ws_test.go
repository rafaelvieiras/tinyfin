package conformance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestFindInHistorySeesWhatAWaitSteppedOver covers WsStep.Lookback: take (and
// so waitFor) looks forward from the last message it matched, so a push that
// arrived between two waits is stepped over for good. A scenario that asserts
// "this change was pushed" needs findInHistory instead, which reads the whole
// buffer without consuming anything.
func TestFindInHistorySeesWhatAWaitSteppedOver(t *testing.T) {
	removal := map[string]any{"MessageType": "LibraryChanged", "Data": map[string]any{"ItemsRemoved": []any{"gone"}}}
	additions := map[string]any{"MessageType": "LibraryChanged", "Data": map[string]any{"ItemsAdded": []any{"new"}}}
	w := &wsConn{msgs: []map[string]any{removal, additions}}

	// The additions are matched first in buffer order, which steps over the
	// removal that came before them.
	wantAdded := func(m map[string]any) bool {
		d, _ := m["Data"].(map[string]any)
		return d["ItemsAdded"] != nil
	}
	if _, ok := w.take("LibraryChanged", wantAdded); !ok {
		t.Fatal("take did not find the additions")
	}
	if _, ok := w.take("LibraryChanged", func(m map[string]any) bool {
		d, _ := m["Data"].(map[string]any)
		return d["ItemsRemoved"] != nil
	}); ok {
		t.Fatal("take found the removal, but the cursor had already stepped over it")
	}
	if _, ok := w.findInHistory("LibraryChanged", func(m map[string]any) bool {
		d, _ := m["Data"].(map[string]any)
		return d["ItemsRemoved"] != nil
	}); !ok {
		t.Fatal("findInHistory did not find the removal the wait stepped over")
	}
}

// TestSocketKeepsItselfAlive covers wsConn.keepAlive: the server closes a
// connection that has said nothing for its lost timeout (60 s here), and a
// scenario that waits a minute for one batch and then another minute for the
// next one crosses that threshold. The socket has to speak, like a real
// client, or the push it is waiting for is dropped for want of a listener.
func TestSocketKeepsItselfAlive(t *testing.T) {
	old := wsKeepAliveEvery
	wsKeepAliveEvery = 50 * time.Millisecond
	t.Cleanup(func() { wsKeepAliveEvery = old })

	mux := http.NewServeMux()
	mux.HandleFunc("/socket", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		// A push after an idle stretch longer than this server's lost timeout,
		// and a reader that drops the connection when the client goes quiet.
		go func() {
			time.Sleep(600 * time.Millisecond)
			_ = c.Write(ctx, websocket.MessageText, []byte(`{"MessageType":"Late"}`))
		}()
		for {
			read, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			_, _, err := c.Read(read)
			cancel()
			if err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL)
	c.Tokens["admin"] = "tok"
	sc := Scenario{ID: "keepalive", Steps: []Step{
		{WS: &WSStep{Open: "a"}},
		{WS: &WSStep{Wait: "a", Type: "Late", TimeoutMs: 3000}},
	}}
	res := c.Run(context.Background(), []Scenario{sc}, false)
	if res[0].Status != StatusPass {
		t.Fatalf("status = %s: %s", res[0].Status, res[0].Message)
	}
}
