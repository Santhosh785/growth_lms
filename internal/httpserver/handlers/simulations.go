package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
	"growth-lms/internal/simulations"
)

// This file implements Task 9's interactive simulations & diagrams HTTP
// surface: teacher authoring (create/validate a simulation or diagram spec,
// publish, report) and the learner runtime (browse the published catalog,
// record interaction progress, view one's own progress). Like every advanced
// module it is gated by the two-flag feature switch (platform
// LMS_SIMULATIONS_ENABLED AND the org's simulations_enabled) — enforced by
// simGate on every handler — and tenant-scoped by RLS. Spec/config validation
// lives in internal/simulations; handlers persist the validated result and fold
// each learner interaction onto the learner-owned simulation_progress record,
// which is both the resume state and the reporting surface. There is no
// anonymous surface: a learner always interacts inside an authenticated session.

// simEnabledForOrg reports whether the simulations module is switched on for
// this org: the platform-level flag AND the org's own toggle must both be true.
func (d *AuthDeps) simEnabledForOrg(ctx context.Context, tx models.Querier, orgID string) (bool, error) {
	if !d.Config.Simulations.Enabled {
		return false, nil
	}
	return d.Orgs.GetSimulationsEnabled(ctx, tx, orgID)
}

// simGate resolves the request's org context and verifies the feature is
// enabled, writing the appropriate response and returning ok=false otherwise.
func (d *AuthDeps) simGate(c *gin.Context) (middleware.OrgContext, bool) {
	oc, _ := middleware.OrgContextFromGin(c)
	tx, _ := middleware.RequestTxFromGin(c)
	enabled, err := d.simEnabledForOrg(c.Request.Context(), tx, oc.OrgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return oc, false
	}
	if !enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "simulations are not enabled for this organization"})
		return oc, false
	}
	return oc, true
}

// getOrgSimulation loads a simulation and verifies it belongs to the request's
// org, 404ing otherwise so an id from another org is indistinguishable from a
// missing one (defense in depth over RLS).
func (d *AuthDeps) getOrgSimulation(c *gin.Context, orgID string) (*models.Simulation, bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	sim, err := d.SimulationRepo.Get(c.Request.Context(), tx, c.Param("simulationId"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "simulation not found"})
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if sim.OrgID != orgID {
		c.JSON(http.StatusNotFound, gin.H{"error": "simulation not found"})
		return nil, false
	}
	return sim, true
}

// --- Authoring (owner/teacher) ---------------------------------------------

type simulationRequest struct {
	CourseID    string          `json:"course_id"`
	LessonID    string          `json:"lesson_id"`
	Slug        string          `json:"slug"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Kind        string          `json:"kind"`
	Spec        json.RawMessage `json:"spec"`
	Config      json.RawMessage `json:"config"`
	IsPublished bool            `json:"is_published"`
}

// validateSimPayload validates the spec+config against internal/simulations,
// returning the normalized JSON to persist. A bad spec is a 400 with the
// specific reason.
func (d *AuthDeps) validateSimPayload(c *gin.Context, req simulationRequest) (spec, cfg json.RawMessage, ok bool) {
	parsedSpec, err := d.Simulations.ParseSpec(simulations.Kind(req.Kind), req.Spec)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": simReason(err)})
		return nil, nil, false
	}
	parsedCfg, err := d.Simulations.ParseConfig(req.Config)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": simReason(err)})
		return nil, nil, false
	}
	specJSON, err := json.Marshal(parsedSpec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, nil, false
	}
	cfgJSON, err := json.Marshal(parsedCfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, nil, false
	}
	return specJSON, cfgJSON, true
}

// CreateSimulation is POST /api/orgs/:org_slug/simulations.
func CreateSimulation(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req simulationRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" || req.Slug == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "slug, title and kind are required"})
			return
		}
		oc, ok := d.simGate(c)
		if !ok {
			return
		}
		specJSON, cfgJSON, ok := d.validateSimPayload(c, req)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		created, err := d.SimulationRepo.Create(ctx, tx, models.Simulation{
			OrgID:       oc.OrgID,
			CourseID:    &req.CourseID,
			LessonID:    &req.LessonID,
			Slug:        req.Slug,
			Title:       req.Title,
			Description: req.Description,
			Kind:        req.Kind,
			Spec:        specJSON,
			Config:      cfgJSON,
			IsPublished: req.IsPublished,
			CreatedBy:   &ac.UserID,
		})
		if err != nil {
			if models.IsUniqueViolation(err) {
				c.JSON(http.StatusConflict, gin.H{"error": "a simulation with that slug already exists"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		d.auditSim(c, oc.OrgID, ac.UserID, "simulation.created", "simulation", created.ID)
		c.JSON(http.StatusCreated, simulationResponse(created))
	}
}

// ListSimulations is GET /api/orgs/:org_slug/simulations (authoring view).
func ListSimulations(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.simGate(c)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		sims, err := d.SimulationRepo.ListByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"simulations": simulationList(sims)})
	}
}

// GetSimulation is GET /api/orgs/:org_slug/simulations/:simulationId (authoring).
func GetSimulation(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.simGate(c)
		if !ok {
			return
		}
		sim, ok := d.getOrgSimulation(c, oc.OrgID)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, simulationResponseFull(sim))
	}
}

// UpdateSimulation is PATCH /api/orgs/:org_slug/simulations/:simulationId.
func UpdateSimulation(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req simulationRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title and kind are required"})
			return
		}
		oc, ok := d.simGate(c)
		if !ok {
			return
		}
		existing, ok := d.getOrgSimulation(c, oc.OrgID)
		if !ok {
			return
		}
		specJSON, cfgJSON, ok := d.validateSimPayload(c, req)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		updated, err := d.SimulationRepo.Update(ctx, tx, models.Simulation{
			ID:          existing.ID,
			CourseID:    &req.CourseID,
			LessonID:    &req.LessonID,
			Title:       req.Title,
			Description: req.Description,
			Kind:        req.Kind,
			Spec:        specJSON,
			Config:      cfgJSON,
			IsPublished: req.IsPublished,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		d.auditSim(c, oc.OrgID, ac.UserID, "simulation.updated", "simulation", updated.ID)
		c.JSON(http.StatusOK, simulationResponseFull(updated))
	}
}

// SetSimulationPublished is POST /api/orgs/:org_slug/simulations/:simulationId/publish.
func SetSimulationPublished(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Published bool `json:"published"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		oc, ok := d.simGate(c)
		if !ok {
			return
		}
		existing, ok := d.getOrgSimulation(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		updated, err := d.SimulationRepo.SetPublished(c.Request.Context(), tx, existing.ID, req.Published)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		action := "simulation.unpublished"
		if req.Published {
			action = "simulation.published"
		}
		d.auditSim(c, oc.OrgID, ac.UserID, action, "simulation", updated.ID)
		c.JSON(http.StatusOK, simulationResponse(updated))
	}
}

// DeleteSimulation is DELETE /api/orgs/:org_slug/simulations/:simulationId.
func DeleteSimulation(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.simGate(c)
		if !ok {
			return
		}
		existing, ok := d.getOrgSimulation(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		if err := d.SimulationRepo.Delete(c.Request.Context(), tx, existing.ID); err != nil {
			if errNotFoundResponse(c, err, "simulation not found") {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		d.auditSim(c, oc.OrgID, ac.UserID, "simulation.deleted", "simulation", existing.ID)
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	}
}

// SimulationReport is GET /api/orgs/:org_slug/simulations/:simulationId/report
// (owner/teacher): every learner's progress on one simulation.
func SimulationReport(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.simGate(c)
		if !ok {
			return
		}
		sim, ok := d.getOrgSimulation(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		progress, err := d.SimulationProgress.ListBySimulation(c.Request.Context(), tx, sim.ID, queryLimit(c))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"simulation": simulationResponse(sim),
			"progress":   simulationProgressList(progress),
		})
	}
}

// --- Learner-facing (any member) -------------------------------------------

// ListSimulationCatalog is GET /api/orgs/:org_slug/simulations/catalog: the
// org's published simulations for any authenticated member.
func ListSimulationCatalog(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.simGate(c)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		sims, err := d.SimulationRepo.ListPublished(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"simulations": simulationList(sims)})
	}
}

// RecordSimulationProgress is POST
// /api/orgs/:org_slug/simulations/:simulationId/progress: the learner records
// one interaction (opaque state + optional score + optional explicit complete).
// The published simulation's config drives auto-completion by interaction count.
func RecordSimulationProgress(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			State    json.RawMessage `json:"state"`
			Score    *float64        `json:"score"`
			Complete bool            `json:"complete"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		oc, ok := d.simGate(c)
		if !ok {
			return
		}
		sim, ok := d.getOrgSimulation(c, oc.OrgID)
		if !ok {
			return
		}
		// A learner may only interact with a published simulation; drafts are
		// author-only. (Teachers/owners still can't record — this is a learner
		// action; they use the report view.)
		if !sim.IsPublished {
			c.JSON(http.StatusForbidden, gin.H{"error": "simulation is not published"})
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		// The completion threshold is read from the stored (validated) config.
		var cfg simulations.Config
		if len(sim.Config) > 0 {
			_ = json.Unmarshal(sim.Config, &cfg)
		}
		progress, err := d.SimulationProgress.Record(ctx, tx, models.RecordProgress{
			OrgID:                  oc.OrgID,
			SimulationID:           sim.ID,
			LearnerID:              ac.UserID,
			State:                  req.State,
			Score:                  req.Score,
			Complete:               req.Complete,
			CompletionInteractions: cfg.CompletionInteractions,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, simulationProgressResponse(progress))
	}
}

// GetMySimulationProgress is GET
// /api/orgs/:org_slug/simulations/:simulationId/progress: the caller's own
// progress on one simulation.
func GetMySimulationProgress(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.simGate(c)
		if !ok {
			return
		}
		sim, ok := d.getOrgSimulation(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		progress, err := d.SimulationProgress.GetForLearner(c.Request.Context(), tx, sim.ID, ac.UserID)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusOK, gin.H{"progress": nil})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"progress": simulationProgressResponse(progress)})
	}
}

// ListMySimulationProgress is GET /api/orgs/:org_slug/simulations/progress: the
// caller's progress across every simulation.
func ListMySimulationProgress(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := d.simGate(c); !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		progress, err := d.SimulationProgress.ListForLearner(c.Request.Context(), tx, ac.UserID, queryLimit(c))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"progress": simulationProgressList(progress)})
	}
}

// --- Settings (owner) ------------------------------------------------------

// GetSimulationSettings is GET /api/orgs/:org_slug/simulations/settings (owner).
func GetSimulationSettings(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)
		enabled, err := d.Orgs.GetSimulationsEnabled(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"enabled":                      enabled,
			"platform_simulations_enabled": d.Config.Simulations.Enabled,
		})
	}
}

// UpdateSimulationSettings is PATCH /api/orgs/:org_slug/simulations/settings (owner).
func UpdateSimulationSettings(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)
		if err := d.Orgs.SetSimulationsEnabled(c.Request.Context(), tx, oc.OrgID, req.Enabled); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		d.auditSim(c, oc.OrgID, ac.UserID, "simulation.settings_updated", "organization", oc.OrgID)
		c.JSON(http.StatusOK, gin.H{"enabled": req.Enabled})
	}
}

// --- Presentation + helpers ------------------------------------------------

func simulationResponse(s *models.Simulation) gin.H {
	return gin.H{
		"id": s.ID, "slug": s.Slug, "title": s.Title, "description": s.Description,
		"kind": s.Kind, "course_id": s.CourseID, "lesson_id": s.LessonID,
		"is_published": s.IsPublished, "created_at": s.CreatedAt, "updated_at": s.UpdatedAt,
	}
}

// simulationResponseFull includes the spec/config JSON for the authoring/render
// views.
func simulationResponseFull(s *models.Simulation) gin.H {
	r := simulationResponse(s)
	r["spec"] = s.Spec
	r["config"] = s.Config
	return r
}

func simulationList(sims []*models.Simulation) []gin.H {
	out := make([]gin.H, len(sims))
	for i, s := range sims {
		out[i] = simulationResponse(s)
	}
	return out
}

func simulationProgressResponse(p *models.SimulationProgress) gin.H {
	return gin.H{
		"id": p.ID, "simulation_id": p.SimulationID, "learner_id": p.LearnerID,
		"state": p.State, "interaction_count": p.InteractionCount, "last_score": p.LastScore,
		"is_complete": p.IsComplete, "started_at": p.StartedAt, "updated_at": p.UpdatedAt,
		"completed_at": p.CompletedAt,
	}
}

func simulationProgressList(ps []*models.SimulationProgress) []gin.H {
	out := make([]gin.H, len(ps))
	for i, p := range ps {
		out[i] = simulationProgressResponse(p)
	}
	return out
}

// simReason strips the "simulations: " prefix from a validation sentinel error
// for a cleaner client-facing message.
func simReason(err error) string {
	return strings.TrimPrefix(err.Error(), "simulations: ")
}

// queryLimit reads an optional ?limit= query param (0 when absent/invalid; the
// repo clamps it).
func queryLimit(c *gin.Context) int {
	n, _ := strconv.Atoi(c.Query("limit"))
	return n
}

// auditSim records a simulations authoring/settings action to the audit trail —
// this module's authoring observability surface (per-run state is the
// simulation_progress record).
func (d *AuthDeps) auditSim(c *gin.Context, orgID, userID, action, resourceType, resourceID string) {
	ctx := c.Request.Context()
	tx, _ := middleware.RequestTxFromGin(c)
	_ = d.Audit.Record(ctx, tx, models.AuditEvent{
		OrgID: &orgID, UserID: &userID, Action: action,
		ResourceType: resourceType, ResourceID: &resourceID,
		IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
	})
}
