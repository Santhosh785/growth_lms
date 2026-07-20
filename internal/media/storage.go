package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"growth-lms/internal/config"
)

// StorageClient is everything the media handlers need from Supabase
// Storage, for image/file uploads (video goes to Bunny Stream instead —
// see bunny.go).
type StorageClient interface {
	// CreateSignedUploadURL returns a time-limited (30 min) signed URL the
	// browser can PUT directly to.
	CreateSignedUploadURL(ctx context.Context, bucket, path string) (uploadURL string, expiresAt time.Time, err error)
	// CreateSignedURL returns a signed URL for reading an existing
	// object, valid for ttl (spec: <5 min for drafts, up to 1 hour for
	// published courses).
	CreateSignedURL(ctx context.Context, bucket, path string, ttl time.Duration) (string, error)
	// HeadObject makes a real server-side existence check against
	// storage — used by the upload-confirmation handler so a forged
	// "/complete" call for an object that was never actually uploaded
	// can never create a usable asset record.
	HeadObject(ctx context.Context, bucket, path string) (sizeBytes int64, exists bool, err error)
	// UploadServerSide uploads bytes directly, called by the Go process
	// itself rather than issued as a signed URL for a browser to PUT to —
	// used for content the server generates (e.g. Task 5's certificate
	// PDFs), never for learner/teacher-supplied uploads, which must always
	// go through CreateSignedUploadURL + HeadObject so the upload is
	// verified rather than trusted.
	UploadServerSide(ctx context.Context, bucket, path string, data []byte, contentType string) error
}

// RealStorageClient talks to Supabase Storage's REST API directly, using
// the service-role key (never exposed to the browser — only the signed
// URLs this issues are).
type RealStorageClient struct {
	baseURL        string
	serviceRoleKey string
	http           *http.Client
}

var _ StorageClient = (*RealStorageClient)(nil)

func NewStorageClient(cfg config.SupabaseConfig) *RealStorageClient {
	return &RealStorageClient{
		baseURL:        cfg.URL,
		serviceRoleKey: cfg.ServiceRoleKey,
		http:           &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *RealStorageClient) authRequest(ctx context.Context, method, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("media: build storage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.serviceRoleKey)
	req.Header.Set("apikey", c.serviceRoleKey)
	return req, nil
}

// CreateSignedUploadURL calls Supabase Storage's
// POST /storage/v1/object/upload/sign/{bucket}/{path} endpoint, which
// returns a token usable for one PUT within its expiry window.
func (c *RealStorageClient) CreateSignedUploadURL(ctx context.Context, bucket, path string) (string, time.Time, error) {
	req, err := c.authRequest(ctx, http.MethodPost, "/storage/v1/object/upload/sign/"+bucket+"/"+path)
	if err != nil {
		return "", time.Time{}, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("media: create signed upload url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("media: supabase storage returned status %d", resp.StatusCode)
	}

	var out struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", time.Time{}, fmt.Errorf("media: decode signed upload url response: %w", err)
	}

	expiresAt := time.Now().Add(30 * time.Minute)
	return c.baseURL + "/storage/v1" + out.URL, expiresAt, nil
}

// CreateSignedURL calls Supabase Storage's
// POST /storage/v1/object/sign/{bucket}/{path} endpoint for read access.
func (c *RealStorageClient) CreateSignedURL(ctx context.Context, bucket, path string, ttl time.Duration) (string, error) {
	body := fmt.Sprintf(`{"expiresIn": %d}`, int(ttl.Seconds()))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/storage/v1/object/sign/"+bucket+"/"+path, strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("media: build signed url request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.serviceRoleKey)
	req.Header.Set("apikey", c.serviceRoleKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("media: create signed url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("media: supabase storage returned status %d", resp.StatusCode)
	}

	var out struct {
		SignedURL string `json:"signedURL"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("media: decode signed url response: %w", err)
	}
	return c.baseURL + "/storage/v1" + out.SignedURL, nil
}

// HeadObject calls Supabase Storage's object-info endpoint to confirm an
// object actually exists at path before the caller trusts any
// client-reported metadata about it.
func (c *RealStorageClient) HeadObject(ctx context.Context, bucket, path string) (int64, bool, error) {
	req, err := c.authRequest(ctx, http.MethodGet, "/storage/v1/object/info/"+bucket+"/"+path)
	if err != nil {
		return 0, false, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("media: head object: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return 0, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, false, fmt.Errorf("media: supabase storage returned status %d", resp.StatusCode)
	}

	var out struct {
		Size int64 `json:"size"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, false, fmt.Errorf("media: decode object info response: %w", err)
	}
	return out.Size, true, nil
}

// UploadServerSide calls Supabase Storage's
// POST /storage/v1/object/{bucket}/{path} endpoint to upload bytes
// directly using the service-role key — the same credential every other
// method on this client uses, since this is a trusted server-to-server
// call, not a signed URL handed to a browser. Overwrites any existing
// object at path (x-upsert: true), since server-generated content (e.g. a
// re-issued certificate) is expected to be idempotently replaceable.
func (c *RealStorageClient) UploadServerSide(ctx context.Context, bucket, path string, data []byte, contentType string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/storage/v1/object/"+bucket+"/"+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("media: build upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.serviceRoleKey)
	req.Header.Set("apikey", c.serviceRoleKey)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-upsert", "true")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("media: upload server side: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("media: supabase storage returned status %d", resp.StatusCode)
	}
	return nil
}
