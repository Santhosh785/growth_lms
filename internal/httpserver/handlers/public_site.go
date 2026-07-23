// Package handlers: public, unauthenticated org site surfaces (plan.md
// Task 8): the landing-page builder's rendered output, sitemap.xml,
// robots.txt, and an embeddable course catalog. None of these routes sit
// behind Authenticate/WithRequestTx/ResolveOrg — like certificate
// verification and unsubscribe before them, they resolve everything they
// need through SECURITY DEFINER SQL functions (list_published_courses,
// and org_pages_select's own is_published branch) since an anonymous
// visitor has no membership/session context to satisfy ordinary RLS.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/cache"
	"growth-lms/internal/models"
)

// catalogTTL bounds how long a published-course catalog may be served from
// cache before a fresh read. Short enough that a publish/unpublish that
// somehow misses invalidation still self-heals within a minute.
const catalogTTL = 60 * time.Second

// publishedCatalog returns an org's published-course list through the Task 11
// read-through cache, keyed by org ID. Falls back to a direct DB read when the
// cache is disabled or Redis is unavailable.
func (d *AuthDeps) publishedCatalog(ctx context.Context, orgID string) ([]models.PublishedCourse, error) {
	return cache.GetOrLoad(ctx, d.Catalog, orgID, catalogTTL, func() ([]models.PublishedCourse, error) {
		return d.Courses.ListPublished(ctx, d.Pool, orgID)
	})
}

// PublicOrgHome renders GET /o/:org_slug — the org's landing page
// (org_pages slug "home") if one has been published, else a minimal
// fallback so a brand-new org still has a working public URL.
func PublicOrgHome(d *AuthDeps) gin.HandlerFunc {
	return renderOrgPage(d, "home")
}

// PublicOrgPage renders GET /o/:org_slug/pages/:slug — any other
// published page from the landing-page builder.
func PublicOrgPage(d *AuthDeps) gin.HandlerFunc {
	return renderOrgPage(d, "")
}

func renderOrgPage(d *AuthDeps, fixedSlug string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		orgSlug := c.Param("org_slug")
		slug := fixedSlug
		if slug == "" {
			slug = c.Param("slug")
		}

		org, err := d.Orgs.GetBySlug(ctx, d.Pool, orgSlug)
		if err != nil {
			c.String(http.StatusNotFound, "organization not found")
			return
		}

		branding, err := d.Orgs.GetBranding(ctx, d.Pool, orgSlug)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		page, err := d.OrgPages.GetBySlug(ctx, d.Pool, org.ID, slug)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				if slug == "home" {
					c.Header("Content-Type", "text/html; charset=utf-8")
					c.String(http.StatusOK, fallbackHomeHTML(org.Name, branding.MetaDescription))
					return
				}
				c.String(http.StatusNotFound, "page not found")
				return
			}
			c.String(http.StatusInternalServerError, "internal error")
			return
		}
		if !page.IsPublished {
			c.String(http.StatusNotFound, "page not found")
			return
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, renderPublicPageHTML(org.Name, page.Title, page.ContentHTML, branding))
	}
}

func fallbackHomeHTML(orgName, metaDescription string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>%s</title>
<meta name="description" content="%s">
</head><body>
<h1>%s</h1>
<p>%s</p>
</body></html>`, htmlEscape(orgName), htmlEscape(metaDescription), htmlEscape(orgName), htmlEscape(metaDescription))
}

func renderPublicPageHTML(orgName, title, contentHTML string, b *models.OrgBranding) string {
	ogImage := ""
	if b.OGImageURL != nil {
		ogImage = fmt.Sprintf(`<meta property="og:image" content="%s">`, htmlEscape(*b.OGImageURL))
	}
	favicon := ""
	if b.FaviconURL != nil {
		favicon = fmt.Sprintf(`<link rel="icon" href="%s">`, htmlEscape(*b.FaviconURL))
	}
	jsonLD := fmt.Sprintf(`<script type="application/ld+json">{"@context":"https://schema.org","@type":"WebPage","name":%q,"publisher":{"@type":"Organization","name":%q}}</script>`,
		title, orgName)

	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>%s | %s</title>
<meta name="description" content="%s">
<meta property="og:title" content="%s">
<meta property="og:description" content="%s">
<meta property="og:type" content="website">
%s%s%s
</head><body>%s</body></html>`,
		htmlEscape(title), htmlEscape(orgName), htmlEscape(b.MetaDescription),
		htmlEscape(title), htmlEscape(b.MetaDescription), ogImage, favicon, jsonLD, contentHTML)
}

func htmlEscape(s string) string {
	r := []rune{}
	for _, c := range s {
		switch c {
		case '&':
			r = append(r, []rune("&amp;")...)
		case '<':
			r = append(r, []rune("&lt;")...)
		case '>':
			r = append(r, []rune("&gt;")...)
		case '"':
			r = append(r, []rune("&quot;")...)
		default:
			r = append(r, c)
		}
	}
	return string(r)
}

// Sitemap renders GET /o/:org_slug/sitemap.xml: every published course
// and published landing-page-builder page, as a standard urlset.
func Sitemap(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		orgSlug := c.Param("org_slug")

		org, err := d.Orgs.GetBySlug(ctx, d.Pool, orgSlug)
		if err != nil {
			c.String(http.StatusNotFound, "organization not found")
			return
		}

		base := fmt.Sprintf("%s/o/%s", d.Config.BaseURL, org.Slug)

		courses, err := d.publishedCatalog(ctx, org.ID)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}
		pages, err := d.OrgPages.ListByOrg(ctx, d.Pool, org.ID)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		var body string
		body += `<?xml version="1.0" encoding="UTF-8"?>` + "\n"
		body += `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n"
		body += fmt.Sprintf("  <url><loc>%s</loc></url>\n", base)
		for _, p := range pages {
			if !p.IsPublished {
				continue
			}
			loc := base
			if p.Slug != "home" {
				loc = fmt.Sprintf("%s/pages/%s", base, p.Slug)
			}
			body += fmt.Sprintf("  <url><loc>%s</loc><lastmod>%s</lastmod></url>\n", loc, p.UpdatedAt.Format("2006-01-02"))
		}
		for _, cr := range courses {
			body += fmt.Sprintf("  <url><loc>%s/courses/%s</loc></url>\n", base, cr.ID)
		}
		body += `</urlset>`

		c.Header("Content-Type", "application/xml; charset=utf-8")
		c.String(http.StatusOK, body)
	}
}

// Robots renders GET /o/:org_slug/robots.txt.
func Robots(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		orgSlug := c.Param("org_slug")
		body := fmt.Sprintf("User-agent: *\nAllow: /\nSitemap: %s/o/%s/sitemap.xml\n", d.Config.BaseURL, orgSlug)
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.String(http.StatusOK, body)
	}
}

// EmbedCatalog renders GET /embed/o/:org_slug/catalog: an iframe-safe
// (no nav chrome, no auth) list of published courses with a checkout
// deep-link per course/offer generated via CourseCheckoutLink below.
func EmbedCatalog(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		orgSlug := c.Param("org_slug")

		org, err := d.Orgs.GetBySlug(ctx, d.Pool, orgSlug)
		if err != nil {
			c.String(http.StatusNotFound, "organization not found")
			return
		}
		courses, err := d.publishedCatalog(ctx, org.ID)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		body := `<!doctype html><html><head><meta charset="utf-8"><style>
body{font-family:system-ui,sans-serif;margin:0;padding:1rem}
.card{border:1px solid #ddd;border-radius:8px;padding:1rem;margin-bottom:1rem}
</style></head><body>`
		for _, cr := range courses {
			body += fmt.Sprintf(`<div class="card"><h3>%s</h3><p>%s</p></div>`, htmlEscape(cr.Title), htmlEscape(cr.Description))
		}
		body += `</body></html>`

		c.Header("Content-Type", "text/html; charset=utf-8")
		// Embeddable in a third-party iframe by design — no X-Frame-Options
		// restriction here (contrast with the rest of the app, which relies
		// on Gin's default headers).
		c.String(http.StatusOK, body)
	}
}

// CourseCheckoutLink returns GET /api/orgs/:org_slug/courses/:courseId/offers/:offerId/embed-link:
// the fully-qualified checkout URL for embedding on an external site
// (plan.md's "embeddable ... checkout links"), reusing the existing
// learner checkout route rather than inventing a second checkout flow.
func CourseCheckoutLink(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		courseID := c.Param("courseId")
		offerID := c.Param("offerId")
		url := fmt.Sprintf("%s/api/courses/%s/offers/%s/checkout", d.Config.BaseURL, courseID, offerID)
		c.JSON(http.StatusOK, gin.H{
			"checkout_url":  url,
			"embed_snippet": fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener">Enroll now</a>`, url),
		})
	}
}
