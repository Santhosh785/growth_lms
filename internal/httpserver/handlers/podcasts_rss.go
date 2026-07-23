package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/models"
	"growth-lms/internal/podcast"
)

// PodcastRSS renders GET /o/:org_slug/podcasts/:show_slug/rss.xml — the
// public, unauthenticated RSS 2.0 feed a podcast app subscribes to. Like
// Task 8's public site routes it sits behind NO Authenticate/WithRequestTx/
// ResolveOrg: an anonymous podcast client has no session, so it resolves
// the published show and its episodes through the get_published_podcast_show
// / list_published_podcast_episodes SECURITY DEFINER functions, which
// hard-limit output to published rows of a podcasts-enabled org regardless
// of caller. The platform-level LMS_PODCASTS_ENABLED kill-switch is checked
// here (the org-level toggle is enforced inside the SQL functions).
func PodcastRSS(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !d.Config.Podcasts.Enabled {
			c.String(http.StatusNotFound, "not found")
			return
		}
		ctx := c.Request.Context()
		orgSlug := c.Param("org_slug")
		showSlug := c.Param("show_slug")

		org, err := d.Orgs.GetBySlug(ctx, d.Pool, orgSlug)
		if err != nil {
			c.String(http.StatusNotFound, "not found")
			return
		}

		show, err := d.PodcastShows.GetPublishedBySlug(ctx, d.Pool, org.ID, showSlug)
		if err != nil {
			c.String(http.StatusNotFound, "not found")
			return
		}

		episodes, err := d.PodcastEpisodes.ListPublishedByShow(ctx, d.Pool, show.ID)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		feed := buildFeed(d.Config.BaseURL, org.Slug, show, episodes)
		c.Header("Content-Type", "application/rss+xml; charset=utf-8")
		c.String(http.StatusOK, podcast.Render(feed))
	}
}

// buildFeed assembles the DB-free podcast.Feed from the published show and
// episode projections plus the canonical public URLs.
func buildFeed(baseURL, orgSlug string, show *models.PublishedShow, episodes []*models.PublishedEpisode) podcast.Feed {
	link := fmt.Sprintf("%s/o/%s/podcasts/%s", baseURL, orgSlug, show.Slug)
	feedURL := link + "/rss.xml"

	feed := podcast.Feed{
		Title:       show.Title,
		Link:        link,
		FeedURL:     feedURL,
		Description: show.Description,
		Author:      show.Author,
		Language:    show.Language,
		Category:    show.Category,
		Episodes:    make([]podcast.FeedItem, 0, len(episodes)),
	}
	if show.ImageURL != nil {
		feed.ImageURL = *show.ImageURL
	}
	for _, e := range episodes {
		item := podcast.FeedItem{
			GUID:          e.ID,
			Title:         e.Title,
			Description:   e.Description,
			AudioURL:      e.AudioURL,
			AudioBytes:    e.AudioBytes,
			AudioMimeType: e.AudioMimeType,
			Duration:      e.Duration,
			EpisodeNumber: e.EpisodeNumber,
			SeasonNumber:  e.SeasonNumber,
			PublishedAt:   e.PublishedAt,
		}
		feed.Episodes = append(feed.Episodes, item)
	}
	return feed
}
