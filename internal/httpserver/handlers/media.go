package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/media"
	"growth-lms/internal/models"
)

// UploadVideo lazily provisions the org's Bunny Stream library on its
// first video upload, creates the assets row immediately
// (processing_status='pending', trackable from the start), and returns a
// signed/TUS upload URL. The Bunny API key never leaves the server — only
// this signed URL does.
func UploadVideo(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Filename string `json:"filename" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		filename, err := media.ValidateUploadName(models.AssetTypeVideo, req.Filename)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		req.Filename = filename

		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		libraryID, err := d.Orgs.GetBunnyLibraryID(ctx, tx, course.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if libraryID == "" {
			libraryID, err = d.Bunny.CreateLibrary(ctx, course.OrgID)
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": "failed to provision video library"})
				return
			}
			if err := d.Orgs.SetBunnyLibraryID(ctx, tx, course.OrgID, libraryID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		}

		uploadURL, videoID, expiresAt, err := d.Bunny.CreateSignedUploadURL(ctx, libraryID)
		if err != nil {
			d.recordAlert(ctx, models.AlertSeverityWarning, models.AlertCategoryStorage,
				"bunny_upload", "failed to create video upload URL: "+err.Error(),
				map[string]any{"org_id": course.OrgID})
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to create upload url"})
			return
		}

		asset, err := d.Assets.Create(ctx, tx, course.OrgID, course.ID, models.AssetTypeVideo, req.Filename,
			models.StorageProviderBunny, videoID, ac.UserID, models.ProcessingStatusPending)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"asset":      assetResponse(asset),
			"upload_url": uploadURL,
			"expires_at": expiresAt,
		})
	}
}

// UploadFile returns a signed Supabase Storage upload URL for an
// image/file, at path org/{org_id}/courses/{course_id}/{asset_id}/
// {filename}. The assets row is created here too (processing_status
// 'pending' until UploadFileComplete confirms it), so a pendingId exists
// for the confirmation call.
func UploadFile(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Filename string `json:"filename" binding:"required"`
			Type     string `json:"type" binding:"required"` // "image" or "file"
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if req.Type != models.AssetTypeImage && req.Type != models.AssetTypeFile {
			c.JSON(http.StatusBadRequest, gin.H{"error": "type must be image or file"})
			return
		}
		filename, err := media.ValidateUploadName(req.Type, req.Filename)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		req.Filename = filename

		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		// The asset ID is part of its own storage path, so it must be
		// minted before the row exists. Using a fresh UUID here (rather
		// than letting the INSERT default it) lets the path be computed
		// up front and stored on the row in the same Create call, instead
		// of creating with an empty storage_key and reconciling it later.
		assetID := uuid.NewString()
		storageKey := "org/" + course.OrgID + "/courses/" + course.ID + "/" + assetID + "/" + req.Filename

		asset, err := d.Assets.CreateWithID(ctx, tx, assetID, course.OrgID, course.ID, req.Type, req.Filename,
			models.StorageProviderSupabase, storageKey, ac.UserID, models.ProcessingStatusPending)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		uploadURL, expiresAt, err := d.Storage.CreateSignedUploadURL(ctx, d.Config.Supabase.StorageBucket, storageKey)
		if err != nil {
			d.recordAlert(ctx, models.AlertSeverityWarning, models.AlertCategoryStorage,
				"supabase_storage", "failed to create file upload URL: "+err.Error(),
				map[string]any{"org_id": course.OrgID, "bucket": d.Config.Supabase.StorageBucket})
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to create upload url"})
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"asset":       assetResponse(asset),
			"pending_id":  asset.ID,
			"storage_key": storageKey,
			"upload_url":  uploadURL,
			"expires_at":  expiresAt,
		})
	}
}

// UploadFileComplete is called by the browser after it PUTs the file to
// the signed URL. It never trusts that call alone: it performs a
// server-side existence check (HeadObject) against Supabase Storage
// before marking the asset ready, so a forged completion call for a file
// that was never uploaded can't create a usable asset.
func UploadFileComplete(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		asset, err := d.Assets.Get(ctx, tx, c.Param("pendingId"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "pending upload not found"})
			return
		}

		sizeBytes, exists, err := d.Storage.HeadObject(ctx, d.Config.Supabase.StorageBucket, asset.StorageKey)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to verify upload"})
			return
		}
		if !exists {
			c.JSON(http.StatusConflict, gin.H{"error": "object was not found in storage; upload did not complete"})
			return
		}

		// The signed-URL PUT bypasses the app, so this is the first point the
		// server sees the real byte count. Enforce the per-type ceiling here
		// and fail the asset if the browser uploaded something oversized.
		if limit := media.MaxUploadBytes(asset.Type); sizeBytes > limit {
			_, _ = d.Assets.SetProcessingStatus(ctx, tx, asset.ID, models.ProcessingStatusFailed, nil, nil)
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": media.ErrTooLarge.Error()})
			return
		}

		updated, err := d.Assets.SetMetadata(ctx, tx, asset.ID, sizeBytes, "")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, assetResponse(updated))
	}
}
