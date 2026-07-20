package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// signedURLTTL picks the expiry the spec requires: short-lived (<5 min)
// for a draft/unpublished/scheduled/review course's assets, up to 1 hour
// once the course is published. Revoked immediately means "no longer
// issued long-lived" — RefreshAssetURL always re-derives from the
// course's CURRENT status, so a freshly-unpublished course's next
// refresh call gets the short TTL again.
func signedURLTTL(courseStatus string) time.Duration {
	if courseStatus == models.CourseStatusPublished {
		return time.Hour
	}
	return 4 * time.Minute
}

// RefreshAssetURL regenerates and caches a signed URL for an asset, with
// TTL depending on its course's current status.
func RefreshAssetURL(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		course, _ := middleware.CourseFromGin(c)

		asset, err := d.Assets.Get(c.Request.Context(), tx, c.Param("assetId"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "asset not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		ttl := signedURLTTL(course.Status)

		var signedURL string
		if asset.StorageProvider == models.StorageProviderBunny {
			libraryID, libErr := d.Orgs.GetBunnyLibraryID(c.Request.Context(), tx, course.OrgID)
			if libErr != nil || libraryID == "" {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
			signedURL, err = d.Bunny.SignedPlaybackURL(c.Request.Context(), libraryID, asset.StorageKey, ttl)
		} else {
			signedURL, err = d.Storage.CreateSignedURL(c.Request.Context(), d.Config.Supabase.StorageBucket, asset.StorageKey, ttl)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate signed url"})
			return
		}

		updated, err := d.Assets.RefreshSignedURL(c.Request.Context(), tx, asset.ID, signedURL, time.Now().Add(ttl))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, assetResponse(updated))
	}
}

func assetResponse(a *models.Asset) gin.H {
	return gin.H{
		"id":                    a.ID,
		"course_id":             a.CourseID,
		"type":                  a.Type,
		"filename":              a.Filename,
		"storage_provider":      a.StorageProvider,
		"signed_url":            a.SignedURL,
		"signed_url_expires_at": a.SignedURLExpiresAt,
		"processing_status":     a.ProcessingStatus,
		"duration_seconds":      a.DurationSeconds,
		"thumbnail_url":         a.ThumbnailURL,
	}
}
