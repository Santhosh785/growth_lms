package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// previewSignedURLTTL is always short-lived: preview is a teacher-review
// surface, distinct from the published-course playback path (see
// signedURLTTL in assets.go), which may issue longer-lived URLs once a
// course is actually published.
const previewSignedURLTTL = 4 * time.Minute

// previewLesson/previewChapter/previewBlock are the JSON shape
// PreviewCourse assembles — a Task-4-owned, read-only approximation for
// teacher review, not the real learner-facing player Task 5 owns.
type previewBlock struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Content any    `json:"content"`
}

type previewLesson struct {
	ID     string         `json:"id"`
	Title  string         `json:"title"`
	Blocks []previewBlock `json:"blocks"`
}

type previewChapter struct {
	ID      string          `json:"id"`
	Title   string          `json:"title"`
	Lessons []previewLesson `json:"lessons"`
}

// PreviewCourse renders a teacher/owner-only, read-only assembly of a
// course's chapters/lessons/blocks in order: text as sanitized HTML,
// image/file as an asset reference, video as an asset reference with a
// short-lived signed URL, quiz as a read-only question list (no
// answering). Available regardless of course status — draft/review/
// scheduled are all previewable, since publish_date being in the future
// or absent doesn't block a teacher checking their own work.
func PreviewCourse(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		chapters, err := d.Chapters.ListByCourse(ctx, tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		out := make([]previewChapter, 0, len(chapters))
		for _, ch := range chapters {
			lessons, err := d.Lessons.ListByChapter(ctx, tx, ch.ID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
			pch := previewChapter{ID: ch.ID, Title: ch.Title}
			for _, lsn := range lessons {
				blocks, err := d.Blocks.ListByLesson(ctx, tx, lsn.ID)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
					return
				}
				plsn := previewLesson{ID: lsn.ID, Title: lsn.Title}
				for _, b := range blocks {
					var content any
					_ = json.Unmarshal(b.Content, &content)
					if b.Type == models.BlockTypeVideo || b.Type == models.BlockTypeImage || b.Type == models.BlockTypeFile {
						content = d.enrichBlockContentWithSignedURL(ctx, tx, content)
					}
					plsn.Blocks = append(plsn.Blocks, previewBlock{ID: b.ID, Type: b.Type, Content: content})
				}
				pch.Lessons = append(pch.Lessons, plsn)
			}
			out = append(out, pch)
		}

		c.JSON(http.StatusOK, gin.H{
			"course":   courseResponse(course),
			"chapters": out,
		})
	}
}

// enrichBlockContentWithSignedURL attaches a "preview_url" field (a
// short-lived signed URL) to a media block's decoded content, for
// image/video/file blocks only. Falls back to the un-enriched content on
// any lookup failure — preview is best-effort, not a security boundary
// (RLS already gates which assets are even queryable).
func (d *AuthDeps) enrichBlockContentWithSignedURL(ctx context.Context, tx models.Querier, content any) any {
	assetID := extractAssetID(content)
	if assetID == "" {
		return content
	}
	asset, err := d.Assets.Get(ctx, tx, assetID)
	if err != nil {
		return content
	}

	var signedURL string
	if asset.StorageProvider == models.StorageProviderBunny {
		libraryID, err := d.Orgs.GetBunnyLibraryID(ctx, tx, asset.OrgID)
		if err != nil || libraryID == "" {
			return content
		}
		signedURL, err = d.Bunny.SignedPlaybackURL(ctx, libraryID, asset.StorageKey, previewSignedURLTTL)
		if err != nil {
			return content
		}
	} else {
		signedURL, err = d.Storage.CreateSignedURL(ctx, d.Config.Supabase.StorageBucket, asset.StorageKey, previewSignedURLTTL)
		if err != nil {
			return content
		}
	}

	m, ok := content.(map[string]any)
	if !ok {
		return content
	}
	m["preview_url"] = signedURL
	return m
}

func extractAssetID(content any) string {
	m, ok := content.(map[string]any)
	if !ok {
		return ""
	}
	assetID, _ := m["asset_id"].(string)
	return assetID
}
