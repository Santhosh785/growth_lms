package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// Task 9 Podcasts module: curated playlist authoring (owner/teacher). Every
// handler gates on the feature flag via podcastGate, the same as the show/
// episode handlers in podcasts.go.

type podcastPlaylistRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	IsPublished bool   `json:"is_published"`
}

// CreatePodcastPlaylist is POST /api/orgs/:org_slug/podcasts/playlists.
func CreatePodcastPlaylist(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req podcastPlaylistRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title is required"})
			return
		}
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		pl, err := d.PodcastPlaylists.Create(ctx, tx, oc.OrgID, req.Title, req.Description, req.IsPublished, ac.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create playlist"})
			return
		}
		d.auditPodcast(c, oc.OrgID, ac.UserID, "podcast.playlist_created", "podcast_playlist", pl.ID)
		c.JSON(http.StatusCreated, gin.H{"playlist": pl})
	}
}

// ListPodcastPlaylists is GET /api/orgs/:org_slug/podcasts/playlists.
func ListPodcastPlaylists(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		pls, err := d.PodcastPlaylists.ListByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"playlists": pls})
	}
}

// getOrgPlaylist loads a playlist and verifies org ownership, 404ing otherwise.
func (d *AuthDeps) getOrgPlaylist(c *gin.Context, orgID string) (*models.PodcastPlaylist, bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	pl, err := d.PodcastPlaylists.Get(c.Request.Context(), tx, c.Param("playlistId"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "playlist not found"})
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if pl.OrgID != orgID {
		c.JSON(http.StatusNotFound, gin.H{"error": "playlist not found"})
		return nil, false
	}
	return pl, true
}

// GetPodcastPlaylist is GET /api/orgs/:org_slug/podcasts/playlists/:playlistId,
// returning the playlist plus its ordered episodes.
func GetPodcastPlaylist(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		pl, ok := d.getOrgPlaylist(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		items, err := d.PodcastPlaylists.ListItems(c.Request.Context(), tx, pl.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"playlist": pl, "episodes": items})
	}
}

// DeletePodcastPlaylist is DELETE /api/orgs/:org_slug/podcasts/playlists/:playlistId.
func DeletePodcastPlaylist(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		pl, ok := d.getOrgPlaylist(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		if err := d.PodcastPlaylists.Delete(c.Request.Context(), tx, pl.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		d.auditPodcast(c, oc.OrgID, ac.UserID, "podcast.playlist_deleted", "podcast_playlist", pl.ID)
		c.Status(http.StatusNoContent)
	}
}

type podcastPlaylistItemRequest struct {
	EpisodeID string `json:"episode_id"`
	SortOrder int    `json:"sort_order"`
}

// AddPodcastPlaylistItem is POST /api/orgs/:org_slug/podcasts/playlists/:playlistId/items.
func AddPodcastPlaylistItem(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req podcastPlaylistItemRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.EpisodeID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "episode_id is required"})
			return
		}
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		pl, ok := d.getOrgPlaylist(c, oc.OrgID)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)

		// Confirm the episode belongs to this org before linking it, so a
		// playlist can never reference another org's episode.
		ep, err := d.PodcastEpisodes.Get(ctx, tx, req.EpisodeID)
		if err != nil || ep.OrgID != oc.OrgID {
			c.JSON(http.StatusNotFound, gin.H{"error": "episode not found"})
			return
		}

		item, err := d.PodcastPlaylists.AddItem(ctx, tx, pl.ID, req.EpisodeID, oc.OrgID, req.SortOrder)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"item": item})
	}
}

// RemovePodcastPlaylistItem is DELETE
// /api/orgs/:org_slug/podcasts/playlists/:playlistId/items/:episodeId.
func RemovePodcastPlaylistItem(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.podcastGate(c)
		if !ok {
			return
		}
		pl, ok := d.getOrgPlaylist(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		err := d.PodcastPlaylists.RemoveItem(c.Request.Context(), tx, pl.ID, c.Param("episodeId"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "item not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}
