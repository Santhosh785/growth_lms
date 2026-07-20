package handlers

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// Categories are org-scoped but course-independent, same routing
// exception as collections.go — mounted under
// /api/orgs/:org_slug/categories via the existing ResolveOrg middleware.
// POST/PATCH/DELETE additionally require RequireRole(owner) at the route
// level (categories are a curated, owner-managed taxonomy per spec).

var categorySlugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

type createCategoryRequest struct {
	Name string `json:"name" binding:"required,min=1,max=200"`
}

func CreateCategory(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createCategoryRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		slug := slugify(req.Name)
		if !categorySlugPattern.MatchString(slug) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name must contain at least one alphanumeric character"})
			return
		}

		oc, _ := middleware.OrgContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		category, err := d.Categories.Create(c.Request.Context(), tx, oc.OrgID, req.Name, slug)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "a category with that name already exists"})
			return
		}
		c.JSON(http.StatusCreated, categoryResponse(category))
	}
}

func ListCategories(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, _ := middleware.OrgContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		categories, err := d.Categories.List(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(categories))
		for i, cat := range categories {
			out[i] = categoryResponse(cat)
		}
		c.JSON(http.StatusOK, gin.H{"categories": out})
	}
}

func UpdateCategory(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createCategoryRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		slug := slugify(req.Name)
		tx, _ := middleware.RequestTxFromGin(c)

		category, err := d.Categories.Update(c.Request.Context(), tx, c.Param("categoryId"), req.Name, slug)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, categoryResponse(category))
	}
}

func DeleteCategory(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		if err := d.Categories.Delete(c.Request.Context(), tx, c.Param("categoryId")); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "category not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func categoryResponse(cat *models.Category) gin.H {
	return gin.H{"id": cat.ID, "org_id": cat.OrgID, "name": cat.Name, "slug": cat.Slug}
}
