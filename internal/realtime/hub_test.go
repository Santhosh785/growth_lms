package realtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// newTestServer stands up an httptest server that upgrades every request and
// joins the "board:test" room as the user named by ?u=.
func newTestServer(t *testing.T, hub *Hub) (string, func()) {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		u := r.URL.Query().Get("u")
		hub.Add("board:test", u, u, conn)
	}))
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?u="
	return url, srv.Close
}

func dial(t *testing.T, url, user string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url+user, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", user, err)
	}
	return conn
}

func readJSON(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal %q: %v", data, err)
	}
	return m
}

// TestHub_PresenceAndRelay proves a room broadcasts presence on join and
// relays a client's op to the other clients (but not back to the sender).
func TestHub_PresenceAndRelay(t *testing.T) {
	hub := NewHub()
	url, closeSrv := newTestServer(t, hub)
	defer closeSrv()

	alice := dial(t, url, "alice")
	defer alice.Close()
	// Alice receives presence with just herself.
	m := readJSON(t, alice)
	if m["type"] != "presence" {
		t.Fatalf("expected presence, got %v", m["type"])
	}

	bob := dial(t, url, "bob")
	defer bob.Close()
	// Both receive an updated presence (2 users). Alice's next message is the
	// presence update triggered by bob joining.
	m = readJSON(t, alice)
	if m["type"] != "presence" {
		t.Fatalf("expected presence update, got %v", m["type"])
	}
	users, _ := m["users"].([]any)
	if len(users) != 2 {
		t.Fatalf("expected 2 present users, got %d", len(users))
	}
	// Drain bob's own join presence.
	_ = readJSON(t, bob)

	// Alice sends an op; bob receives it, alice does not.
	op := `{"type":"op","op":"set","element_id":"e1","element":{"x":1}}`
	if err := alice.WriteMessage(websocket.TextMessage, []byte(op)); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readJSON(t, bob)
	if got["op"] != "set" || got["element_id"] != "e1" {
		t.Fatalf("bob did not receive relayed op: %v", got)
	}

	// Alice must NOT receive her own op (read should time out).
	_ = alice.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := alice.ReadMessage(); err == nil {
		t.Fatal("sender should not receive its own op")
	}
}

// TestHub_OnMessageCallback proves the SetOnMessage hook fires for inbound
// messages with the correct room id and sender.
func TestHub_OnMessageCallback(t *testing.T) {
	hub := NewHub()
	got := make(chan string, 1)
	hub.SetOnMessage(func(roomID string, from *Client, msg []byte) {
		got <- roomID + "|" + from.UserID
	})
	url, closeSrv := newTestServer(t, hub)
	defer closeSrv()

	alice := dial(t, url, "alice")
	defer alice.Close()
	_ = readJSON(t, alice) // presence

	if err := alice.WriteMessage(websocket.TextMessage, []byte(`{"type":"op"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case v := <-got:
		if v != "board:test|alice" {
			t.Fatalf("unexpected callback value: %s", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnMessage callback did not fire")
	}
}
