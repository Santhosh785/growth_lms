// Package mediatest provides in-memory fakes of media.BunnyClient and
// media.StorageClient for handler/worker tests that need predictable
// signed-URL/webhook behavior without live Bunny/Supabase credentials.
package mediatest

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"growth-lms/internal/media"
)

var (
	_ media.BunnyClient   = (*FakeBunnyClient)(nil)
	_ media.StorageClient = (*FakeStorageClient)(nil)
)

// FakeBunnyClient is a deterministic, in-memory media.BunnyClient.
type FakeBunnyClient struct {
	LibraryIDToReturn string
	WebhookSecret     string
}

func (f *FakeBunnyClient) CreateLibrary(ctx context.Context, orgName string) (string, error) {
	if f.LibraryIDToReturn != "" {
		return f.LibraryIDToReturn, nil
	}
	return "lib-" + uuid.NewString(), nil
}

func (f *FakeBunnyClient) CreateSignedUploadURL(ctx context.Context, libraryID string) (string, string, time.Time, error) {
	videoID := "video-" + uuid.NewString()
	return "https://fake-upload.example/" + videoID, videoID, time.Now().Add(30 * time.Minute), nil
}

func (f *FakeBunnyClient) VerifyWebhookSignature(payload []byte, signatureHeader string) bool {
	return f.WebhookSecret != "" && signatureHeader == "valid-signature"
}

func (f *FakeBunnyClient) SignedPlaybackURL(ctx context.Context, libraryID, videoID string, ttl time.Duration) (string, error) {
	return fmt.Sprintf("https://fake-playback.example/%s/%s?ttl=%d", libraryID, videoID, int(ttl.Seconds())), nil
}

// FakeStorageClient is a deterministic, in-memory media.StorageClient.
// Objects "exist" once RegisterObject has been called for their path,
// simulating a real upload having completed server-side.
type FakeStorageClient struct {
	objects map[string]int64
}

func NewFakeStorageClient() *FakeStorageClient {
	return &FakeStorageClient{objects: map[string]int64{}}
}

// RegisterObject simulates an object having actually been uploaded to
// storage, so HeadObject reports it as existing.
func (f *FakeStorageClient) RegisterObject(bucket, path string, sizeBytes int64) {
	if f.objects == nil {
		f.objects = map[string]int64{}
	}
	f.objects[bucket+"/"+path] = sizeBytes
}

func (f *FakeStorageClient) CreateSignedUploadURL(ctx context.Context, bucket, path string) (string, time.Time, error) {
	return "https://fake-storage.example/upload/" + bucket + "/" + path, time.Now().Add(30 * time.Minute), nil
}

func (f *FakeStorageClient) CreateSignedURL(ctx context.Context, bucket, path string, ttl time.Duration) (string, error) {
	return fmt.Sprintf("https://fake-storage.example/%s/%s?ttl=%d", bucket, path, int(ttl.Seconds())), nil
}

func (f *FakeStorageClient) HeadObject(ctx context.Context, bucket, path string) (int64, bool, error) {
	size, ok := f.objects[bucket+"/"+path]
	return size, ok, nil
}

// UploadServerSide records the object as existing (so a subsequent
// HeadObject sees it), mirroring what a real server-side upload would
// leave behind.
func (f *FakeStorageClient) UploadServerSide(ctx context.Context, bucket, path string, data []byte, contentType string) error {
	f.RegisterObject(bucket, path, int64(len(data)))
	return nil
}
