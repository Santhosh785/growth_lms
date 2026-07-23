// Package realtime is the Task 7 in-process WebSocket hub: presence and
// message broadcast within named rooms, running inside the API server binary
// (no separate collaboration service). It is transport-only — callers wire
// domain behavior (e.g. collaborative-board snapshot persistence) through
// SetOnMessage. Authentication/authorization happens in the HTTP upgrade
// handler before Add is called, so the hub trusts every Client handed to it.
//
// Single-instance for now: rooms live in this process's memory. Horizontal
// scale would add a Redis pub/sub fan-out between instances — a clean seam,
// deliberately out of scope here.
package realtime

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	sendBufferSize = 64
)

// Hub owns all rooms.
type Hub struct {
	mu        sync.Mutex
	rooms     map[string]*room
	onMessage func(roomID string, from *Client, msg []byte)
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{rooms: map[string]*room{}} }

// SetOnMessage registers a callback invoked for every inbound client message,
// before it is relayed to the rest of the room. Runs on the client's read
// goroutine — keep it quick (the board coordinator only mutates in-memory
// state and schedules a debounced save).
func (h *Hub) SetOnMessage(fn func(roomID string, from *Client, msg []byte)) {
	h.onMessage = fn
}

type room struct {
	id      string
	mu      sync.Mutex
	clients map[*Client]struct{}
}

// PresenceUser is one member currently connected to a room.
type PresenceUser struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

// Client is one connected socket. Fields are read-only after Add.
type Client struct {
	UserID string
	Name   string
	roomID string
	conn   *websocket.Conn
	send   chan []byte
	hub    *Hub
	room   *room
}

// Add registers a new connection in roomID, starts its read/write pumps, and
// broadcasts updated presence. The caller must have already authorized the
// user for this room.
func (h *Hub) Add(roomID, userID, name string, conn *websocket.Conn) *Client {
	h.mu.Lock()
	r, ok := h.rooms[roomID]
	if !ok {
		r = &room{id: roomID, clients: map[*Client]struct{}{}}
		h.rooms[roomID] = r
	}
	h.mu.Unlock()

	c := &Client{UserID: userID, Name: name, roomID: roomID, conn: conn, send: make(chan []byte, sendBufferSize), hub: h, room: r}

	r.mu.Lock()
	r.clients[c] = struct{}{}
	r.mu.Unlock()

	go c.writePump()
	go c.readPump()

	h.broadcastPresence(r)
	return c
}

// broadcastPresence sends the current member list to everyone in the room.
func (h *Hub) broadcastPresence(r *room) {
	r.mu.Lock()
	users := make([]PresenceUser, 0, len(r.clients))
	seen := map[string]struct{}{}
	for c := range r.clients {
		if _, dup := seen[c.UserID]; dup {
			continue
		}
		seen[c.UserID] = struct{}{}
		users = append(users, PresenceUser{UserID: c.UserID, Name: c.Name})
	}
	r.mu.Unlock()

	msg, err := json.Marshal(map[string]any{"type": "presence", "users": users})
	if err != nil {
		return
	}
	r.broadcast(msg)
}

// broadcast fans a message out to every client in the room (best effort: a
// client whose send buffer is full is dropped).
func (r *room) broadcast(msg []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c := range r.clients {
		select {
		case c.send <- msg:
		default:
			// Slow consumer: drop it to protect the room. Its readPump will
			// clean up on the next failed write.
			close(c.send)
			delete(r.clients, c)
		}
	}
}

// remove unregisters a client and returns whether the room is now empty.
func (h *Hub) remove(c *Client) (empty bool) {
	c.room.mu.Lock()
	if _, ok := c.room.clients[c]; ok {
		delete(c.room.clients, c)
		close(c.send)
	}
	empty = len(c.room.clients) == 0
	c.room.mu.Unlock()

	if empty {
		h.mu.Lock()
		delete(h.rooms, c.roomID)
		h.mu.Unlock()
	} else {
		h.broadcastPresence(c.room)
	}
	return empty
}

// readPump reads messages, invokes the hub callback, and relays each to the
// rest of the room until the connection closes.
func (c *Client) readPump() {
	defer func() {
		c.hub.remove(c)
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(1 << 20) // 1 MB per message cap
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		if c.hub.onMessage != nil {
			c.hub.onMessage(c.roomID, c, msg)
		}
		c.room.broadcastExcept(c, msg)
	}
}

// broadcastExcept relays a message to every client except the sender.
func (r *room) broadcastExcept(sender *Client, msg []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c := range r.clients {
		if c == sender {
			continue
		}
		select {
		case c.send <- msg:
		default:
			close(c.send)
			delete(r.clients, c)
		}
	}
}

// writePump drains the send channel to the socket and keeps it alive with
// pings.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
