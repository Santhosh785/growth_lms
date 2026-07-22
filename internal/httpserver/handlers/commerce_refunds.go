// Task 6 (commerce-handlers): refunds (owner-only) and manual
// entitlement grants (owner/teacher, reason required). See
// commerce_checkout.go for the paid-order/entitlement invariant these
// two endpoints intentionally break in the ONE way this codebase allows:
// a manual grant is a deliberate, reason-required, audit-logged staff
// action, not a payment-derived one.
package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

type refundRequest struct {
	Reason *string `json:"reason"`
}

// RefundOrder initiates a refund against an order's successful payment.
// Persists a refunds row with status = 'pending' (NEVER 'succeeded'
// directly) — actual success/failure is written later by the
// webhook-driven worker job (internal/worker/payments.go) once Razorpay's
// refund.processed/refund.failed event is verified. Rejects 409 if the
// order has no successful payment, or already has a pending/succeeded
// refund.
//
// Expected route: POST /api/orgs/:org_slug/orders/:orderId/refund,
// middleware chain ResolveOrg + RequireRole(auth.RoleOwner) — refunds are
// financial actions restricted to the org owner (see
// ownerOnlyCommerceDomainActions in internal/auth/permissions.go), unlike
// offer/discount management above which teachers may also do.
func RefundOrder(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req refundRequest
		// reason is optional, so an empty body is valid — only reject a
		// non-empty body that fails to parse as JSON.
		if c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
				return
			}
		}

		oc, _ := middleware.OrgContextFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		order, err := d.Orders.Get(ctx, tx, c.Param("orderId"))
		if err != nil || order.OrgID != oc.OrgID {
			c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
			return
		}

		payment, err := d.CommercePayments.GetByOrderID(ctx, tx, order.ID)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusConflict, gin.H{"error": "this order has no payment to refund"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if payment.Status != models.PaymentStatusSucceeded || payment.RazorpayPaymentID == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "this order has no successful payment to refund"})
			return
		}

		existingRefunds, err := d.Refunds.GetByPaymentID(ctx, tx, payment.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		for _, rf := range existingRefunds {
			if rf.Status == models.RefundStatusPending || rf.Status == models.RefundStatusSucceeded {
				c.JSON(http.StatusConflict, gin.H{"error": "this payment already has a pending or succeeded refund"})
				return
			}
		}

		// Full refund only for MVP (no partial-refund amount field in the
		// request) — an explicit assumption since neither the task doc nor
		// Task 4/5's schema specify partial refunds.
		amountMinor := toMinorUnits(order.Total)
		razorpayRefundID, err := d.Payments.CreateRefund(ctx, *payment.RazorpayPaymentID, amountMinor)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "payment provider error"})
			return
		}

		refund, err := d.Refunds.Create(ctx, tx, models.Refund{
			OrgID:            oc.OrgID,
			PaymentID:        payment.ID,
			RazorpayRefundID: &razorpayRefundID,
			Amount:           order.Total,
			Reason:           req.Reason,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		oldState := payment.Status
		if err := d.PaymentAuditTrail.Record(ctx, tx, models.PaymentAuditEvent{
			OrgID: oc.OrgID, EventType: "refund_initiated", OrderID: &order.ID, PaymentID: &payment.ID,
			OldState: &oldState, NewState: models.RefundStatusPending, Reason: req.Reason, UserID: &ac.UserID,
		}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &oc.OrgID, UserID: &ac.UserID, Action: "refund.initiate", ResourceType: "refund", ResourceID: &refund.ID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})

		c.JSON(http.StatusCreated, gin.H{
			"id":         refund.ID,
			"payment_id": refund.PaymentID,
			"status":     refund.Status,
			"amount":     refund.Amount,
			"reason":     refund.Reason,
			"created_at": refund.CreatedAt,
		})
	}
}

type grantAccessRequest struct {
	LearnerID string `json:"learner_id" binding:"required"`
	Reason    string `json:"reason"`
}

// GrantAccess is a manual, non-payment entitlement grant: an explicit,
// reason-required, audit-logged staff action, deliberately bypassing the
// published-course-only and prerequisite checks EnrollCourse applies
// (see learner.go), since a deliberate admin grant is allowed to override
// both. This task's choice for identifying the learner is `learner_id` (a
// profiles.id, matching this codebase's user-ID-everywhere convention)
// rather than email — documented per the task doc's "pick one, document
// the choice" instruction.
//
// Expected route: POST /api/courses/:courseId/grant-access, middleware
// chain ResolveCourseOrg + RequireRole(auth.RoleOwner, auth.RoleTeacher).
func GrantAccess(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req grantAccessRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		reason := strings.TrimSpace(req.Reason)
		if reason == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "reason is required"})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		entitlement, err := d.Entitlements.Create(ctx, tx, models.Entitlement{
			OrgID:       course.OrgID,
			LearnerID:   req.LearnerID,
			CourseID:    course.ID,
			Status:      models.EntitlementStatusActive,
			GrantedBy:   &ac.UserID,
			GrantReason: &reason,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		if existing, accErr := d.LearnerCourseAccess.Get(ctx, tx, req.LearnerID, course.ID); accErr == nil {
			if _, setErr := d.LearnerCourseAccess.SetEntitlementAndStatus(ctx, tx, existing.ID, &entitlement.ID, models.AccessStatusActive); setErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		} else if errors.Is(accErr, models.ErrNotFound) {
			if _, createErr := d.LearnerCourseAccess.Create(ctx, tx, course.OrgID, req.LearnerID, course.ID, &entitlement.ID); createErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		_ = d.Audit.Record(ctx, tx, models.AuditEvent{
			OrgID: &course.OrgID, UserID: &ac.UserID, Action: "entitlement.grant", ResourceType: "entitlement", ResourceID: &entitlement.ID,
			Details:   map[string]any{"reason": reason, "learner_id": req.LearnerID},
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		// The one non-payment event that still gets a payment_audit_trail
		// row, per the task doc: a money-adjacent access grant even though
		// no money moved.
		if err := d.PaymentAuditTrail.Record(ctx, tx, models.PaymentAuditEvent{
			OrgID: course.OrgID, EventType: "entitlement_granted", NewState: models.EntitlementStatusActive,
			Reason: &reason, UserID: &ac.UserID,
		}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		c.JSON(http.StatusCreated, gin.H{
			"id":           entitlement.ID,
			"learner_id":   entitlement.LearnerID,
			"course_id":    entitlement.CourseID,
			"status":       entitlement.Status,
			"granted_by":   entitlement.GrantedBy,
			"grant_reason": entitlement.GrantReason,
			"created_at":   entitlement.CreatedAt,
		})
	}
}
