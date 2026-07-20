---
task: 6
name: media-clients
parallel_group: 2
depends_on: [2]
issue: N/A (disk-only draft, no GitHub remote configured)
---

# Task 6: Bunny Stream and Supabase Storage media clients

## What to build

New package `internal/media/`, mirroring `internal/auth/supabase_client.go`'s
interface-plus-real-implementation pattern exactly (interface for
testability, real implementation is best-effort against documented APIs,
not live-verified without credentials).

`internal/media/bunny.go`:
- `BunnyClient` interface: `CreateLibrary(ctx, orgName string) (libraryID
  string, err error)`, `CreateSignedUploadURL(ctx, libraryID, videoTitle
  string) (uploadURL string, expiresAt time.Time, err error)` (30 min
  expiry, TUS-resumable per spec), `VerifyWebhookSignature(payload []byte,
  signatureHeader string) bool`, `SignedPlaybackURL(ctx, libraryID,
  videoID string, ttl time.Duration) (string, error)`.
- `RealBunnyClient` implementation using `config.BunnyNetConfig` (API key
  never leaves the server — only signed URLs/tokens go to the browser).

`internal/media/storage.go`:
- `StorageClient` interface: `CreateSignedUploadURL(ctx, bucket, path
  string) (uploadURL string, expiresAt time.Time, err error)` (30 min
  expiry), `CreateSignedURL(ctx, bucket, path string, ttl time.Duration)
  (string, error)`, `HeadObject(ctx, bucket, path string) (sizeBytes
  int64, exists bool, err error)` (used by the upload-confirmation flow to
  verify an object actually exists server-side before trusting client-
  reported metadata — never trust the client's "I uploaded it" call
  alone).
- `RealStorageClient` implementation using `config.SupabaseConfig`
  (service-role key, never exposed to the browser).

Both real implementations should be small, direct `net/http` callers (no
new SDK dependency) — same style as `supabase_client.go`'s `do()`/
`checkStatus()` helpers; feel free to share that style but these are a
different package (`media`, not `auth`) since they're a distinct concern.

## Acceptance criteria

- [ ] `BunnyClient` and `StorageClient` are interfaces; `AuthDeps` (Task 8)
      depends on the interface type, not the concrete struct.
- [ ] `RealBunnyClient.VerifyWebhookSignature` does an HMAC comparison
      using `config.BunnyNetConfig.WebhookSecret` (Task 2) with constant-
      time comparison (`crypto/subtle`), never a `==` string compare.
- [ ] `RealStorageClient.HeadObject` makes an actual HTTP HEAD/metadata
      call — never fabricates a result from client-supplied data.
- [ ] Signed upload URLs are documented/typed as 30-minute expiry; signed
      playback/access URLs support a caller-supplied TTL (so handlers can
      pick <5 min for drafts, up to 1 hour for published, per spec).
- [ ] Package has at least a fake/mock implementation of each interface
      available for other packages' tests to import (e.g.
      `media.FakeBunnyClient`, `media.FakeStorageClient` behind a
      `_test.go`-adjacent exported helper or a small `mediatest` sub-
      package — whichever keeps production code from importing test-only
      code).

## Commit convention

This is a disk-only plan (no GitHub remote configured) — commit normally
with a descriptive message; no `Closes #` trailer applies.
