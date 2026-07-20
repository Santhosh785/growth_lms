// Package media wraps the two external storage providers Task 4 uploads
// content to: Bunny Stream (video) and Supabase Storage (images/files).
// Both are exposed as small interfaces, mirroring
// internal/auth/supabase_client.go's Client pattern, so handlers can be
// unit-tested against a fake instead of requiring live credentials. The
// real implementations are best-effort against each provider's documented
// REST API and have not been exercised against a live account in this
// session — treat them as a starting point to validate against a real
// sandbox before relying on them in production.
package media

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"growth-lms/internal/config"
)

// BunnyClient is everything the media handlers need from Bunny Stream.
type BunnyClient interface {
	// CreateLibrary provisions a new Stream library for an org (lazy,
	// called once per org on its first video upload) and returns its ID.
	CreateLibrary(ctx context.Context, orgName string) (libraryID string, err error)
	// CreateSignedUploadURL returns a time-limited (30 min) TUS-resumable
	// upload URL for direct browser upload into libraryID, plus the video
	// ID Bunny assigned (used as the asset's storage_key).
	CreateSignedUploadURL(ctx context.Context, libraryID string) (uploadURL, videoID string, expiresAt time.Time, err error)
	// VerifyWebhookSignature HMAC-verifies Bunny's transcoding-complete
	// webhook payload against the configured webhook secret, using a
	// constant-time comparison. Never trust a webhook call without this
	// returning true first.
	VerifyWebhookSignature(payload []byte, signatureHeader string) bool
	// SignedPlaybackURL returns a short-lived signed URL for playing back
	// a video, honoring the caller-supplied TTL (spec: <5 min for
	// draft/unpublished courses, up to 1 hour for published).
	SignedPlaybackURL(ctx context.Context, libraryID, videoID string, ttl time.Duration) (string, error)
}

// RealBunnyClient talks to Bunny's Stream API directly over HTTP. The API
// key never leaves the server — only signed URLs/tokens are handed to the
// browser.
type RealBunnyClient struct {
	apiKey        string
	webhookSecret string
	http          *http.Client
}

func NewBunnyClient(cfg config.BunnyNetConfig) *RealBunnyClient {
	return &RealBunnyClient{
		apiKey:        cfg.APIKey,
		webhookSecret: cfg.WebhookSecret,
		http:          &http.Client{Timeout: 15 * time.Second},
	}
}

var _ BunnyClient = (*RealBunnyClient)(nil)

const bunnyManagementAPIBase = "https://api.bunny.net"

// CreateLibrary calls Bunny's account-level video library management API
// to provision a dedicated Stream library for an org, so every org's
// videos are hard-isolated from every other org's at the storage layer
// (not just by our own application logic).
func (c *RealBunnyClient) CreateLibrary(ctx context.Context, orgName string) (string, error) {
	body, err := json.Marshal(map[string]string{"Name": orgName})
	if err != nil {
		return "", fmt.Errorf("media: marshal create-library request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bunnyManagementAPIBase+"/videolibrary", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("media: build create-library request: %w", err)
	}
	req.Header.Set("AccessKey", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("media: create bunny library: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("media: bunny create-library returned status %d", resp.StatusCode)
	}

	var out struct {
		ID int64 `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("media: decode create-library response: %w", err)
	}
	return fmt.Sprintf("%d", out.ID), nil
}

// CreateSignedUploadURL asks Bunny to create a new video entry in
// libraryID and returns the resumable-upload endpoint plus the assigned
// video ID. A real TUS-resumable upload additionally needs a signed
// AuthorizationSignature/expiration pair computed from the library's own
// API key (per Bunny's TUS docs) — that signing step happens per-request
// in the handler layer, since it depends on the library's own key, which
// this client would need to fetch/cache separately in a fuller
// implementation.
func (c *RealBunnyClient) CreateSignedUploadURL(ctx context.Context, libraryID string) (string, string, time.Time, error) {
	body, err := json.Marshal(map[string]string{"title": "untitled"})
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("media: marshal create-video request: %w", err)
	}

	url := fmt.Sprintf("%s/library/%s/videos", "https://video.bunnycdn.com", libraryID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("media: build create-video request: %w", err)
	}
	req.Header.Set("AccessKey", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("media: create bunny video: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", time.Time{}, fmt.Errorf("media: bunny create-video returned status %d", resp.StatusCode)
	}

	var out struct {
		GUID string `json:"guid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", time.Time{}, fmt.Errorf("media: decode create-video response: %w", err)
	}

	expiresAt := time.Now().Add(30 * time.Minute)
	tusURL := "https://video.bunnycdn.com/tusupload"
	return tusURL, out.GUID, expiresAt, nil
}

// SignedPlaybackURL builds a Bunny Stream token-authenticated playback
// URL: sha256(libraryID token + videoID + expires), per Bunny's URL token
// authentication scheme, using the library's own API key as the signing
// secret. ttl controls how soon the URL expires.
func (c *RealBunnyClient) SignedPlaybackURL(ctx context.Context, libraryID, videoID string, ttl time.Duration) (string, error) {
	expires := time.Now().Add(ttl).Unix()
	mac := hmac.New(sha256.New, []byte(c.apiKey))
	mac.Write([]byte(fmt.Sprintf("%s%s%d", libraryID, videoID, expires)))
	token := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("https://iframe.mediadelivery.net/embed/%s/%s?token=%s&expires=%d", libraryID, videoID, token, expires), nil
}

// VerifyWebhookSignature HMAC-SHA256-verifies payload against
// signatureHeader using the configured webhook secret, constant-time.
func (c *RealBunnyClient) VerifyWebhookSignature(payload []byte, signatureHeader string) bool {
	if c.webhookSecret == "" || signatureHeader == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(c.webhookSecret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signatureHeader)) == 1
}
