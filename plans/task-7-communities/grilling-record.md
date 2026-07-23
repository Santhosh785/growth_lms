# Grilling Record — task-7-communities

> Reference only — NOT part of the spec. Kept for a future agent, developer,
> or the author revisiting why this plan is shaped this way. Executing agents
> never see this file.

The design was resolved through a nine-question interview before
implementation. Options are listed as they were presented; the first option in
each question was the one recommended.

---

### Q1: How much of Task 7 should this deliverable actually cover?

**Options presented:**
1. Discussions + notifications only — defer all real-time/Yjs collaboration.
2. Discussions only — leave notifications beyond existing course emails.
3. Everything incl. real-time boards — full Task 7 with a collaboration service.

**Recommended:** Option 1 — real-time is a heavyweight, cleanly separable piece.
**Chosen:** Option 3 — **against the recommendation**: the user wanted the
complete engagement layer, including real-time boards, in this deliverable.

---

### Q2: How should the real-time collaboration service be built?

**Options presented:**
1. Separate Node.js Yjs service (y-websocket) — true CRDT, most faithful to the
   plan's wording; most infra, second runtime + internal service auth.
2. Go-native WebSocket hub — one language, last-write-wins/simple OT, lighter.
3. Presence only for now — defer live multi-cursor CRDT editing.

**Recommended:** Option 1 (listed first as the most plan-faithful).
**Chosen:** Option 2 — **against the recommendation**: keep a single Go
language/runtime and reuse the existing auth + RLS layer; accept last-write-wins
instead of a Yjs CRDT.

---

### Q3: Where do discussions live, and how deep does threading go?

**Options presented:**
1. Org + course, 1-level replies — two scopes in one subsystem, flat replies.
2. Course-only, 1-level replies — no org-wide forum.
3. Org + course, nested threads — arbitrary-depth Reddit-style.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

---

### Q4: How should moderation + reporting work?

**Options presented:**
1. Report queue + soft-delete — member reports, moderator queue, rows kept +
   audited, `is_org_moderator` in RLS.
2. Hard-delete, no queue — reports notify moderators, rows deleted outright.
3. Author + owner only — no moderator role, no reporting UX.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

---

### Q5: What is the notification delivery model?

**Options presented:**
1. In-app table + email, per-type prefs — bell/inbox + gated email + unsubscribe.
2. In-app table only — inbox but no new email types, prefs deferred.
3. Email-only, extend existing — no in-app inbox.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

---

### Q6: How should the Go WebSocket hub be deployed relative to the API server?

**Options presented:**
1. In-process goroutine hub — inside the existing binary, reuses cookie/JWT auth,
   no internal service secret; single-instance for now.
2. Separate `cmd/collab` binary — independently scalable, needs an internal
   HMAC/JWT service handshake.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

---

### Q7: What should the hub power in this first cut, and how are boards persisted?

**Options presented:**
1. Presence + course boards, snapshot persist — LWW per element, debounced JSON
   snapshot.
2. Presence + discussion live-updates only — no boards.
3. Presence + boards, op-log persist — append-only op log, replay semantics.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.

---

### Q8: How should @mentions resolve, given no username field on profiles?

**Options presented:**
1. Member-picker with user-id tokens — `@` opens an autocomplete; selection
   inserts a hidden `@[uuid]` token; server validates membership.
2. Add a username column — migration + uniqueness + settings UI.
3. Mention by email — parse `@email`, resolve via ProfileRepo.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended. (Verified in the codebase that
`profiles` has `email` + nullable `full_name` but no username/handle.)

---

### Q9: How should the pending uncommitted admin-register work be handled?

**Options presented:**
1. Secure it, then commit separately — gate behind platform-owner auth + rate
   limit, gitignore the `app` binary, commit as its own fix.
2. Leave as-is, start Task 7 — deal with it later.
3. Revert it entirely — discard the endpoint and the binary.

**Recommended:** Option 1.
**Chosen:** Option 1 — as recommended.
