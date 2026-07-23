package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/codeexec"
	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// This file implements Task 9's sandboxed code-execution HTTP surface. Every
// run goes through executeGated, which enforces the two non-negotiables the
// plan calls out for advanced modules: the feature is gated (platform
// LMS_CODE_EXEC_ENABLED AND the org's code_exec_enabled flag) and it is
// metered (a per-org daily execution cap, checked before the run and
// incremented after). Every attempt — success, runner error, or a run blocked
// by the cap — is written to the code_submissions ledger, so usage tracking is
// complete and observable.
//
// The actual OS-level sandboxing (CPU/memory/time/network/filesystem limits)
// is the runner backend's job (internal/codeexec); handlers only choose the
// limit envelope and record what happened. With no real runner configured the
// service uses the no-op stub, so this whole surface is safe to enable in dev.

// codeExecEnabledForOrg reports whether code execution is switched on for this
// org: the platform-level flag AND the org's own toggle must both be true.
func (d *AuthDeps) codeExecEnabledForOrg(ctx context.Context, tx models.Querier, orgID string) (bool, models.CodeExecSettings, error) {
	if !d.Config.CodeExec.Enabled {
		return false, models.CodeExecSettings{}, nil
	}
	s, err := d.Orgs.GetCodeExecSettings(ctx, tx, orgID)
	if err != nil {
		return false, s, err
	}
	return s.Enabled, s, nil
}

// dailyLimit resolves the effective daily execution cap for an org: its own
// override if set, else the platform default. A non-positive value means
// unlimited.
func (d *AuthDeps) dailyLimit(s models.CodeExecSettings) int64 {
	if s.DailyLimit != nil {
		return *s.DailyLimit
	}
	return d.Config.CodeExec.DailyLimit
}

// codeExecGate resolves the request's org context and transaction and verifies
// the feature is enabled, writing the appropriate response and returning
// ok=false otherwise. Authoring/settings handlers that don't run code start
// with it.
func (d *AuthDeps) codeExecGate(c *gin.Context) (middleware.OrgContext, bool) {
	oc, _ := middleware.OrgContextFromGin(c)
	tx, _ := middleware.RequestTxFromGin(c)
	enabled, _, err := d.codeExecEnabledForOrg(c.Request.Context(), tx, oc.OrgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return oc, false
	}
	if !enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "code execution is not enabled for this organization"})
		return oc, false
	}
	return oc, true
}

// executeGated runs one code execution with feature-gating, daily-cap
// metering, grading, and ledger logging. exercise may be nil for an ad-hoc
// run. On any failure it writes the appropriate HTTP response and returns
// ok=false; on success it returns the persisted submission (which carries the
// captured output, status, and pass/fail).
func (d *AuthDeps) executeGated(c *gin.Context, orgID string, exercise *models.CodeExercise, req codeexec.Request) (*models.CodeSubmission, bool) {
	ctx := c.Request.Context()
	tx, _ := middleware.RequestTxFromGin(c)
	ac, _ := middleware.AuthContextFromGin(c)

	enabled, settings, err := d.codeExecEnabledForOrg(ctx, tx, orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if !enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "code execution is not enabled for this organization"})
		return nil, false
	}

	var exerciseID string
	if exercise != nil {
		exerciseID = exercise.ID
	}

	// Daily cap: check before running, and record the blocked attempt.
	period := models.DayPeriod(time.Now())
	limit := d.dailyLimit(settings)
	if limit > 0 {
		usage, err := d.CodeExecUsage.GetForPeriod(ctx, tx, orgID, period)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return nil, false
		}
		if usage.ExecutionCount >= limit {
			d.logSubmission(ctx, tx, orgID, ac.UserID, exerciseID, req, codeexec.Output{
				Runner:   d.CodeExec.RunnerName(),
				Language: req.Language,
			}, models.CodeStatusBlockedLimit, false, "daily execution limit reached")
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "daily code execution limit reached"})
			return nil, false
		}
	}

	out, err := d.CodeExec.Execute(ctx, req)
	if err != nil {
		// A validation error (unsupported language / empty source) is the
		// caller's fault → 400; a runner transport failure → 502.
		if errors.Is(err, codeexec.ErrUnsupportedLanguage) || errors.Is(err, codeexec.ErrEmptySource) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return nil, false
		}
		d.logSubmission(ctx, tx, orgID, ac.UserID, exerciseID, req, codeexec.Output{
			Runner:   d.CodeExec.RunnerName(),
			Language: req.Language,
		}, models.CodeStatusError, false, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"error": "code execution failed"})
		return nil, false
	}

	// Grade: a submission passes when the run succeeded and (for an exercise
	// with a reference output) its stdout matches. Whitespace at the edges is
	// ignored so a trailing newline doesn't fail an otherwise-correct answer.
	passed := out.Status == codeexec.StatusSucceeded
	if exercise != nil && strings.TrimSpace(exercise.ExpectedOutput) != "" {
		passed = passed && normalizeOutput(out.Stdout) == normalizeOutput(exercise.ExpectedOutput)
	}

	sub := d.logSubmission(ctx, tx, orgID, ac.UserID, exerciseID, req, out, string(out.Status), passed, out.Err)
	if sub == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if err := d.CodeExecUsage.AddUsage(ctx, tx, orgID, period, int64(out.DurationMillis)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	return sub, true
}

// logSubmission writes one code_submissions ledger row and returns it (nil on
// failure). For blocked/error paths, out carries only the runner/language.
func (d *AuthDeps) logSubmission(ctx context.Context, tx models.Querier, orgID, learnerID, exerciseID string, req codeexec.Request, out codeexec.Output, status string, passed bool, errMsg string) *models.CodeSubmission {
	runner := out.Runner
	if runner == "" {
		runner = d.CodeExec.RunnerName()
	}
	sub, err := d.CodeSubmissions.Log(ctx, tx, models.CodeSubmission{
		OrgID:          orgID,
		ExerciseID:     &exerciseID,
		LearnerID:      learnerID,
		Language:       string(req.Language),
		Source:         req.Source,
		Stdin:          req.Stdin,
		Stdout:         out.Stdout,
		Stderr:         out.Stderr,
		ExitCode:       out.ExitCode,
		DurationMillis: out.DurationMillis,
		MemoryKB:       out.MemoryKB,
		Runner:         runner,
		Status:         status,
		Passed:         passed,
		Error:          errMsg,
	})
	if err != nil {
		return nil
	}
	return sub
}

// normalizeOutput trims trailing whitespace on each line and the overall edges
// so grading is resilient to incidental whitespace differences.
func normalizeOutput(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// --- Exercises (owner/teacher authoring) -----------------------------------

type codeExerciseRequest struct {
	CourseID       string `json:"course_id"`
	LessonID       string `json:"lesson_id"`
	Slug           string `json:"slug"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Language       string `json:"language"`
	StarterCode    string `json:"starter_code"`
	SolutionCode   string `json:"solution_code"`
	Stdin          string `json:"stdin"`
	ExpectedOutput string `json:"expected_output"`
	CPUMillis      int    `json:"cpu_millis_limit"`
	MemoryBytes    int64  `json:"memory_bytes_limit"`
	WallMillis     int    `json:"wall_time_millis_limit"`
	IsPublished    bool   `json:"is_published"`
}

// applyExerciseDefaults normalizes a new/updated exercise's per-exercise
// resource limits: an unset (<=0) field takes the platform default, and an
// over-large field is capped to the platform maximum — so an author can only
// ever request equal-or-less than the operator allows, and the stored/served
// limits match what the runner will actually enforce at run time (the same
// clamp codeexec.Service applies per run). Keeps the exercise metadata honest.
func (d *AuthDeps) applyExerciseDefaults(e *models.CodeExercise) {
	def := d.CodeExec.DefaultLimits()
	e.CPUMillisLimit = clampLimit(e.CPUMillisLimit, def.CPUMillis)
	e.MemoryBytesLimit = clampLimit64(e.MemoryBytesLimit, def.MemoryBytes)
	e.WallTimeMillisLimit = clampLimit(e.WallTimeMillisLimit, def.WallTimeMillis)
}

// clampLimit maps a requested limit into (0, max]: a non-positive request
// takes max (the platform default), an over-large one is capped to max.
func clampLimit(req, max int) int {
	if req <= 0 || req > max {
		return max
	}
	return req
}

func clampLimit64(req, max int64) int64 {
	if req <= 0 || req > max {
		return max
	}
	return req
}

// CreateCodeExercise is POST /api/orgs/:org_slug/code/exercises.
func CreateCodeExercise(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req codeExerciseRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" || req.Slug == "" || req.Language == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "slug, title and language are required"})
			return
		}
		if !codeexec.SupportedLanguage(req.Language) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported language"})
			return
		}
		oc, ok := d.codeExecGate(c)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		ex := models.CodeExercise{
			OrgID:               oc.OrgID,
			CourseID:            &req.CourseID,
			LessonID:            &req.LessonID,
			Slug:                req.Slug,
			Title:               req.Title,
			Description:         req.Description,
			Language:            strings.ToLower(strings.TrimSpace(req.Language)),
			StarterCode:         req.StarterCode,
			SolutionCode:        req.SolutionCode,
			Stdin:               req.Stdin,
			ExpectedOutput:      req.ExpectedOutput,
			CPUMillisLimit:      req.CPUMillis,
			MemoryBytesLimit:    req.MemoryBytes,
			WallTimeMillisLimit: req.WallMillis,
			IsPublished:         req.IsPublished,
			CreatedBy:           &ac.UserID,
		}
		d.applyExerciseDefaults(&ex)

		created, err := d.CodeExercises.Create(ctx, tx, ex)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create exercise"})
			return
		}
		d.auditCodeExec(c, oc.OrgID, ac.UserID, "code_exec.exercise_created", "code_exercise", created.ID)
		c.JSON(http.StatusCreated, gin.H{"exercise": created})
	}
}

// ListCodeExercises is GET /api/orgs/:org_slug/code/exercises (owner/teacher):
// every exercise, including unpublished ones and their solutions.
func ListCodeExercises(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.codeExecGate(c)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		exercises, err := d.CodeExercises.ListByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"exercises": exercises})
	}
}

// getOrgExercise loads an exercise and verifies it belongs to the request's
// org, 404ing otherwise so an id from another org is indistinguishable from a
// missing one (defense in depth over RLS).
func (d *AuthDeps) getOrgExercise(c *gin.Context, orgID string) (*models.CodeExercise, bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	ex, err := d.CodeExercises.Get(c.Request.Context(), tx, c.Param("exerciseId"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "exercise not found"})
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if ex.OrgID != orgID {
		c.JSON(http.StatusNotFound, gin.H{"error": "exercise not found"})
		return nil, false
	}
	return ex, true
}

// GetCodeExercise is GET /api/orgs/:org_slug/code/exercises/:exerciseId
// (owner/teacher): the full exercise, including solution and reference output.
func GetCodeExercise(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.codeExecGate(c)
		if !ok {
			return
		}
		ex, ok := d.getOrgExercise(c, oc.OrgID)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"exercise": ex})
	}
}

// UpdateCodeExercise is PATCH /api/orgs/:org_slug/code/exercises/:exerciseId.
func UpdateCodeExercise(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req codeExerciseRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" || req.Language == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title and language are required"})
			return
		}
		if !codeexec.SupportedLanguage(req.Language) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported language"})
			return
		}
		oc, ok := d.codeExecGate(c)
		if !ok {
			return
		}
		ex, ok := d.getOrgExercise(c, oc.OrgID)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		ex.CourseID = &req.CourseID
		ex.LessonID = &req.LessonID
		ex.Title = req.Title
		ex.Description = req.Description
		ex.Language = strings.ToLower(strings.TrimSpace(req.Language))
		ex.StarterCode = req.StarterCode
		ex.SolutionCode = req.SolutionCode
		ex.Stdin = req.Stdin
		ex.ExpectedOutput = req.ExpectedOutput
		ex.CPUMillisLimit = req.CPUMillis
		ex.MemoryBytesLimit = req.MemoryBytes
		ex.WallTimeMillisLimit = req.WallMillis
		ex.IsPublished = req.IsPublished
		d.applyExerciseDefaults(ex)

		updated, err := d.CodeExercises.Update(ctx, tx, *ex)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not update exercise"})
			return
		}
		d.auditCodeExec(c, oc.OrgID, ac.UserID, "code_exec.exercise_updated", "code_exercise", updated.ID)
		c.JSON(http.StatusOK, gin.H{"exercise": updated})
	}
}

// SetCodeExercisePublished is POST /api/orgs/:org_slug/code/exercises/:exerciseId/publish.
func SetCodeExercisePublished(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Published bool `json:"published"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		oc, ok := d.codeExecGate(c)
		if !ok {
			return
		}
		ex, ok := d.getOrgExercise(c, oc.OrgID)
		if !ok {
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)

		updated, err := d.CodeExercises.SetPublished(ctx, tx, ex.ID, req.Published)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		action := "code_exec.exercise_published"
		if !req.Published {
			action = "code_exec.exercise_unpublished"
		}
		d.auditCodeExec(c, oc.OrgID, ac.UserID, action, "code_exercise", updated.ID)
		c.JSON(http.StatusOK, gin.H{"exercise": updated})
	}
}

// DeleteCodeExercise is DELETE /api/orgs/:org_slug/code/exercises/:exerciseId.
func DeleteCodeExercise(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.codeExecGate(c)
		if !ok {
			return
		}
		ex, ok := d.getOrgExercise(c, oc.OrgID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		if err := d.CodeExercises.Delete(c.Request.Context(), tx, ex.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		d.auditCodeExec(c, oc.OrgID, ac.UserID, "code_exec.exercise_deleted", "code_exercise", ex.ID)
		c.Status(http.StatusNoContent)
	}
}

// --- Learner: browse published exercises -----------------------------------

// publicExercise is the learner-facing projection of an exercise: everything
// needed to attempt it, but never the solution or the reference output (those
// would give the answer away).
func publicExercise(e *models.CodeExercise) gin.H {
	return gin.H{
		"id":                     e.ID,
		"slug":                   e.Slug,
		"title":                  e.Title,
		"description":            e.Description,
		"language":               e.Language,
		"starter_code":           e.StarterCode,
		"stdin":                  e.Stdin,
		"cpu_millis_limit":       e.CPUMillisLimit,
		"memory_bytes_limit":     e.MemoryBytesLimit,
		"wall_time_millis_limit": e.WallTimeMillisLimit,
	}
}

// ListCodeCatalog is GET /api/orgs/:org_slug/code/catalog (any member): the
// published exercises a learner can attempt, with solutions stripped.
func ListCodeCatalog(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.codeExecGate(c)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		exercises, err := d.CodeExercises.ListByOrg(c.Request.Context(), tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, 0, len(exercises))
		for _, e := range exercises {
			if e.IsPublished {
				out = append(out, publicExercise(e))
			}
		}
		c.JSON(http.StatusOK, gin.H{"exercises": out})
	}
}

// GetCodeCatalogExercise is GET /api/orgs/:org_slug/code/catalog/:exerciseId
// (any member): one published exercise with its solution stripped.
func GetCodeCatalogExercise(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, ok := d.codeExecGate(c)
		if !ok {
			return
		}
		ex, ok := d.getOrgExercise(c, oc.OrgID)
		if !ok {
			return
		}
		if !ex.IsPublished {
			c.JSON(http.StatusNotFound, gin.H{"error": "exercise not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"exercise": publicExercise(ex)})
	}
}

// --- Learner: run + submit -------------------------------------------------

type codeRunRequest struct {
	Language string `json:"language"`
	Source   string `json:"source"`
	Stdin    string `json:"stdin"`
}

// RunCode is POST /api/orgs/:org_slug/code/run (any member): an ad-hoc run not
// tied to any exercise. The resource envelope is the platform default (the
// service clamps it to the platform maxima regardless).
func RunCode(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req codeRunRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Source == "" || req.Language == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "language and source are required"})
			return
		}
		oc, _ := middleware.OrgContextFromGin(c)
		sub, ok := d.executeGated(c, oc.OrgID, nil, codeexec.Request{
			Language: codeexec.Language(req.Language),
			Source:   req.Source,
			Stdin:    req.Stdin,
			// zero Limits → service uses the platform defaults.
		})
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"submission": sub})
	}
}

type codeSubmitRequest struct {
	Source string `json:"source"`
}

// SubmitCodeExercise is POST /api/orgs/:org_slug/code/exercises/:exerciseId/submit
// (any member): run the learner's source against a published exercise, graded
// against its reference output. The language, stdin, and resource limits come
// from the exercise, not the request.
func SubmitCodeExercise(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req codeSubmitRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Source == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "source is required"})
			return
		}
		oc, ok := d.codeExecGate(c)
		if !ok {
			return
		}
		ex, ok := d.getOrgExercise(c, oc.OrgID)
		if !ok {
			return
		}
		if !ex.IsPublished {
			c.JSON(http.StatusNotFound, gin.H{"error": "exercise not found"})
			return
		}
		sub, ok := d.executeGated(c, oc.OrgID, ex, codeexec.Request{
			Language: codeexec.Language(ex.Language),
			Source:   req.Source,
			Stdin:    ex.Stdin,
			Limits: codeexec.Limits{
				CPUMillis:      ex.CPUMillisLimit,
				MemoryBytes:    ex.MemoryBytesLimit,
				WallTimeMillis: ex.WallTimeMillisLimit,
			},
		})
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"submission": sub, "passed": sub.Passed})
	}
}

// ListMyCodeSubmissions is GET /api/orgs/:org_slug/code/submissions (any
// member): the caller's own recent submissions, optionally filtered to one
// exercise via ?exercise_id=.
func ListMyCodeSubmissions(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := d.codeExecGate(c); !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		subs, err := d.CodeSubmissions.ListForLearner(c.Request.Context(), tx, ac.UserID, c.Query("exercise_id"), 50)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"submissions": subs})
	}
}

// --- Settings (owner) ------------------------------------------------------

// GetCodeExecSettings is GET /api/orgs/:org_slug/code/settings (owner).
func GetCodeExecSettings(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		s, err := d.Orgs.GetCodeExecSettings(ctx, tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"enabled":                    s.Enabled,
			"daily_limit":                s.DailyLimit,
			"platform_code_exec_enabled": d.Config.CodeExec.Enabled,
			"platform_default_daily":     d.Config.CodeExec.DailyLimit,
			"runner":                     d.CodeExec.RunnerName(),
		})
	}
}

type updateCodeExecSettingsRequest struct {
	Enabled    bool   `json:"enabled"`
	DailyLimit *int64 `json:"daily_limit"`
}

// UpdateCodeExecSettings is PATCH /api/orgs/:org_slug/code/settings (owner).
func UpdateCodeExecSettings(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updateCodeExecSettingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if req.DailyLimit != nil && *req.DailyLimit < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "daily_limit must not be negative"})
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		if err := d.Orgs.SetCodeExecSettings(ctx, tx, oc.OrgID, req.Enabled, req.DailyLimit); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		d.auditCodeExec(c, oc.OrgID, ac.UserID, "code_exec.settings_updated", "organization", oc.OrgID)
		c.JSON(http.StatusOK, gin.H{"enabled": req.Enabled, "daily_limit": req.DailyLimit})
	}
}

// CodeExecUsageDashboard is GET /api/orgs/:org_slug/code/usage (owner/teacher):
// today's execution count against the effective cap, plus the recent
// submission ledger.
func CodeExecUsageDashboard(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		settings, err := d.Orgs.GetCodeExecSettings(ctx, tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		period := models.DayPeriod(time.Now())
		usage, err := d.CodeExecUsage.GetForPeriod(ctx, tx, oc.OrgID, period)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		recent, err := d.CodeSubmissions.RecentByOrg(ctx, tx, oc.OrgID, 50)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"period":             period.Format("2006-01-02"),
			"daily_limit":        d.dailyLimit(settings),
			"runner":             d.CodeExec.RunnerName(),
			"usage":              gin.H{"execution_count": usage.ExecutionCount, "cpu_millis": usage.CPUMillis},
			"recent_submissions": recent,
		})
	}
}

// auditCodeExec records a code-execution authoring/settings action to the
// audit trail — this module's authoring observability surface (per-run
// observability is the code_submissions ledger).
func (d *AuthDeps) auditCodeExec(c *gin.Context, orgID, userID, action, resourceType, resourceID string) {
	ctx := c.Request.Context()
	tx, _ := middleware.RequestTxFromGin(c)
	_ = d.Audit.Record(ctx, tx, models.AuditEvent{
		OrgID: &orgID, UserID: &userID, Action: action,
		ResourceType: resourceType, ResourceID: &resourceID,
		IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
	})
}
