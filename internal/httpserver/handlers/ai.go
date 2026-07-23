package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/ai"
	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// This file implements Task 9's AI authoring & tutor HTTP surface. Every
// generation goes through generateGated, which enforces the two
// non-negotiables the plan calls out for advanced modules: the feature is
// gated (platform LMS_AI_ENABLED AND the org's ai_enabled flag) and it is
// metered (a per-org monthly token cap, checked before the call and
// incremented after). Every attempt — success, provider error, or a call
// blocked by the cap — is written to the ai_generations ledger, so cost
// tracking and prompt/version logging are complete and observable.

// aiTutorHistoryLimit bounds how many prior turns of a tutor conversation
// are replayed to the model, keeping token spend (and latency) bounded on
// long conversations.
const aiTutorHistoryLimit = 20

// aiEnabledForOrg reports whether AI generation is switched on for this org:
// the platform-level flag AND the org's own ai_enabled toggle must both be
// true.
func (d *AuthDeps) aiEnabledForOrg(ctx context.Context, tx models.Querier, orgID string) (bool, models.AISettings, error) {
	if !d.Config.AI.Enabled {
		return false, models.AISettings{}, nil
	}
	s, err := d.Orgs.GetAISettings(ctx, tx, orgID)
	if err != nil {
		return false, s, err
	}
	return s.Enabled, s, nil
}

// monthlyLimit resolves the effective token cap for an org: its own override
// if set, else the platform default. A non-positive value means unlimited.
func (d *AuthDeps) monthlyLimit(s models.AISettings) int64 {
	if s.MonthlyTokenLimit != nil {
		return *s.MonthlyTokenLimit
	}
	return d.Config.AI.MonthlyTokenLimit
}

// generateGated runs one AI generation with feature-gating, usage metering,
// and ledger logging. gen performs the actual ai.Service call. courseID may
// be "" for org-scoped generations. On any failure it writes the appropriate
// HTTP response and returns ok=false; on success it returns the ai.Output.
func (d *AuthDeps) generateGated(c *gin.Context, kind ai.Kind, orgID, courseID string, gen func(context.Context) (ai.Output, error)) (ai.Output, bool) {
	ctx := c.Request.Context()
	tx, _ := middleware.RequestTxFromGin(c)
	ac, _ := middleware.AuthContextFromGin(c)

	enabled, settings, err := d.aiEnabledForOrg(ctx, tx, orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return ai.Output{}, false
	}
	if !enabled {
		c.JSON(http.StatusForbidden, gin.H{"error": "AI features are not enabled for this organization"})
		return ai.Output{}, false
	}

	period := models.MonthPeriod(time.Now())
	limit := d.monthlyLimit(settings)
	if limit > 0 {
		usage, err := d.AIUsage.GetForPeriod(ctx, tx, orgID, period)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return ai.Output{}, false
		}
		if usage.TotalTokens() >= limit {
			d.logGeneration(ctx, tx, orgID, ac.UserID, courseID, kind, ai.Output{
				Provider: d.AI.ProviderName(), Model: d.AI.Model(),
			}, models.AIStatusBlockedLimit, "monthly token limit reached")
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "monthly AI usage limit reached"})
			return ai.Output{}, false
		}
	}

	out, err := gen(ctx)
	if err != nil {
		d.logGeneration(ctx, tx, orgID, ac.UserID, courseID, kind, out, models.AIStatusFailed, err.Error())
		c.JSON(http.StatusBadGateway, gin.H{"error": "AI generation failed"})
		return ai.Output{}, false
	}

	d.logGeneration(ctx, tx, orgID, ac.UserID, courseID, kind, out, models.AIStatusSucceeded, "")
	if err := d.AIUsage.AddUsage(ctx, tx, orgID, period, int64(out.InputTokens), int64(out.OutputTokens), out.CostMicros); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return ai.Output{}, false
	}
	return out, true
}

// logGeneration writes one ai_generations ledger row. Best-effort: a ledger
// write failure must not fail the user's request (the generation already
// happened), so the error is swallowed after logging nothing further.
func (d *AuthDeps) logGeneration(ctx context.Context, tx models.Querier, orgID, actorUserID, courseID string, kind ai.Kind, out ai.Output, status, errMsg string) {
	provider, model := out.Provider, out.Model
	if provider == "" {
		provider = d.AI.ProviderName()
	}
	if model == "" {
		model = d.AI.Model()
	}
	promptVersion := out.PromptVersion
	if promptVersion == "" {
		promptVersion = "n/a"
	}
	_, _ = d.AIGenerations.Log(ctx, tx, models.AIGeneration{
		OrgID:         orgID,
		ActorUserID:   &actorUserID,
		CourseID:      &courseID,
		Kind:          string(kind),
		Provider:      provider,
		Model:         model,
		PromptVersion: promptVersion,
		InputTokens:   out.InputTokens,
		OutputTokens:  out.OutputTokens,
		CostMicros:    out.CostMicros,
		Status:        status,
		Error:         errMsg,
	})
}

// --- Authoring endpoints (owner/teacher, course-scoped) --------------------

type aiOutlineRequest struct {
	Topic    string `json:"topic"`
	Audience string `json:"audience"`
	Modules  int    `json:"modules"`
}

// GenerateOutline is POST /api/courses/:courseId/ai/outline.
func GenerateOutline(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req aiOutlineRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Topic == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "topic is required"})
			return
		}
		course, _ := middleware.CourseFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		out, ok := d.generateGated(c, ai.KindOutline, oc.OrgID, course.ID, func(ctx context.Context) (ai.Output, error) {
			return d.AI.Outline(ctx, ai.OutlineInput{Topic: req.Topic, Audience: req.Audience, Modules: req.Modules})
		})
		if !ok {
			return
		}
		c.JSON(http.StatusOK, aiTextResponse(out))
	}
}

type aiLessonRequest struct {
	LessonTitle string `json:"lesson_title"`
	Objectives  string `json:"objectives"`
}

// GenerateLesson is POST /api/courses/:courseId/ai/lesson.
func GenerateLesson(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req aiLessonRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.LessonTitle == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "lesson_title is required"})
			return
		}
		course, _ := middleware.CourseFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		out, ok := d.generateGated(c, ai.KindLesson, oc.OrgID, course.ID, func(ctx context.Context) (ai.Output, error) {
			return d.AI.Lesson(ctx, ai.LessonInput{CourseTitle: course.Title, LessonTitle: req.LessonTitle, Objectives: req.Objectives})
		})
		if !ok {
			return
		}
		c.JSON(http.StatusOK, aiTextResponse(out))
	}
}

type aiQuizRequest struct {
	Topic         string `json:"topic"`
	SourceContent string `json:"source_content"`
	NumQuestions  int    `json:"num_questions"`
}

// quizQuestion is the structured shape the quiz prompt asks the model to
// return. Parsed leniently — if the model's JSON doesn't validate, the raw
// text is returned instead so the author still gets usable output.
type quizQuestion struct {
	Question     string   `json:"question"`
	Options      []string `json:"options"`
	CorrectIndex int      `json:"correct_index"`
}

// GenerateQuiz is POST /api/courses/:courseId/ai/quiz.
func GenerateQuiz(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req aiQuizRequest
		if err := c.ShouldBindJSON(&req); err != nil || (req.Topic == "" && req.SourceContent == "") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "topic or source_content is required"})
			return
		}
		course, _ := middleware.CourseFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		out, ok := d.generateGated(c, ai.KindQuiz, oc.OrgID, course.ID, func(ctx context.Context) (ai.Output, error) {
			return d.AI.Quiz(ctx, ai.QuizInput{Topic: req.Topic, SourceContent: req.SourceContent, NumQuestions: req.NumQuestions})
		})
		if !ok {
			return
		}

		resp := aiTextResponse(out)
		var questions []quizQuestion
		if err := json.Unmarshal([]byte(out.Text), &questions); err == nil && len(questions) > 0 {
			resp["questions"] = questions
		}
		c.JSON(http.StatusOK, resp)
	}
}

// aiTextResponse builds the common response envelope shared by the authoring
// endpoints: the generated text plus the accounting a caller may want to
// surface.
func aiTextResponse(out ai.Output) gin.H {
	return gin.H{
		"text":     out.Text,
		"provider": out.Provider,
		"model":    out.Model,
		"usage": gin.H{
			"input_tokens":  out.InputTokens,
			"output_tokens": out.OutputTokens,
			"cost_micros":   out.CostMicros,
		},
	}
}

// --- Tutor endpoints (enrolled learner, course-scoped) ---------------------

type aiTutorRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// TutorAsk is POST /api/courses/:courseId/ai/tutor. It creates or continues
// a tutor session, persists both turns, and returns the assistant reply.
func TutorAsk(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req aiTutorRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Message == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		course, _ := middleware.CourseFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		// Resolve or create the session, and load prior turns for context.
		var (
			session *models.AITutorSession
			history []ai.Message
			err     error
		)
		if req.SessionID != "" {
			session, err = d.AITutor.GetSession(ctx, tx, req.SessionID)
			if err != nil {
				if errors.Is(err, models.ErrNotFound) {
					c.JSON(http.StatusNotFound, gin.H{"error": "tutor session not found"})
					return
				}
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
			if session.LearnerID != ac.UserID || session.CourseID != course.ID {
				c.JSON(http.StatusNotFound, gin.H{"error": "tutor session not found"})
				return
			}
			history, err = d.loadTutorHistory(ctx, tx, session.ID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		}

		out, ok := d.generateGated(c, ai.KindTutor, oc.OrgID, course.ID, func(ctx context.Context) (ai.Output, error) {
			return d.AI.Tutor(ctx, ai.TutorInput{
				CourseTitle:       course.Title,
				CourseDescription: course.Description,
				History:           history,
				Question:          req.Message,
			})
		})
		if !ok {
			return
		}

		// Persist the exchange only after a successful generation. Create the
		// session lazily so a blocked/failed first message leaves no empty
		// session behind.
		if session == nil {
			session, err = d.AITutor.CreateSession(ctx, tx, oc.OrgID, course.ID, ac.UserID, tutorSessionTitle(req.Message))
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		}
		if _, err := d.AITutor.AppendMessage(ctx, tx, session.ID, oc.OrgID, ac.UserID, "user", req.Message); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if _, err := d.AITutor.AppendMessage(ctx, tx, session.ID, oc.OrgID, ac.UserID, "assistant", out.Text); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"session_id": session.ID,
			"reply":      out.Text,
		})
	}
}

// loadTutorHistory returns the last aiTutorHistoryLimit turns of a session
// as ai.Messages, oldest first.
func (d *AuthDeps) loadTutorHistory(ctx context.Context, tx models.Querier, sessionID string) ([]ai.Message, error) {
	msgs, err := d.AITutor.ListMessages(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(msgs) > aiTutorHistoryLimit {
		msgs = msgs[len(msgs)-aiTutorHistoryLimit:]
	}
	out := make([]ai.Message, 0, len(msgs))
	for _, m := range msgs {
		role := ai.RoleUser
		if m.Role == "assistant" {
			role = ai.RoleAssistant
		}
		out = append(out, ai.Message{Role: role, Content: m.Content})
	}
	return out, nil
}

// tutorSessionTitle derives a short session title from the first message.
func tutorSessionTitle(msg string) string {
	r := []rune(msg)
	if len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return msg
}

// ListTutorSessions is GET /api/courses/:courseId/ai/tutor/sessions.
func ListTutorSessions(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		course, _ := middleware.CourseFromGin(c)

		sessions, err := d.AITutor.ListSessionsForLearner(ctx, tx, ac.UserID, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"sessions": sessions})
	}
}

// GetTutorSession is GET /api/courses/:courseId/ai/tutor/sessions/:sessionId.
func GetTutorSession(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		course, _ := middleware.CourseFromGin(c)

		session, err := d.AITutor.GetSession(ctx, tx, c.Param("sessionId"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "tutor session not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if session.LearnerID != ac.UserID || session.CourseID != course.ID {
			c.JSON(http.StatusNotFound, gin.H{"error": "tutor session not found"})
			return
		}
		msgs, err := d.AITutor.ListMessages(ctx, tx, session.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"session": session, "messages": msgs})
	}
}

// --- Org admin: settings + usage dashboard ---------------------------------

// GetAISettings is GET /api/orgs/:org_slug/ai/settings (owner).
func GetAISettings(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		s, err := d.Orgs.GetAISettings(ctx, tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"enabled":              s.Enabled,
			"monthly_token_limit":  s.MonthlyTokenLimit,
			"platform_ai_enabled":  d.Config.AI.Enabled,
			"platform_default_cap": d.Config.AI.MonthlyTokenLimit,
			"provider":             d.AI.ProviderName(),
			"model":                d.AI.Model(),
		})
	}
}

type updateAISettingsRequest struct {
	Enabled           bool   `json:"enabled"`
	MonthlyTokenLimit *int64 `json:"monthly_token_limit"`
}

// UpdateAISettings is PATCH /api/orgs/:org_slug/ai/settings (owner).
func UpdateAISettings(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updateAISettingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if req.MonthlyTokenLimit != nil && *req.MonthlyTokenLimit < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "monthly_token_limit must not be negative"})
			return
		}
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		if err := d.Orgs.SetAISettings(ctx, tx, oc.OrgID, req.Enabled, req.MonthlyTokenLimit); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "org.ai_settings_updated",
			ResourceType: "organization", ResourceID: &oc.OrgID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"enabled": req.Enabled, "monthly_token_limit": req.MonthlyTokenLimit})
	}
}

// AIUsageDashboard is GET /api/orgs/:org_slug/ai/usage (owner/teacher): the
// current month's token/cost totals against the effective cap, plus the
// recent generation ledger.
func AIUsageDashboard(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		tx, _ := middleware.RequestTxFromGin(c)
		oc, _ := middleware.OrgContextFromGin(c)

		settings, err := d.Orgs.GetAISettings(ctx, tx, oc.OrgID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		period := models.MonthPeriod(time.Now())
		usage, err := d.AIUsage.GetForPeriod(ctx, tx, oc.OrgID, period)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		recent, err := d.AIGenerations.RecentByOrg(ctx, tx, oc.OrgID, 50)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"period":              period.Format("2006-01"),
			"monthly_token_limit": d.monthlyLimit(settings),
			"usage": gin.H{
				"input_tokens":  usage.InputTokens,
				"output_tokens": usage.OutputTokens,
				"total_tokens":  usage.TotalTokens(),
				"cost_micros":   usage.CostMicros,
			},
			"recent_generations": recent,
		})
	}
}
