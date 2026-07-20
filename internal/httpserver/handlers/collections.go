package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// Collections are org-scoped but course-independent — mounted under
// /api/orgs/:org_slug/collections using the existing ResolveOrg
// middleware, per the main-plan's noted routing exception (there's no
// :courseId to derive org context from for these two resource types).

type createCollectionRequest struct {
	Name        string `json:"name" binding:"required,min=1,max=300"`
	Description string `json:"description"`
}

func CreateCollection(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createCollectionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		collection, err := d.Collections.Create(c.Request.Context(), tx, oc.OrgID, ac.UserID, req.Name, req.Description)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, collectionResponse(collection))
	}
}

func ListCollections(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, _ := middleware.OrgContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)

		collections, err := d.Collections.List(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(collections))
		for i, col := range collections {
			out[i] = collectionResponse(col)
		}
		c.JSON(http.StatusOK, gin.H{"collections": out})
	}
}

func UpdateCollection(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createCollectionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		collection, err := d.Collections.Update(c.Request.Context(), tx, c.Param("collectionId"), req.Name, req.Description)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, collectionResponse(collection))
	}
}

func DeleteCollection(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		if err := d.Collections.Delete(c.Request.Context(), tx, c.Param("collectionId")); err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

type addCourseToCollectionRequest struct {
	CourseID string `json:"course_id" binding:"required"`
}

func AddCourseToCollection(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req addCourseToCollectionRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		oc, _ := middleware.OrgContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		collectionID := c.Param("collectionId")

		existing, err := d.Collections.ListCourses(c.Request.Context(), tx, collectionID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		sortOrders := make([]float64, len(existing))
		for i, cc := range existing {
			sortOrders[i] = cc.SortOrder
		}
		next := models.NextSortOrder(sortOrders)

		if err := d.Collections.AddCourse(c.Request.Context(), tx, collectionID, req.CourseID, oc.OrgID, next); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Status(http.StatusCreated)
	}
}

func RemoveCourseFromCollection(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		if err := d.Collections.RemoveCourse(c.Request.Context(), tx, c.Param("collectionId"), c.Param("courseId")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func ListCollectionCourses(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		courses, err := d.Collections.ListCourses(c.Request.Context(), tx, c.Param("collectionId"))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(courses))
		for i, cc := range courses {
			out[i] = gin.H{"course_id": cc.CourseID, "sort_order": cc.SortOrder}
		}
		c.JSON(http.StatusOK, gin.H{"courses": out})
	}
}

func ReorderCollectionCourses(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req reorderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		collectionID := c.Param("collectionId")
		for _, item := range req.Items {
			if err := d.Collections.SetCourseSortOrder(c.Request.Context(), tx, collectionID, item.ID, item.SortOrder); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		}
		c.Status(http.StatusNoContent)
	}
}

func collectionResponse(col *models.Collection) gin.H {
	return gin.H{
		"id":          col.ID,
		"org_id":      col.OrgID,
		"name":        col.Name,
		"description": col.Description,
		"updated_at":  col.UpdatedAt,
	}
}
