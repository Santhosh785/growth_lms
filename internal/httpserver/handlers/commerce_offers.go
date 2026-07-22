// Task 6 (commerce-handlers): offer management, discount codes, and
// invite tokens — the owner/teacher-facing "set up what's for sale" side
// of commerce. See commerce_checkout.go for the learner-facing checkout
// flow that consumes these, commerce_refunds.go for refunds/manual
// grants, and commerce_reports.go for revenue reporting.
package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// offerInCourse loads :offerId and verifies it belongs to the course
// already resolved by ResolveCourseOrg, writing a 404 response and
// returning ok=false otherwise — mirrors learner.go's lessonInCourse,
// same "never trust a client-supplied child id belongs to this parent"
// rule.
func offerInCourse(c *gin.Context, d *AuthDeps, courseID string) (*models.Offer, bool) {
	tx, _ := middleware.RequestTxFromGin(c)
	offer, err := d.Offers.Get(c.Request.Context(), tx, c.Param("offerId"))
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "offer not found"})
			return nil, false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if offer.CourseID != courseID {
		c.JSON(http.StatusNotFound, gin.H{"error": "offer not found"})
		return nil, false
	}
	return offer, true
}

type offerRequest struct {
	Type               string     `json:"type"`
	Price              float64    `json:"price"`
	Currency           string     `json:"currency"`
	TaxRatePercent     float64    `json:"tax_rate_percent"`
	AccessDurationDays *int       `json:"access_duration_days"`
	MaxSeats           *int       `json:"max_seats"`
	EnrollmentStartsAt *time.Time `json:"enrollment_starts_at"`
	EnrollmentEndsAt   *time.Time `json:"enrollment_ends_at"`
}

// validateOfferTypeFields enforces the per-type required/forbidden field
// rules from task-6-commerce-handlers.md's "Offer management" section:
// each type has its own required fields, and setting another type's
// fields to a non-zero/non-nil value is rejected outright (defense
// against a client smuggling e.g. max_seats onto a paid offer). typ is
// the offer's type — the caller (create) takes it from the request body,
// while update takes it from the existing, immutable offer row.
func validateOfferTypeFields(typ string, req offerRequest) error {
	hasSubscriptionFields := req.AccessDurationDays != nil
	hasCohortFields := req.MaxSeats != nil || req.EnrollmentStartsAt != nil || req.EnrollmentEndsAt != nil

	switch typ {
	case models.OfferTypeFree:
		if req.Price != 0 {
			return errors.New("free offers must have price 0")
		}
		if hasSubscriptionFields || hasCohortFields {
			return errors.New("free offers may not set subscription/cohort-specific fields")
		}
	case models.OfferTypePaid:
		if req.Price <= 0 {
			return errors.New("paid offers require a positive price")
		}
		if req.Currency == "" {
			return errors.New("currency is required")
		}
		if hasSubscriptionFields || hasCohortFields {
			return errors.New("paid offers may not set subscription/cohort-specific fields")
		}
	case models.OfferTypeSubscription:
		if req.Price <= 0 {
			return errors.New("subscription offers require a positive price")
		}
		if req.Currency == "" {
			return errors.New("currency is required")
		}
		if req.AccessDurationDays == nil || *req.AccessDurationDays <= 0 {
			return errors.New("subscription offers require a positive access_duration_days")
		}
		if hasCohortFields {
			return errors.New("subscription offers may not set cohort-specific fields")
		}
	case models.OfferTypeCohort:
		if req.Price <= 0 {
			return errors.New("cohort offers require a positive price")
		}
		if req.Currency == "" {
			return errors.New("currency is required")
		}
		if req.EnrollmentStartsAt == nil || req.EnrollmentEndsAt == nil {
			return errors.New("cohort offers require enrollment_starts_at and enrollment_ends_at")
		}
		if !req.EnrollmentEndsAt.After(*req.EnrollmentStartsAt) {
			return errors.New("enrollment_ends_at must be after enrollment_starts_at")
		}
		if req.MaxSeats != nil && *req.MaxSeats <= 0 {
			return errors.New("max_seats must be positive if set")
		}
		if hasSubscriptionFields {
			return errors.New("cohort offers may not set access_duration_days")
		}
	case models.OfferTypeInvitationOnly:
		// "requires no extra pricing beyond price/currency" per the task
		// doc — tax_rate_percent is allowed (it's a common field), but
		// subscription/cohort-specific fields are not.
		if req.Currency == "" {
			return errors.New("currency is required")
		}
		if hasSubscriptionFields || hasCohortFields {
			return errors.New("invitation_only offers may not set subscription/cohort-specific fields")
		}
	default:
		return fmt.Errorf("unknown offer type %q", typ)
	}
	return nil
}

// CreateOffer creates a purchasable/enrollable variant of a course.
// Expected route: POST /api/courses/:courseId/offers, middleware chain
// ResolveCourseOrg + RequireRole(auth.RoleOwner, auth.RoleTeacher) (see
// server.go's authoring group precedent for Task 4/5 routes).
func CreateOffer(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req offerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if req.Type == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "type is required"})
			return
		}
		if err := validateOfferTypeFields(req.Type, req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		offer, err := d.Offers.Create(ctx, tx, models.Offer{
			OrgID:              course.OrgID,
			CourseID:           course.ID,
			Type:               req.Type,
			Price:              req.Price,
			Currency:           req.Currency,
			TaxRatePercent:     req.TaxRatePercent,
			AccessDurationDays: req.AccessDurationDays,
			MaxSeats:           req.MaxSeats,
			EnrollmentStartsAt: req.EnrollmentStartsAt,
			EnrollmentEndsAt:   req.EnrollmentEndsAt,
			Status:             models.OfferStatusActive,
			CreatedBy:          ac.UserID,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &course.OrgID, UserID: &ac.UserID, Action: "offer.create", ResourceType: "offer", ResourceID: &offer.ID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusCreated, offerResponse(offer))
	}
}

// UpdateOffer updates an offer's mutable pricing/availability fields.
// type is immutable after creation (see offer.go's Update doc comment) —
// the request's Type field, if present, is ignored; validation runs
// against the EXISTING offer's type. Expected route:
// PATCH /api/courses/:courseId/offers/:offerId, middleware chain
// ResolveCourseOrg + RequireRole(auth.RoleOwner, auth.RoleTeacher).
func UpdateOffer(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req offerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		existing, ok := offerInCourse(c, d, course.ID)
		if !ok {
			return
		}
		if err := validateOfferTypeFields(existing.Type, req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		offer, err := d.Offers.Update(ctx, tx, existing.ID, models.Offer{
			Price:              req.Price,
			Currency:           req.Currency,
			TaxRatePercent:     req.TaxRatePercent,
			AccessDurationDays: req.AccessDurationDays,
			MaxSeats:           req.MaxSeats,
			EnrollmentStartsAt: req.EnrollmentStartsAt,
			EnrollmentEndsAt:   req.EnrollmentEndsAt,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &course.OrgID, UserID: &ac.UserID, Action: "offer.update", ResourceType: "offer", ResourceID: &offer.ID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, offerResponse(offer))
	}
}

// ArchiveOffer soft-deletes an offer: archived offers are excluded from
// ListOffers by default and reject new checkouts (enforced by
// commerce_checkout.go), but historical orders/entitlements referencing
// them are untouched. Expected route:
// POST /api/courses/:courseId/offers/:offerId/archive, middleware chain
// ResolveCourseOrg + RequireRole(auth.RoleOwner, auth.RoleTeacher).
func ArchiveOffer(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		existing, ok := offerInCourse(c, d, course.ID)
		if !ok {
			return
		}

		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		offer, err := d.Offers.Archive(ctx, tx, existing.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &course.OrgID, UserID: &ac.UserID, Action: "offer.archive", ResourceType: "offer", ResourceID: &offer.ID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, offerResponse(offer))
	}
}

// ListOffers lists every ACTIVE offer for a course by default (archived
// offers are excluded, per ArchiveOffer's doc comment) — pass
// ?include_archived=true to see archived rows too, for the owner/teacher
// admin view. Expected route: GET /api/courses/:courseId/offers,
// middleware chain ResolveCourseOrg + RequireRole(auth.RoleOwner,
// auth.RoleTeacher).
func ListOffers(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		offers, err := d.Offers.ListByCourse(ctx, tx, course.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		includeArchived := c.Query("include_archived") == "true"
		out := make([]gin.H, 0, len(offers))
		for _, o := range offers {
			if !includeArchived && o.Status == models.OfferStatusArchived {
				continue
			}
			out = append(out, offerResponse(o))
		}
		c.JSON(http.StatusOK, gin.H{"offers": out})
	}
}

func offerResponse(o *models.Offer) gin.H {
	return gin.H{
		"id":                   o.ID,
		"org_id":               o.OrgID,
		"course_id":            o.CourseID,
		"type":                 o.Type,
		"price":                o.Price,
		"currency":             o.Currency,
		"tax_rate_percent":     o.TaxRatePercent,
		"access_duration_days": o.AccessDurationDays,
		"max_seats":            o.MaxSeats,
		"enrollment_starts_at": o.EnrollmentStartsAt,
		"enrollment_ends_at":   o.EnrollmentEndsAt,
		"status":               o.Status,
		"created_at":           o.CreatedAt,
		"updated_at":           o.UpdatedAt,
	}
}

// ---- Discount codes ----

type discountCodeRequest struct {
	Code string `json:"code" binding:"required"`
	// DiscountType is "percent" or "fixed" (models.DiscountTypePercent/
	// DiscountTypeFixed) — matching db/migrations/000006_commerce.up.sql's
	// actual CHECK constraint, not the task doc's illustrative
	// "percentage"/"fixed_amount" naming (adjusted per this task's own
	// instructions to follow what Task 4 actually shipped).
	DiscountType   string     `json:"discount_type" binding:"required"`
	DiscountValue  float64    `json:"discount_value" binding:"required"`
	ExpiresAt      *time.Time `json:"expires_at"`
	MaxRedemptions *int       `json:"max_redemptions"`
}

func validateDiscountRequest(req discountCodeRequest) error {
	switch req.DiscountType {
	case models.DiscountTypePercent:
		if req.DiscountValue <= 0 || req.DiscountValue > 100 {
			return errors.New("percent discount_value must be between 0 and 100")
		}
	case models.DiscountTypeFixed:
		if req.DiscountValue <= 0 {
			return errors.New("fixed discount_value must be positive")
		}
	default:
		return fmt.Errorf("discount_type must be %q or %q", models.DiscountTypePercent, models.DiscountTypeFixed)
	}
	if req.MaxRedemptions != nil && *req.MaxRedemptions <= 0 {
		return errors.New("max_redemptions must be positive if set")
	}
	return nil
}

// CreateDiscountCode creates a discount code scoped to one offer (code is
// unique per offer, not globally — see DiscountCodeRepo.GetByCode's doc
// comment). Expected route:
// POST /api/courses/:courseId/offers/:offerId/discounts, middleware
// chain ResolveCourseOrg + RequireRole(auth.RoleOwner, auth.RoleTeacher).
func CreateDiscountCode(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req discountCodeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if err := validateDiscountRequest(req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		offer, ok := offerInCourse(c, d, course.ID)
		if !ok {
			return
		}

		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		dc, err := d.DiscountCodes.Create(ctx, tx, models.DiscountCode{
			OrgID:          course.OrgID,
			OfferID:        offer.ID,
			Code:           req.Code,
			DiscountType:   req.DiscountType,
			Value:          req.DiscountValue,
			ExpiresAt:      req.ExpiresAt,
			MaxRedemptions: req.MaxRedemptions,
			CreatedBy:      ac.UserID,
		})
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "a discount code with that code already exists for this offer"})
			return
		}

		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &course.OrgID, UserID: &ac.UserID, Action: "discount.create", ResourceType: "discount_code", ResourceID: &dc.ID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusCreated, discountCodeResponse(dc))
	}
}

// ListDiscountCodes lists every discount code for an offer. Expected
// route: GET /api/courses/:courseId/offers/:offerId/discounts, middleware
// chain ResolveCourseOrg + RequireRole(auth.RoleOwner, auth.RoleTeacher).
func ListDiscountCodes(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		offer, ok := offerInCourse(c, d, course.ID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		codes, err := d.DiscountCodes.ListByOffer(ctx, tx, offer.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out := make([]gin.H, len(codes))
		for i, dc := range codes {
			out[i] = discountCodeResponse(dc)
		}
		c.JSON(http.StatusOK, gin.H{"discount_codes": out})
	}
}

// DeactivateDiscountCode stops future redemptions of a code (does not
// affect orders that already redeemed it). Expected route:
// POST /api/courses/:courseId/offers/:offerId/discounts/:discountId/deactivate,
// middleware chain ResolveCourseOrg + RequireRole(auth.RoleOwner,
// auth.RoleTeacher).
func DeactivateDiscountCode(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		offer, ok := offerInCourse(c, d, course.ID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		ctx := c.Request.Context()

		existing, err := d.DiscountCodes.Get(ctx, tx, c.Param("discountId"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "discount code not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if existing.OfferID != offer.ID {
			c.JSON(http.StatusNotFound, gin.H{"error": "discount code not found"})
			return
		}

		dc, err := d.DiscountCodes.Deactivate(ctx, tx, existing.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &course.OrgID, UserID: &ac.UserID, Action: "discount.archive", ResourceType: "discount_code", ResourceID: &dc.ID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, discountCodeResponse(dc))
	}
}

func discountCodeResponse(dc *models.DiscountCode) gin.H {
	return gin.H{
		"id":               dc.ID,
		"org_id":           dc.OrgID,
		"offer_id":         dc.OfferID,
		"code":             dc.Code,
		"discount_type":    dc.DiscountType,
		"discount_value":   dc.Value,
		"expires_at":       dc.ExpiresAt,
		"max_redemptions":  dc.MaxRedemptions,
		"redemption_count": dc.RedemptionCount,
		"created_at":       dc.CreatedAt,
	}
}

// ---- Invite tokens ----

type createInviteTokenRequest struct {
	Email     *string    `json:"email"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// generateInviteTokenValue returns a random, unguessable token string.
// uuid.NewString() (version-4 random UUID, 122 bits of entropy) is reused
// here rather than introducing a new random-token dependency, matching
// how completion.go/media.go generate their own opaque IDs elsewhere in
// this codebase.
func generateInviteTokenValue() string {
	return "invite_" + uuid.NewString()
}

// CreateInviteToken generates a single-use invite token for an
// invitation_only offer. Rejects with 400 if the offer's type is not
// invitation_only. The raw token is returned ONLY in this response, never
// again (see ListInviteTokens) — the caller (owner/teacher) must copy it
// out now. Expected route:
// POST /api/courses/:courseId/offers/:offerId/invite-tokens, middleware
// chain ResolveCourseOrg + RequireRole(auth.RoleOwner, auth.RoleTeacher).
func CreateInviteToken(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createInviteTokenRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		offer, ok := offerInCourse(c, d, course.ID)
		if !ok {
			return
		}
		if offer.Type != models.OfferTypeInvitationOnly {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invite tokens can only be created for invitation_only offers"})
			return
		}

		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		token := generateInviteTokenValue()
		it, err := d.InviteTokens.Create(ctx, tx, course.OrgID, offer.ID, ac.UserID, token, req.Email, req.ExpiresAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &course.OrgID, UserID: &ac.UserID, Action: "invitetoken.create", ResourceType: "commerce_invite_token", ResourceID: &it.ID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		// The one and only time the raw token is returned — see doc
		// comment above and ListInviteTokens' redaction.
		c.JSON(http.StatusCreated, gin.H{
			"id":          it.ID,
			"offer_id":    it.OfferID,
			"token":       it.Token,
			"bound_email": it.BoundEmail,
			"expires_at":  it.ExpiresAt,
			"created_at":  it.CreatedAt,
		})
	}
}

// ListInviteTokens lists every invite token issued for an offer, with a
// derived status (used/expired/outstanding) but WITHOUT the raw token
// value — matching the "shown once at creation" convention documented on
// CreateInviteToken and InviteTokenRepo. Expected route:
// GET /api/courses/:courseId/offers/:offerId/invite-tokens, middleware
// chain ResolveCourseOrg + RequireRole(auth.RoleOwner, auth.RoleTeacher).
func ListInviteTokens(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		offer, ok := offerInCourse(c, d, course.ID)
		if !ok {
			return
		}
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		tokens, err := d.InviteTokens.ListByOffer(ctx, tx, offer.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		now := time.Now()
		out := make([]gin.H, len(tokens))
		for i, it := range tokens {
			status := "outstanding"
			if it.UsedAt != nil {
				status = "used"
			} else if it.ExpiresAt != nil && it.ExpiresAt.Before(now) {
				status = "expired"
			}
			out[i] = gin.H{
				"id":          it.ID,
				"offer_id":    it.OfferID,
				"bound_email": it.BoundEmail,
				"status":      status,
				"used_at":     it.UsedAt,
				"expires_at":  it.ExpiresAt,
				"created_at":  it.CreatedAt,
				// Deliberately no "token" field — see CreateInviteToken's
				// doc comment; the raw value is never re-exposed.
			}
		}
		c.JSON(http.StatusOK, gin.H{"invite_tokens": out})
	}
}
