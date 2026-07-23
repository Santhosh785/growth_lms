---
task: 6
name: realtime-hub
parallel_group: 4
depends_on: [3, 5]
issue: none
---

# Task 6: In-process realtime hub + WebSocket endpoints

## What to build

A Go-native, in-process WebSocket hub (running inside the API binary) for
presence and collaborative board ops — no separate service.

- **Hub** — named rooms with member presence broadcast and message relay
  (relay to everyone except the sender); read/write pumps with ping/pong
  keepalive; a `SetOnMessage` hook for domain behavior. Transport-only; single
  instance with a documented Redis-pub/sub seam for scale-out.
- **WebSocket endpoints** — course presence and board rooms. Authenticated by
  the session cookie and authorized with a direct membership check; NOT a
  request transaction (a long-lived socket must not hold one open). Same-origin
  check on upgrade.
- **Board coordinator** — the hub's message callback: applies board element
  ops last-write-wins into in-memory state (lazily seeded from the persisted
  snapshot so a partial op never clobbers existing elements) and
  debounce-persists the JSON snapshot to `collab_boards`.

## Acceptance criteria

- [ ] Presence is broadcast on join/leave; a client's op reaches other room
      members but not the sender.
- [ ] Board ops persist as a debounced snapshot; a new viewer restores current
      state.
- [ ] Non-members are rejected at upgrade; cross-origin upgrades are refused.
- [ ] Hub behavior covered by a race-tested unit test.

## Boundary

Does NOT render the board HTML page (task 7). Reuses the board repo (task 3)
and the membership check pattern (task 5).

## Commit convention

Feature commit on the `task-7-communities` branch (disk-only plan).
