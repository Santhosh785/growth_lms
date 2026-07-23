package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// This file implements Task 9's Podcasts & RSS authoring, settings, and
// learner-progress HTTP surface. Every authoring/progress handler goes
// through podcastGate, which enforces the two-flag feature gate the plan
// requires for advanced modules: the platform-level LMS_PODCASTS_ENABLED
// AND the org's own podcasts_enabled toggle must both be true. The public
// RSS feed lives in podcasts_rss.go (unauthenticated, SECURITY DEFINER).
//
// There is no per-call cost to meter here the way the AI module meters
// tokens, so this module has no ledger table: observability is the audit
// events written on publish/create actions plus the podcast_progress
// consumption record.

// podcastsEnabledForOrg reports whether the Podcasts module is switched on
// for this org: the platform flag AND the org's own toggle must both be true.
func (d *AuthDeps) podcastsEnabledForOrg(ctx context.Context, tx models.Querier, orgID string) (bool, error) {
	if !d.Config.Podcasts.Enabled {
		return false, nil
	}
	return d.Orgs.GetPodcastsEnabled(ctx, tx, orgID)
}

// podcastGate resolves the request's org context and transaction and
// verifies the feature is enabled, writing the appropriate response and
// returning ok=false otherwise. Every authoring/progress handler starts
// with it.
func (d *AuthDeps) podcastGate(c *gin.Context) (middleware.OrgContext, bool) {
	oc, _ := middleware.OrgContextFromGin(c)
	tx, _ := middleware.RequestTxFromGin(c)
	enabled, err := d.podcastsEnabledForOrg(c.Request.Context(), tx, oc.OrgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return oc, false
	}
	if !enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "podcasts are not enabled for this organization"})
		return oc, false
	}
	return oc, true
}

// --- Shows (owner/teacher) -------------------------------------------------

type podcastShowRequest struct {
	CourseID    string `json:"course_id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Author      string `json:"author"`
	ImageURL    string `json:"image_url"`
	Language    string `json:"language"`
	Category    string `json:"category"`
	IsPublished bool   `json:"is_published"`
}

// CreatePodcastShow is POST /api/orgs/:org_slug/podcasts/shows.
func CreatePodcastShow(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req podcastShowRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" || req.Slug == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "slug and title are required"})
			return
		}
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		show, err := d.PodcastShows.Create(ctx, tx, models.PodcastShow{
			OrgID:       oc.OrgID,
			CourseID:    &req.CourseID,
			Slug:        req.Slug,
			Title:       req.Title,
			Description: req.Description,
			Author:      req.Author,
			ImageURL:    &req.ImageURL,
			Language:    req.Language,
			Category:    req.Category,
			IsPublished: req.IsPublished,
			CreatedBy:   &ac.UserID,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create show"})
			return
		}
		d.auditPodcast(c, oc.OrgID, ac.UserID, "podcast.show_created", "podcast_show", show.ID)
		c.JSON(http.StatusCreated, gin.H{"show": show})
	}
}

// ListPodcastShows is GET /api/orgs/:org_slug/podcasts/shows.
func ListPodcastShows(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		shows, err := d.PodcastShows.ListByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"shows": shows})
	}
}

// getOrgShow loads a show and verifies it belongs to the request's org,
// 404ing otherwise so a show id from another org is indistinguishable from
// a missing one (defense in depth over RLS).
func (d *AuthDeps) getOrgShow(c *gin.Context, orgID string) (*models.PodcastShow, bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	show, err := d.PodcastShows.Get(c.Request.Context(), tx, c.Param("showId"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "show not found"})
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if show.OrgID != orgID {
		c.JSON(http.StatusNotFound, gin.H{"error": "show not found"})
		return nil, false
	}
	return show, true
}

// GetPodcastShow is GET /api/orgs/:org_slug/podcasts/shows/:showId.
func GetPodcastShow(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		show, ok := d.getOrgShow(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		episodes, err := d.PodcastEpisodes.ListByShow(c.Request.Context(), tx, show.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"show": show, "episodes": episodes})
	}
}

// UpdatePodcastShow is PATCH /api/orgs/:org_slug/podcasts/shows/:showId.
func UpdatePodcastShow(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req podcastShowRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title is required"})
			return
		}
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		show, ok := d.getOrgShow(c, oc.OrgID)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		show.CourseID = &req.CourseID
		show.Title = req.Title
		show.Description = req.Description
		show.Author = req.Author
		show.ImageURL = &req.ImageURL
		show.Language = req.Language
		show.Category = req.Category
		show.IsPublished = req.IsPublished

		updated, err := d.PodcastShows.Update(ctx, tx, *show)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update show"})
			return
		}
		d.auditPodcast(c, oc.OrgID, ac.UserID, "podcast.show_updated", "podcast_show", updated.ID)
		c.JSON(http.StatusOK, gin.H{"show": updated})
	}
}

// DeletePodcastShow is DELETE /api/orgs/:org_slug/podcasts/shows/:showId.
func DeletePodcastShow(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		show, ok := d.getOrgShow(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		if err := d.PodcastShows.Delete(c.Request.Context(), tx, show.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		d.auditPodcast(c, oc.OrgID, ac.UserID, "podcast.show_deleted", "podcast_show", show.ID)
		c.Status(http.StatusNoContent)
	}
}

// --- Episodes (owner/teacher) ----------------------------------------------

type podcastEpisodeRequest struct {
	Title         string `json:"title"`
	Description   string `json:"description"`
	AudioURL      string `json:"audio_url"`
	AudioBytes    int64  `json:"audio_bytes"`
	AudioMimeType string `json:"audio_mime_type"`
	Duration      int    `json:"duration_seconds"`
	Transcript    string `json:"transcript"`
	EpisodeNumber *int   `json:"episode_number"`
	SeasonNumber  *int   `json:"season_number"`
}

// CreatePodcastEpisode is POST /api/orgs/:org_slug/podcasts/shows/:showId/episodes.
func CreatePodcastEpisode(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req podcastEpisodeRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" || req.AudioURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title and audio_url are required"})
			return
		}
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		show, ok := d.getOrgShow(c, oc.OrgID)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		ep, err := d.PodcastEpisodes.Create(ctx, tx, models.PodcastEpisode{
			ShowID:        show.ID,
			OrgID:         oc.OrgID,
			Title:         req.Title,
			Description:   req.Description,
			AudioURL:      req.AudioURL,
			AudioBytes:    req.AudioBytes,
			AudioMimeType: req.AudioMimeType,
			Duration:      req.Duration,
			Transcript:    req.Transcript,
			EpisodeNumber: req.EpisodeNumber,
			SeasonNumber:  req.SeasonNumber,
			CreatedBy:     &ac.UserID,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create episode"})
			return
		}
		d.auditPodcast(c, oc.OrgID, ac.UserID, "podcast.episode_created", "podcast_episode", ep.ID)
		c.JSON(http.StatusCreated, gin.H{"episode": ep})
	}
}

// getOrgEpisode loads an episode and verifies org ownership, 404ing otherwise.
func (d *AuthDeps) getOrgEpisode(c *gin.Context, orgID string) (*models.PodcastEpisode, bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	ep, err := d.PodcastEpisodes.Get(c.Request.Context(), tx, c.Param("episodeId"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "episode not found"})
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if ep.OrgID != orgID {
		c.JSON(http.StatusNotFound, gin.H{"error": "episode not found"})
		return nil, false
	}
	return ep, true
}

// UpdatePodcastEpisode is PATCH /api/orgs/:org_slug/podcasts/episodes/:episodeId.
func UpdatePodcastEpisode(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req podcastEpisodeRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" || req.AudioURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title and audio_url are required"})
			return
		}
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		ep, ok := d.getOrgEpisode(c, oc.OrgID)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		ep.Title = req.Title
		ep.Description = req.Description
		ep.AudioURL = req.AudioURL
		ep.AudioBytes = req.AudioBytes
		ep.AudioMimeType = req.AudioMimeType
		ep.Duration = req.Duration
		ep.Transcript = req.Transcript
		ep.EpisodeNumber = req.EpisodeNumber
		ep.SeasonNumber = req.SeasonNumber

		updated, err := d.PodcastEpisodes.Update(ctx, tx, *ep)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update episode"})
			return
		}
		d.auditPodcast(c, oc.OrgID, ac.UserID, "podcast.episode_updated", "podcast_episode", updated.ID)
		c.JSON(http.StatusOK, gin.H{"episode": updated})
	}
}

type podcastPublishRequest struct {
	Published bool `json:"published"`
}

// SetPodcastEpisodePublished is POST /api/orgs/:org_slug/podcasts/episodes/:episodeId/publish.
func SetPodcastEpisodePublished(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req podcastPublishRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		ep, ok := d.getOrgEpisode(c, oc.OrgID)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		updated, err := d.PodcastEpisodes.SetPublished(ctx, tx, ep.ID, req.Published)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		action := "podcast.episode_published"
		if !req.Published {
			action = "podcast.episode_unpublished"
		}
		d.auditPodcast(c, oc.OrgID, ac.UserID, action, "podcast_episode", updated.ID)
		c.JSON(http.StatusOK, gin.H{"episode": updated})
	}
}

// DeletePodcastEpisode is DELETE /api/orgs/:org_slug/podcasts/episodes/:episodeId.
func DeletePodcastEpisode(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		ep, ok := d.getOrgEpisode(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		if err := d.PodcastEpisodes.Delete(c.Request.Context(), tx, ep.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		d.auditPodcast(c, oc.OrgID, ac.UserID, "podcast.episode_deleted", "podcast_episode", ep.ID)
		c.Status(http.StatusNoContent)
	}
}

// --- Learner: episode detail + listen progress -----------------------------

// GetPodcastEpisode is GET /api/orgs/:org_slug/podcasts/episodes/:episodeId
// for any org member: the full episode (including transcript) plus the
// caller's own listen progress if any.
func GetPodcastEpisodeDetail(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		ep, ok := d.getOrgEpisode(c, oc.OrgID)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		var progress *models.PodcastProgress
		p, err := d.PodcastProgress.Get(ctx, tx, ac.UserID, ep.ID)
		if err != nil && !errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if err == nil {
			progress = p
		}
		c.JSON(http.StatusOK, gin.H{"episode": ep, "progress": progress})
	}
}

type podcastProgressRequest struct {
	PositionSeconds int  `json:"position_seconds"`
	DurationSeconds int  `json:"duration_seconds"`
	Completed       bool `json:"completed"`
}

// ReportPodcastProgress is POST /api/orgs/:org_slug/podcasts/episodes/:episodeId/progress
// for any org member: records the caller's listen position.
func ReportPodcastProgress(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req podcastProgressRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.PositionSeconds < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "position_seconds is required and must be non-negative"})
			return
		}
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		ep, ok := d.getOrgEpisode(c, oc.OrgID)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		p, err := d.PodcastProgress.Upsert(ctx, tx, oc.OrgID, ep.ID, ac.UserID,
			req.PositionSeconds, req.DurationSeconds, req.Completed)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"progress": p})
	}
}

// --- Settings (owner) ------------------------------------------------------

// GetPodcastSettings is GET /api/orgs/:org_slug/podcasts/settings (owner).
func GetPodcastSettings(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		enabled, err := d.Orgs.GetPodcastsEnabled(ctx, tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"enabled":                   enabled,
			"platform_podcasts_enabled": d.Config.Podcasts.Enabled,
		})
	}
}

type updatePodcastSettingsRequest struct {
	Enabled bool `json:"enabled"`
}

// UpdatePodcastSettings is PATCH /api/orgs/:org_slug/podcasts/settings (owner).
func UpdatePodcastSettings(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updatePodcastSettingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		if err := d.Orgs.SetPodcastsEnabled(ctx, tx, oc.OrgID, req.Enabled); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		d.auditPodcast(c, oc.OrgID, ac.UserID, "podcast.settings_updated", "organization", oc.OrgID)
		c.JSON(http.StatusOK, gin.H{"enabled": req.Enabled})
	}
}

// auditPodcast records a podcast authoring action to the audit trail —
// this module's observability surface (no token/cost ledger applies).
func (d *AuthDeps) auditPodcast(c *gin.Context, orgID, userID, action, resourceType, resourceID string) {
	ctx := c.Request.Context()
	tx, _ := middleware.RequestTxFromGin(c)
	_ = d.Audit.Record(ctx, tx, models.AuditEvent{
		OrgID: &orgID, UserID: &userID, Action: action,
		ResourceType: resourceType, ResourceID: &resourceID,
		IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
	})
}
