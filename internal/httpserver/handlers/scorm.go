package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
	"growth-lms/internal/scorm"
)

// This file implements Task 9's SCORM 1.2/2004 HTTP surface: teacher authoring
// (import a validated imsmanifest.xml, publish, report) and the learner runtime
// (launch → resume, commit CMI data, finish). Like every advanced module it is
// gated by the two-flag feature switch (platform LMS_SCORM_ENABLED AND the
// org's scorm_enabled) — enforced by scormGate on every handler — and
// tenant-scoped by RLS. Package parsing/validation and CMI element validation
// live in internal/scorm; handlers persist the validated result and fold each
// SCO commit onto the learner-owned scorm_attempts record, which is both the
// resume state and the reporting surface. There is no anonymous surface: a SCO
// always runs inside an authenticated learner's session.

// scormEnabledForOrg reports whether SCORM is switched on for this org: the
// platform-level flag AND the org's own toggle must both be true.
func (d *AuthDeps) scormEnabledForOrg(ctx context.Context, tx models.Querier, orgID string) (bool, error) {
	if !d.Config.Scorm.Enabled {
		return false, nil
	}
	return d.Orgs.GetScormEnabled(ctx, tx, orgID)
}

// scormGate resolves the request's org context and verifies the feature is
// enabled, writing the appropriate response and returning ok=false otherwise.
func (d *AuthDeps) scormGate(c *gin.Context) (middleware.OrgContext, bool) {
	oc, _ := middleware.OrgContextFromGin(c)
	tx, _ := middleware.RequestTxFromGin(c)
	enabled, err := d.scormEnabledForOrg(c.Request.Context(), tx, oc.OrgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return oc, false
	}
	if !enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "SCORM is not enabled for this organization"})
		return oc, false
	}
	return oc, true
}

// getOrgPackage loads a package and verifies it belongs to the request's org,
// 404ing otherwise so an id from another org is indistinguishable from a
// missing one (defense in depth over RLS).
func (d *AuthDeps) getOrgPackage(c *gin.Context, orgID string) (*models.ScormPackage, bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	pkg, err := d.ScormPackages.Get(c.Request.Context(), tx, c.Param("packageId"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "package not found"})
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if pkg.OrgID != orgID {
		c.JSON(http.StatusNotFound, gin.H{"error": "package not found"})
		return nil, false
	}
	return pkg, true
}

// --- Authoring (owner/teacher) ---------------------------------------------

type createScormPackageRequest struct {
	CourseID    string `json:"course_id"`
	LessonID    string `json:"lesson_id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	StoragePath string `json:"storage_path"`
	// ManifestXML is the raw imsmanifest.xml the author uploaded; it is parsed
	// and validated server-side to derive the version, launch href, and TOC.
	ManifestXML string `json:"manifest_xml"`
	IsPublished bool   `json:"is_published"`
}

// CreateScormPackage is POST /api/orgs/:org_slug/scorm/packages. It validates
// the supplied manifest XML — a malformed/undeterminable/launch-less manifest
// is a 400 with the specific reason — and persists the derived metadata.
func CreateScormPackage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createScormPackageRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" || req.Slug == "" || req.ManifestXML == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "slug, title and manifest_xml are required"})
			return
		}
		parsed, err := d.Scorm.ParseManifest([]byte(req.ManifestXML))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid SCORM manifest: " + scormReason(err)})
			return
		}
		oc, ok := d.scormGate(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		manifestJSON, err := json.Marshal(parsed)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		created, err := d.ScormPackages.Create(ctx, tx, models.ScormPackage{
			OrgID:        oc.OrgID,
			CourseID:     &req.CourseID,
			LessonID:     &req.LessonID,
			Slug:         req.Slug,
			Title:        req.Title,
			Description:  req.Description,
			Version:      string(parsed.Version),
			Identifier:   parsed.Identifier,
			LaunchHref:   parsed.LaunchHref,
			StoragePath:  req.StoragePath,
			MasteryScore: parsed.MasteryScore,
			Manifest:     manifestJSON,
			IsPublished:  req.IsPublished,
			CreatedBy:    &ac.UserID,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create package"})
			return
		}
		d.auditScorm(c, oc.OrgID, ac.UserID, "scorm.package_created", "scorm_package", created.ID)
		c.JSON(http.StatusCreated, gin.H{"package": created})
	}
}

// ListScormPackages is GET /api/orgs/:org_slug/scorm/packages (owner/teacher):
// every package, published or not.
func ListScormPackages(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.scormGate(c)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		pkgs, err := d.ScormPackages.ListByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"packages": pkgs})
	}
}

// GetScormPackage is GET /api/orgs/:org_slug/scorm/packages/:packageId
// (owner/teacher): the full package including its manifest.
func GetScormPackage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.scormGate(c)
		if !ok {
			return
		}
		pkg, ok := d.getOrgPackage(c, oc.OrgID)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"package": pkg})
	}
}

type updateScormPackageRequest struct {
	CourseID     string   `json:"course_id"`
	LessonID     string   `json:"lesson_id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	MasteryScore *float64 `json:"mastery_score"`
	IsPublished  bool     `json:"is_published"`
}

// UpdateScormPackage is PATCH /api/orgs/:org_slug/scorm/packages/:packageId. It
// edits metadata only; the version/manifest/launch identity comes from the
// imported file and is replaced only by re-importing.
func UpdateScormPackage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updateScormPackageRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title is required"})
			return
		}
		oc, ok := d.scormGate(c)
		if !ok {
			return
		}
		pkg, ok := d.getOrgPackage(c, oc.OrgID)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		pkg.CourseID = &req.CourseID
		pkg.LessonID = &req.LessonID
		pkg.Title = req.Title
		pkg.Description = req.Description
		pkg.MasteryScore = req.MasteryScore
		pkg.IsPublished = req.IsPublished

		updated, err := d.ScormPackages.Update(ctx, tx, *pkg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update package"})
			return
		}
		d.auditScorm(c, oc.OrgID, ac.UserID, "scorm.package_updated", "scorm_package", updated.ID)
		c.JSON(http.StatusOK, gin.H{"package": updated})
	}
}

// SetScormPackagePublished is POST /api/orgs/:org_slug/scorm/packages/:packageId/publish.
func SetScormPackagePublished(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Published bool `json:"published"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		oc, ok := d.scormGate(c)
		if !ok {
			return
		}
		pkg, ok := d.getOrgPackage(c, oc.OrgID)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		updated, err := d.ScormPackages.SetPublished(ctx, tx, pkg.ID, req.Published)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		action := "scorm.package_published"
		if !req.Published {
			action = "scorm.package_unpublished"
		}
		d.auditScorm(c, oc.OrgID, ac.UserID, action, "scorm_package", updated.ID)
		c.JSON(http.StatusOK, gin.H{"package": updated})
	}
}

// DeleteScormPackage is DELETE /api/orgs/:org_slug/scorm/packages/:packageId.
func DeleteScormPackage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.scormGate(c)
		if !ok {
			return
		}
		pkg, ok := d.getOrgPackage(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		if err := d.ScormPackages.Delete(c.Request.Context(), tx, pkg.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		d.auditScorm(c, oc.OrgID, ac.UserID, "scorm.package_deleted", "scorm_package", pkg.ID)
		c.Status(http.StatusNoContent)
	}
}

// --- Reporting (owner/teacher) ---------------------------------------------

// ScormPackageReport is GET /api/orgs/:org_slug/scorm/packages/:packageId/report
// (owner/teacher): every learner's attempts at the package (RLS grants teachers
// org-wide read).
func ScormPackageReport(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.scormGate(c)
		if !ok {
			return
		}
		pkg, ok := d.getOrgPackage(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		attempts, err := d.ScormAttempts.ListByPackage(c.Request.Context(), tx, pkg.ID, 200)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"package": gin.H{"id": pkg.ID, "title": pkg.Title, "version": pkg.Version}, "attempts": attempts})
	}
}

// --- Learner: catalog ------------------------------------------------------

// publicPackage is the learner-facing projection of a package: everything
// needed to launch and render its table of contents.
func publicPackage(p *models.ScormPackage) gin.H {
	return gin.H{
		"id":          p.ID,
		"slug":        p.Slug,
		"title":       p.Title,
		"description": p.Description,
		"version":     p.Version,
		"launch_href": p.LaunchHref,
		"manifest":    p.Manifest,
	}
}

// ListScormCatalog is GET /api/orgs/:org_slug/scorm/catalog (any member): the
// published packages a learner can launch.
func ListScormCatalog(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.scormGate(c)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		pkgs, err := d.ScormPackages.ListByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, 0, len(pkgs))
		for _, p := range pkgs {
			if p.IsPublished {
				out = append(out, publicPackage(p))
			}
		}
		c.JSON(http.StatusOK, gin.H{"packages": out})
	}
}

// --- Learner: runtime (launch / commit / finish) ---------------------------

// LaunchScormPackage is POST /api/orgs/:org_slug/scorm/packages/:packageId/launch
// (any member): resolves the learner's attempt (resuming an in-progress one or
// starting a fresh one) and returns the launch descriptor plus the current CMI
// state so the browser API adapter can restore a suspended SCO.
func LaunchScormPackage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.scormGate(c)
		if !ok {
			return
		}
		pkg, ok := d.getOrgPackage(c, oc.OrgID)
		if !ok {
			return
		}
		if !pkg.IsPublished {
			c.JSON(http.StatusNotFound, gin.H{"error": "package not found"})
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		attempt, err := d.ScormAttempts.StartOrResume(ctx, tx, oc.OrgID, pkg.ID, ac.UserID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not start attempt"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"package": publicPackage(pkg),
			"attempt": attempt,
		})
	}
}

type scormCommitRequest struct {
	// Elements are the CMI key/value pairs the SCO set this session, e.g.
	// {"cmi.core.lesson_status": "completed", "cmi.core.score.raw": "88"}.
	Elements map[string]string `json:"elements"`
}

// loadOwnAttempt loads an attempt by :attemptId and confirms it belongs to the
// caller and the request's org (RLS already scopes it, but a mismatched id
// 404s indistinguishably from a missing one).
func (d *AuthDeps) loadOwnAttempt(c *gin.Context, orgID, userID string) (*models.ScormAttempt, bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	a, err := d.ScormAttempts.Get(c.Request.Context(), tx, c.Param("attemptId"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "attempt not found"})
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if a.OrgID != orgID || a.LearnerID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "attempt not found"})
		return nil, false
	}
	return a, true
}

// CommitScormRuntime is POST /api/orgs/:org_slug/scorm/attempts/:attemptId/commit
// (owner of the attempt): validates and merges the SCO's committed CMI
// elements, re-summarizes the merged state, and persists it.
func CommitScormRuntime(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req scormCommitRequest
		if err := c.ShouldBindJSON(&req); err != nil || len(req.Elements) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "elements are required"})
			return
		}
		oc, ok := d.scormGate(c)
		if !ok {
			return
		}
		ac, _ := middleware.AuthContextFromGin(c)
		attempt, ok := d.loadOwnAttempt(c, oc.OrgID, ac.UserID)
		if !ok {
			return
		}
		if attempt.IsComplete {
			c.JSON(http.StatusConflict, gin.H{"error": "attempt is already complete"})
			return
		}
		d.applyScormCommit(c, attempt, req.Elements, false)
	}
}

// FinishScormAttempt is POST /api/orgs/:org_slug/scorm/attempts/:attemptId/finish
// (owner of the attempt): the SCO's Terminate. Applies any final elements and
// marks the attempt complete regardless of the reported status.
func FinishScormAttempt(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req scormCommitRequest
		_ = c.ShouldBindJSON(&req) // elements optional on finish
		oc, ok := d.scormGate(c)
		if !ok {
			return
		}
		ac, _ := middleware.AuthContextFromGin(c)
		attempt, ok := d.loadOwnAttempt(c, oc.OrgID, ac.UserID)
		if !ok {
			return
		}
		d.applyScormCommit(c, attempt, req.Elements, true)
	}
}

// applyScormCommit validates the incoming elements against the package version,
// merges them into the attempt's stored CMI map, summarizes the result, and
// persists it. forceComplete marks the attempt done (Terminate) even if the SCO
// reported no terminal status. On success it writes the updated attempt.
func (d *AuthDeps) applyScormCommit(c *gin.Context, attempt *models.ScormAttempt, elements map[string]string, forceComplete bool) {
	version := scorm.Version(pkgVersionOf(c, d, attempt))
	if !version.Valid() {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// Validate each element before mutating anything, so a bad value rejects
	// the whole commit atomically.
	for k, v := range elements {
		if err := d.Scorm.ValidateElement(version, k, v); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": scormReason(err)})
			return
		}
	}

	// Merge onto the attempt's stored element map so partial commits accumulate.
	merged := map[string]string{}
	if len(attempt.CMIData) > 0 {
		if err := json.Unmarshal(attempt.CMIData, &merged); err != nil {
			merged = map[string]string{}
		}
	}
	for k, v := range elements {
		merged[k] = v
	}

	state := d.Scorm.Summarize(version, merged)
	mergedJSON, err := json.Marshal(merged)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// The accumulated total_time is folded from the session-time growth inside
	// Commit's SQL (atomic against concurrent commits); the handler only passes
	// the reported session time.
	ctx := c.Request.Context()
	tx, _ := middleware.RequestTxFromGin(c)
	updated, err := d.ScormAttempts.Commit(ctx, tx, attempt.ID, models.ScormRuntimeUpdate{
		LessonStatus:       state.LessonStatus,
		CompletionStatus:   state.CompletionStatus,
		SuccessStatus:      state.SuccessStatus,
		ScoreRaw:           state.ScoreRaw,
		ScoreMin:           state.ScoreMin,
		ScoreMax:           state.ScoreMax,
		ScoreScaled:        state.ScoreScaled,
		SessionTimeSeconds: state.SessionSeconds,
		Location:           state.Location,
		SuspendData:        state.SuspendData,
		CMIData:            mergedJSON,
		IsComplete:         state.Complete || forceComplete,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not save runtime state"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"attempt": updated})
}

// pkgVersionOf resolves the SCORM version of the attempt's package. The version
// is not on the attempt row, so we read the parent package (RLS lets a member
// read their org's packages).
func pkgVersionOf(c *gin.Context, d *AuthDeps, attempt *models.ScormAttempt) string {
	tx, _ := middleware.RequestTxFromGin(c)
	pkg, err := d.ScormPackages.Get(c.Request.Context(), tx, attempt.PackageID)
	if err != nil {
		return ""
	}
	return pkg.Version
}

// ListMyScormAttempts is GET /api/orgs/:org_slug/scorm/attempts (any member):
// the caller's own recent attempts across packages.
func ListMyScormAttempts(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := d.scormGate(c); !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		attempts, err := d.ScormAttempts.ListForLearner(c.Request.Context(), tx, ac.UserID, 100)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"attempts": attempts})
	}
}

// --- Settings (owner) ------------------------------------------------------

// GetScormSettings is GET /api/orgs/:org_slug/scorm/settings (owner).
func GetScormSettings(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		enabled, err := d.Orgs.GetScormEnabled(ctx, tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"enabled":                enabled,
			"platform_scorm_enabled": d.Config.Scorm.Enabled,
		})
	}
}

// UpdateScormSettings is PATCH /api/orgs/:org_slug/scorm/settings (owner).
func UpdateScormSettings(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		if err := d.Orgs.SetScormEnabled(ctx, tx, oc.OrgID, req.Enabled); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		d.auditScorm(c, oc.OrgID, ac.UserID, "scorm.settings_updated", "organization", oc.OrgID)
		c.JSON(http.StatusOK, gin.H{"enabled": req.Enabled})
	}
}

// scormReason strips the "scorm: " prefix from a package sentinel error for a
// cleaner client-facing message.
func scormReason(err error) string {
	return strings.TrimPrefix(err.Error(), "scorm: ")
}

// auditScorm records a SCORM authoring/settings action to the audit trail —
// this module's authoring observability surface (per-run state is the
// scorm_attempts record).
func (d *AuthDeps) auditScorm(c *gin.Context, orgID, userID, action, resourceType, resourceID string) {
	ctx := c.Request.Context()
	tx, _ := middleware.RequestTxFromGin(c)
	_ = d.Audit.Record(ctx, tx, models.AuditEvent{
		OrgID: &orgID, UserID: &userID, Action: action,
		ResourceType: resourceType, ResourceID: &resourceID,
		IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
	})
}
