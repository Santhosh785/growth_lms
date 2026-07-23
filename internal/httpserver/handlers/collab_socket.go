package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
	"growth-lms/internal/realtime"
)

// Task 7 real-time collaboration transport. The in-process realtime.Hub
// carries presence for a course and live board ops. WebSocket upgrade
// handlers authenticate via the session cookie (Authenticate middleware) and
// authorize with a direct membership check — NOT WithRequestTx, since a
// long-lived socket must not hold a request transaction open. Board op
// persistence is a debounced, last-write-wins snapshot handled by
// BoardCoordinator, wired to the hub in server.go.

// wsUpgrader only accepts same-origin connections (Origin host must match the
// request host), the WebSocket analogue of CSRF protection.
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return false
		}
		return sameHost(origin, r.Host)
	},
}

func sameHost(origin, host string) bool {
	// origin is like "https://host[:port]"; compare the host[:port] part.
	if i := strings.Index(origin, "://"); i >= 0 {
		origin = origin[i+3:]
	}
	return origin == host
}

// authorizeSocketOrg checks the caller is a member of orgID (or a platform
// owner) using a direct pool query, and returns their display name. It does
// not open a request transaction.
func authorizeSocketOrg(ctx context.Context, d *AuthDeps, userID, orgID string) (string, bool) {
	if _, err := d.Memberships.GetRole(ctx, d.Pool, userID, orgID); err != nil {
		profile, perr := d.Profiles.GetByID(ctx, d.Pool, userID)
		if perr != nil || !profile.IsPlatformOwner {
			return "", false
		}
		return socketDisplayName(profile), true
	}
	profile, err := d.Profiles.GetByID(ctx, d.Pool, userID)
	if err != nil {
		return "user", true
	}
	return socketDisplayName(profile), true
}

func socketDisplayName(p *models.Profile) string {
	if p.FullName != nil && *p.FullName != "" {
		return *p.FullName
	}
	return p.Email
}

// CoursePresenceSocket upgrades to a WebSocket that broadcasts presence for a
// course room (who is currently viewing). No persistence.
func CoursePresenceSocket(d *AuthDeps, hub *realtime.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, _ := middleware.AuthContextFromGin(c)
		ctx := c.Request.Context()
		courseID := c.Param("courseId")

		course, err := d.Courses.Get(ctx, d.Pool, courseID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "course not found"})
			return
		}
		name, ok := authorizeSocketOrg(ctx, d, ac.UserID, course.OrgID)
		if !ok {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return // Upgrade already wrote an error response
		}
		hub.Add("presence:course:"+courseID, ac.UserID, name, conn)
	}
}

// BoardSocket upgrades to a WebSocket for a collaborative board: presence plus
// live element ops, which BoardCoordinator persists (debounced, LWW).
func BoardSocket(d *AuthDeps, hub *realtime.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, _ := middleware.AuthContextFromGin(c)
		ctx := c.Request.Context()
		boardID := c.Param("boardId")

		board, err := d.Boards.Get(ctx, d.Pool, boardID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "board not found"})
			return
		}
		name, ok := authorizeSocketOrg(ctx, d, ac.UserID, board.OrgID)
		if !ok {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		hub.Add("board:"+boardID, ac.UserID, name, conn)
	}
}

// --- Board persistence coordinator ---------------------------------------

// boardOp is one inbound board mutation. Only "set"/"delete" ops mutate
// persisted state; other message types (e.g. cursor) are relayed but ignored
// here.
type boardOp struct {
	Type      string          `json:"type"`
	Op        string          `json:"op"`
	ElementID string          `json:"element_id"`
	Element   json.RawMessage `json:"element"`
}

type boardState struct {
	mu       sync.Mutex
	elements map[string]json.RawMessage
	loaded   bool
	dirty    bool
	timer    *time.Timer
	lastBy   string
}

// BoardCoordinator keeps in-memory board state and debounce-persists it to the
// collab_boards.snapshot column, applying last-write-wins per element. It is
// wired to the hub via Hub.SetOnMessage in server.go.
type BoardCoordinator struct {
	pool     *pgxpool.Pool
	boards   *models.CollabBoardRepo
	debounce time.Duration
	mu       sync.Mutex
	states   map[string]*boardState
}

// NewBoardCoordinator returns a coordinator with a 2s debounce window.
func NewBoardCoordinator(pool *pgxpool.Pool, boards *models.CollabBoardRepo) *BoardCoordinator {
	return &BoardCoordinator{pool: pool, boards: boards, debounce: 2 * time.Second, states: map[string]*boardState{}}
}

// OnMessage is the Hub.SetOnMessage callback. It applies board ops to in-
// memory state and schedules a debounced snapshot save; it ignores non-board
// rooms and non-op messages.
func (bc *BoardCoordinator) OnMessage(roomID string, from *realtime.Client, msg []byte) {
	if !strings.HasPrefix(roomID, "board:") {
		return
	}
	boardID := strings.TrimPrefix(roomID, "board:")

	var op boardOp
	if err := json.Unmarshal(msg, &op); err != nil || op.Type != "op" {
		return
	}

	st := bc.stateFor(boardID)
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.loaded {
		bc.loadLocked(boardID, st)
	}
	switch op.Op {
	case "set":
		if op.ElementID != "" && len(op.Element) > 0 {
			st.elements[op.ElementID] = op.Element
		}
	case "delete":
		delete(st.elements, op.ElementID)
	default:
		return
	}
	st.dirty = true
	st.lastBy = from.UserID
	bc.scheduleSaveLocked(boardID, st)
}

func (bc *BoardCoordinator) stateFor(boardID string) *boardState {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	st, ok := bc.states[boardID]
	if !ok {
		st = &boardState{elements: map[string]json.RawMessage{}}
		bc.states[boardID] = st
	}
	return st
}

// loadLocked seeds in-memory state from the persisted snapshot the first time
// a board is touched, so a partial op never clobbers existing elements. Caller
// holds st.mu.
func (bc *BoardCoordinator) loadLocked(boardID string, st *boardState) {
	st.loaded = true
	board, err := bc.boards.Get(context.Background(), bc.pool, boardID)
	if err != nil || len(board.Snapshot) == 0 {
		return
	}
	_ = json.Unmarshal(board.Snapshot, &st.elements)
	if st.elements == nil {
		st.elements = map[string]json.RawMessage{}
	}
}

// scheduleSaveLocked (re)arms the debounce timer. Caller holds st.mu.
func (bc *BoardCoordinator) scheduleSaveLocked(boardID string, st *boardState) {
	if st.timer != nil {
		st.timer.Stop()
	}
	st.timer = time.AfterFunc(bc.debounce, func() { bc.flush(boardID, st) })
}

// flush persists the current snapshot if dirty.
func (bc *BoardCoordinator) flush(boardID string, st *boardState) {
	st.mu.Lock()
	if !st.dirty {
		st.mu.Unlock()
		return
	}
	snapshot, err := json.Marshal(st.elements)
	lastBy := st.lastBy
	st.dirty = false
	st.mu.Unlock()
	if err != nil {
		return
	}
	if err := bc.boards.SaveSnapshot(context.Background(), bc.pool, boardID, snapshot, lastBy); err != nil {
		slog.Default().Error("handlers: persist board snapshot", "error", err, "board_id", boardID)
	}
}
