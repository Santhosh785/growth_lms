// Task 6 (commerce-handlers): the learner-facing checkout flow — the
// server-rendered checkout page, the JSON create-order endpoint that
// recomputes every money value server-side, and the read-only
// order-status polling endpoint.
//
// HARD SECURITY RULE (restated per task doc, see also
// commerce_refunds.go and commerce_reports.go): no handler in this file
// may ever create or transition an entitlements row to 'active' as a
// result of a payment. The ONLY entitlement-creating path in this file is
// the free-offer branch of CreateOrder below, because no money moved and
// there is nothing for a webhook to confirm. Every PAID order's
// entitlement is created exclusively by the webhook-driven worker job
// (internal/worker/payments.go), never here — even if a client calls this
// endpoint again, or some other channel claims "payment succeeded".
// internal/payments.Provider.VerifyPaymentSignature exists ONLY to let a
// client-side "verifying your payment..." UI state render optimistically;
// its return value must never be wired into a status/entitlement
// mutation, and this file never calls it for that purpose.
//
// Similarly, d.Config.Razorpay.KeySecret and d.Config.Razorpay.WebhookSecret
// must never appear in any response body, template, or log line produced
// by this file — only d.Config.Razorpay.KeyID (the public key) ever
// reaches the browser.
package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/httpserver/templates"
	"growth-lms/internal/models"
)

// toMinorUnits converts a NUMERIC(12,2) major-unit float64 amount to an
// integer minor-unit amount (paise for INR, cents for USD) — the ONLY
// place in this task's code that produces a minor-units value, and it
// must never leak back out of this conversion into anything persisted or
// returned by these handlers (see package doc comment and
// task-6-commerce-handlers.md's money-representation rule).
func toMinorUnits(major float64) int64 {
	return int64(math.Round(major * 100))
}

func toMajorUnits(minor int64) float64 {
	return float64(minor) / 100
}

// checkoutPricePreview is the server-computed subtotal/discount/tax/total
// breakdown, shared by the checkout page (preview only — for display) and
// CreateOrder (authoritative — the only numbers that matter). Computed
// entirely in integer minor units to avoid float rounding drift, per the
// task doc's step 5 instruction, then converted back to float64 major
// units at the boundary since this task's own wire format/DB columns are
// float64/NUMERIC(12,2) throughout.
type checkoutPricePreview struct {
	SubtotalMinor int64
	DiscountMinor int64
	TaxMinor      int64
	TotalMinor    int64
}

func (p checkoutPricePreview) Subtotal() float64 { return toMajorUnits(p.SubtotalMinor) }
func (p checkoutPricePreview) Discount() float64 { return toMajorUnits(p.DiscountMinor) }
func (p checkoutPricePreview) Tax() float64      { return toMajorUnits(p.TaxMinor) }
func (p checkoutPricePreview) Total() float64    { return toMajorUnits(p.TotalMinor) }

// computePricePreview recomputes subtotal/discount/tax/total for offer,
// applying discountCode (if non-nil) — this is the ONE place both the
// checkout page preview and CreateOrder's authoritative computation go
// through, so they can never drift apart.
func computePricePreview(offer *models.Offer, discountCode *models.DiscountCode) checkoutPricePreview {
	subtotalMinor := toMinorUnits(offer.Price)

	var discountMinor int64
	if discountCode != nil {
		switch discountCode.DiscountType {
		case models.DiscountTypePercent:
			discountMinor = int64(math.Round(float64(subtotalMinor) * discountCode.Value / 100))
		case models.DiscountTypeFixed:
			discountMinor = toMinorUnits(discountCode.Value)
		}
		if discountMinor > subtotalMinor {
			discountMinor = subtotalMinor
		}
	}

	netMinor := subtotalMinor - discountMinor
	// Step 5 of the task doc: "tax = round((subtotal - discount) *
	// tax_rate_percent / 100) using integer minor-unit arithmetic
	// throughout (no floats)" — computed here in minor units directly.
	taxMinor := int64(math.Round(float64(netMinor) * offer.TaxRatePercent / 100))
	totalMinor := netMinor + taxMinor

	return checkoutPricePreview{
		SubtotalMinor: subtotalMinor,
		DiscountMinor: discountMinor,
		TaxMinor:      taxMinor,
		TotalMinor:    totalMinor,
	}
}

// lookupValidDiscountCode resolves a discount code string scoped to
// offer, returning (nil, nil) if code is empty, or an error suitable for
// display if the code doesn't exist / is expired / is exhausted. Does
// NOT increment redemption_count — per DiscountCodeRepo.IncrementRedemption's
// doc comment, that only happens once a payment is confirmed (the
// webhook-driven job), never at checkout-preview or order-creation time,
// so an abandoned/failed order never permanently burns a redemption.
func lookupValidDiscountCode(c *gin.Context, d *AuthDeps, offerID, code string) (*models.DiscountCode, error) {
	if code == "" {
		return nil, nil
	}
	tx, _ := middleware.RequestTxFromGin(c)
	dc, err := d.DiscountCodes.GetByCode(c.Request.Context(), tx, offerID, code)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return nil, errors.New("invalid discount code")
		}
		return nil, err
	}
	if dc.ExpiresAt != nil && dc.ExpiresAt.Before(time.Now()) {
		return nil, errors.New("discount code has expired")
	}
	if dc.MaxRedemptions != nil && dc.RedemptionCount >= *dc.MaxRedemptions {
		return nil, errors.New("discount code has been fully redeemed")
	}
	return dc, nil
}

// cohortWindowError checks a cohort offer's enrollment window/seat cap,
// returning a human-readable error (nil if the offer is currently
// purchasable) — shared by the checkout page (renders it as a banner) and
// CreateOrder (rejects with 400/409). Only applies to cohort offers; nil
// for every other type.
func cohortWindowError(c *gin.Context, d *AuthDeps, offer *models.Offer) (string, error) {
	if offer.Type != models.OfferTypeCohort {
		return "", nil
	}
	now := time.Now()
	if offer.EnrollmentStartsAt != nil && now.Before(*offer.EnrollmentStartsAt) {
		return "enrollment for this cohort has not started yet", nil
	}
	if offer.EnrollmentEndsAt != nil && now.After(*offer.EnrollmentEndsAt) {
		return "enrollment window for this cohort is closed", nil
	}
	if offer.MaxSeats != nil {
		tx, _ := middleware.RequestTxFromGin(c)
		// "Full" is judged by successful (paid or free-terminal) orders
		// against this offer — see OrderRepo.CountByOfferAndStatus's doc
		// comment for why a succeeded order, not an entitlement row, is
		// the unit counted here.
		count, err := d.Orders.CountByOfferAndStatus(c.Request.Context(), tx, offer.ID, models.OrderStatusSucceeded)
		if err != nil {
			return "", err
		}
		if count >= *offer.MaxSeats {
			return "this cohort is full", nil
		}
	}
	return "", nil
}

// resolveInviteTokenForCheckout validates an invitation_only offer's
// invite token (from query string on the checkout page, or request body
// on create-order — see callers), returning the token row if it's
// currently redeemable and (if bound) matches callerEmail. Returns a
// human-readable error otherwise; never mutates the token (MarkUsed only
// happens once the webhook/free-path confirms the order, per
// InviteTokenRepo.MarkUsed's doc comment).
func resolveInviteTokenForCheckout(c *gin.Context, d *AuthDeps, offer *models.Offer, tokenValue, callerEmail string) (*models.InviteToken, string) {
	if offer.Type != models.OfferTypeInvitationOnly {
		return nil, ""
	}
	if tokenValue == "" {
		return nil, "an invite token is required for this offer"
	}
	tx, _ := middleware.RequestTxFromGin(c)
	it, err := d.InviteTokens.GetByToken(c.Request.Context(), tx, tokenValue)
	if err != nil {
		switch {
		case errors.Is(err, models.ErrNotFound):
			return nil, "invite token not found"
		case errors.Is(err, models.ErrInviteTokenUsed):
			return nil, "invite token has already been used"
		case errors.Is(err, models.ErrInviteTokenExpired):
			return nil, "invite token has expired"
		default:
			return nil, "internal error"
		}
	}
	if it.OfferID != offer.ID {
		return nil, "invite token does not match this offer"
	}
	if it.BoundEmail != nil && *it.BoundEmail != callerEmail {
		return nil, "invite token is bound to a different email address"
	}
	return it, ""
}

// checkoutViewOffer is the HTML-page-only offer summary shape, mirroring
// learner_ui.go's learnCourseView pattern of small dedicated view structs
// rather than reusing the JSON API's response shape.
type checkoutViewOffer struct {
	ID       string
	Type     string
	Currency string
}

// CheckoutPage renders GET /api/courses/:courseId/offers/:offerId/checkout:
// a server-computed price preview, the invite-token/cohort-window gate,
// and (only if the offer is currently purchasable) Razorpay's checkout.js
// widget wired to POST .../checkout/order. Expected middleware chain:
// ResolveCourseOrg only — any authenticated org member may load this page
// (not staff-gated), matching CourseLearnPage's precedent.
//
// Blocked states (archived offer, closed cohort window, full cohort,
// missing/invalid invite token) render this SAME page with HTTP 200 and
// an error banner instead of the checkout widget — not a raw 403/409 the
// learner can't see explained — except invitation_only's invite-token
// checks, which respond 403 per the task doc's explicit instruction (a
// missing/invalid token for an invitation_only offer is a hard access
// gate, not a soft "come back later" state like a cohort window).
func CheckoutPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		offer, err := d.Offers.Get(ctx, tx, c.Param("offerId"))
		if err != nil || offer.CourseID != course.ID {
			c.String(http.StatusNotFound, "offer not found")
			return
		}
		if offer.Status == models.OfferStatusArchived {
			c.String(http.StatusNotFound, "offer not found")
			return
		}

		if offer.Type == models.OfferTypeInvitationOnly {
			if _, errMsg := resolveInviteTokenForCheckout(c, d, offer, c.Query("invite_token"), ac.Email); errMsg != "" {
				c.String(http.StatusForbidden, errMsg)
				return
			}
		}

		blockedMessage, err := cohortWindowError(c, d, offer)
		if err != nil {
			c.String(http.StatusInternalServerError, "internal error")
			return
		}

		dc, err := lookupValidDiscountCode(c, d, offer.ID, c.Query("discount_code"))
		var discountError string
		if err != nil {
			// A preview-time invalid discount code is not fatal to loading
			// the page — show the error and preview without the discount,
			// same "still render, explain the blocked/degraded state"
			// philosophy as the cohort banner above. The authoritative
			// create-order call re-validates and rejects outright.
			discountError = err.Error()
			dc = nil
		}
		preview := computePricePreview(offer, dc)

		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.Checkout.Execute(c.Writer, gin.H{
			"Course":         learnCourseView{ID: course.ID, Title: course.Title},
			"Offer":          checkoutViewOffer{ID: offer.ID, Type: offer.Type, Currency: offer.Currency},
			"Subtotal":       preview.Subtotal(),
			"Discount":       preview.Discount(),
			"Tax":            preview.Tax(),
			"Total":          preview.Total(),
			"DiscountError":  discountError,
			"BlockedMessage": blockedMessage,
			"Purchasable":    blockedMessage == "",
			"InviteToken":    c.Query("invite_token"),
			"RazorpayKeyID":  d.Config.Razorpay.KeyID,
		})
	}
}

// createOrderRequest is the ONLY shape CreateOrder accepts client input
// through: discount_code (looked up server-side) and, for
// invitation_only offers, invite_token. Every other field below exists
// SOLELY to detect and reject a tampered request that tries to smuggle a
// client-supplied money/currency value — see the hard security rule
// restated in this file's package doc comment and step 2 of the task
// doc's create-order spec. These fields are never read for computation,
// only checked for "was something non-empty/non-zero sent here".
type createOrderRequest struct {
	DiscountCode *string `json:"discount_code"`
	InviteToken  *string `json:"invite_token"`

	// Forbidden money/currency fields — reject 400 if any is present with
	// a non-empty/non-zero value.
	Price          json.Number `json:"price"`
	Amount         json.Number `json:"amount"`
	Subtotal       json.Number `json:"subtotal"`
	DiscountAmount json.Number `json:"discount_amount"`
	TaxAmount      json.Number `json:"tax_amount"`
	Total          json.Number `json:"total"`
	Currency       string      `json:"currency"`
}

// rejectForbiddenMoneyFields implements step 2 of the task doc's
// create-order spec: reject 400 if the request body contains any
// client-supplied price/currency/subtotal/discount/tax/total field with a
// non-empty/non-zero value. This is defense-in-depth ONLY — the handler
// below never reads these fields for computation either way — but a
// tampered request should be told "no" outright rather than silently
// ignored, per the task doc's explicit instruction.
func rejectForbiddenMoneyFields(req createOrderRequest) error {
	numeric := map[string]json.Number{
		"price": req.Price, "amount": req.Amount, "subtotal": req.Subtotal,
		"discount_amount": req.DiscountAmount, "tax_amount": req.TaxAmount, "total": req.Total,
	}
	for name, n := range numeric {
		if n == "" {
			continue
		}
		v, err := n.Float64()
		if err != nil || v != 0 {
			return fmt.Errorf("%s must not be supplied by the client", name)
		}
	}
	if req.Currency != "" {
		return errors.New("currency must not be supplied by the client")
	}
	return nil
}

// CreateOrder is the authoritative, server-side checkout endpoint.
// Expected route:
// POST /api/courses/:courseId/offers/:offerId/checkout/order, middleware
// chain ResolveCourseOrg only (any authenticated org member — same
// gating as CheckoutPage; the offer/invite-token/cohort checks below are
// this endpoint's own re-validation, never trusting that the client only
// reached here via the checkout page).
//
// See this file's package doc comment for the central invariant: the paid
// path below NEVER creates or activates an entitlement — only the
// free-offer branch does, because no money moved and there is nothing for
// a webhook to confirm.
func CreateOrder(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createOrderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		if err := rejectForbiddenMoneyFields(req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		course, _ := middleware.CourseFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		offer, err := d.Offers.Get(ctx, tx, c.Param("offerId"))
		if err != nil || offer.CourseID != course.ID {
			c.JSON(http.StatusNotFound, gin.H{"error": "offer not found"})
			return
		}
		if offer.Status == models.OfferStatusArchived {
			c.JSON(http.StatusConflict, gin.H{"error": "this offer is no longer available"})
			return
		}

		// Step 1: re-validate everything the checkout page validated.
		var inviteToken *models.InviteToken
		if offer.Type == models.OfferTypeInvitationOnly {
			var tokenValue string
			if req.InviteToken != nil {
				tokenValue = *req.InviteToken
			}
			var errMsg string
			inviteToken, errMsg = resolveInviteTokenForCheckout(c, d, offer, tokenValue, ac.Email)
			if errMsg != "" {
				c.JSON(http.StatusForbidden, gin.H{"error": errMsg})
				return
			}
		}
		if blockedMessage, cErr := cohortWindowError(c, d, offer); cErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		} else if blockedMessage != "" {
			c.JSON(http.StatusConflict, gin.H{"error": blockedMessage})
			return
		}

		// Steps 3-4: subtotal + server-side discount-code lookup (never
		// trust a client-supplied discount amount).
		var discountCodeValue string
		if req.DiscountCode != nil {
			discountCodeValue = *req.DiscountCode
		}
		dc, dcErr := lookupValidDiscountCode(c, d, offer.ID, discountCodeValue)
		if dcErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": dcErr.Error()})
			return
		}

		// Steps 5-6: tax + total, integer minor-unit arithmetic throughout.
		preview := computePricePreview(offer, dc)

		// Step 7: snapshot the current platform commission rate onto this
		// order — later rate changes must never retroactively alter it.
		settings, err := d.PlatformSettings.Get(ctx, tx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		// Commission is computed on the discounted, pre-tax amount (the
		// platform's cut of what the creator actually earns, not of the
		// tax passed through to the relevant tax authority) — an
		// assumption this task makes since neither Task 4's schema nor
		// this task's doc pins down the commission base explicitly.
		netMinor := preview.SubtotalMinor - preview.DiscountMinor
		commissionMinor := int64(math.Round(float64(netMinor) * settings.CommissionPercent / 100))

		var discountCodeID *string
		if dc != nil {
			discountCodeID = &dc.ID
		}

		order, err := d.Orders.Create(ctx, tx, models.Order{
			OrgID:                  course.OrgID,
			OfferID:                offer.ID,
			LearnerID:              ac.UserID,
			Currency:               offer.Currency,
			Subtotal:               preview.Subtotal(),
			DiscountAmount:         preview.Discount(),
			TaxAmount:              preview.Tax(),
			CommissionAmount:       toMajorUnits(commissionMinor),
			Total:                  preview.Total(),
			CommissionRateSnapshot: settings.CommissionPercent,
			DiscountCodeID:         discountCodeID,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		// Step 8: free-offer path. This task's reading of an ambiguity the
		// doc explicitly calls out (see task-6-commerce-handlers.md's
		// step 8): "offer.type == free skips Razorpay entirely; a paid
		// offer that discounts to zero still goes through the normal paid
		// flow ... just with a zero-amount Razorpay order" — i.e. only
		// offer.Type == free takes this branch, never a discounted-to-zero
		// paid/subscription/cohort offer.
		if offer.Type == models.OfferTypeFree {
			if _, statusErr := d.Orders.UpdateStatus(ctx, tx, order.ID, models.OrderStatusSucceeded); statusErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}

			// GAP (flagged per task instructions rather than silently
			// worked around): db/migrations/000006_commerce.up.sql's
			// entitlements_insert RLS policy is
			// `is_org_member(org_id) AND app_current_role() IN ('owner','teacher')`
			// — it has no branch for "the learner themself, for a
			// zero-payment order they just created," even though this
			// free-offer path is explicitly meant to run under the
			// purchasing learner's own request context (RequireRole is
			// NOT applied to this route — see this function's doc
			// comment). Under a real 'learner'-role RLS session this
			// INSERT will be rejected by Postgres. A follow-up migration
			// should add an entitlements_insert branch such as
			// `OR (entitlements.learner_id = app_current_user_id() AND
			// entitlements.order_id IS NOT NULL)`, scoped so a learner can
			// only ever insert a row for themselves. This handler is
			// written as the task doc specifies regardless of that gap.
			entitlement, entErr := d.Entitlements.Create(ctx, tx, models.Entitlement{
				OrgID:     course.OrgID,
				OrderID:   &order.ID,
				LearnerID: ac.UserID,
				CourseID:  course.ID,
				Status:    models.EntitlementStatusActive,
			})
			if entErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}

			if existing, accErr := d.LearnerCourseAccess.Get(ctx, tx, ac.UserID, course.ID); accErr == nil {
				if _, setErr := d.LearnerCourseAccess.SetEntitlementAndStatus(ctx, tx, existing.ID, &entitlement.ID, models.AccessStatusActive); setErr != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
					return
				}
			} else if errors.Is(accErr, models.ErrNotFound) {
				if _, createErr := d.LearnerCourseAccess.Create(ctx, tx, course.OrgID, ac.UserID, course.ID, &entitlement.ID); createErr != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
					return
				}
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}

			if dc != nil {
				if _, incErr := d.DiscountCodes.IncrementRedemption(ctx, tx, dc.ID); incErr != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
					return
				}
			}
			if inviteToken != nil {
				if _, markErr := d.InviteTokens.MarkUsed(ctx, tx, inviteToken.ID, ac.UserID, order.ID); markErr != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
					return
				}
			}

			c.JSON(http.StatusOK, gin.H{
				"free":         true,
				"order_id":     order.ID,
				"redirect_url": "/courses/" + course.ID + "/learn",
			})
			return
		}

		// Step 9: paid path. NEVER creates/activates an entitlement — see
		// this file's package doc comment and step 10 of the task doc.
		totalMinor := toMinorUnits(order.Total)
		razorpayOrderID, err := d.Payments.CreateOrder(ctx, totalMinor, order.Currency, order.ID)
		if err != nil {
			if _, statusErr := d.Orders.UpdateStatus(ctx, tx, order.ID, models.OrderStatusFailed); statusErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}
			c.JSON(http.StatusBadGateway, gin.H{"error": "payment provider error"})
			return
		}

		if _, err := d.Orders.AttachRazorpayOrder(ctx, tx, order.ID, razorpayOrderID, models.OrderStatusPaymentInitiated); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		// Exactly order_id/amount/currency/key_id — nothing else, and
		// NEVER KeySecret/WebhookSecret (see package doc comment).
		c.JSON(http.StatusOK, gin.H{
			"order_id": razorpayOrderID,
			"amount":   order.Total,
			"currency": order.Currency,
			"key_id":   d.Config.Razorpay.KeyID,
		})
	}
}

// OrderStatus is READ-ONLY: it never writes to orders, entitlements, or
// learner_course_access, and it never calls out to Razorpay itself — it
// only reports state the webhook-driven worker job
// (internal/worker/payments.go) has already persisted. This is the
// endpoint most likely to be misused as a shortcut around the webhook
// requirement; do not add any write here, ever, no matter how tempting a
// "just mark it succeeded since checkout.js said so" shortcut looks.
//
// Expected route: GET /api/orders/:orderId/status. Middleware chain:
// Authenticate + WithRequestTx only — deliberately NOT ResolveOrg/
// ResolveCourseOrg, since the URL carries no :org_slug or :courseId to
// resolve org context from (see task doc's literal path). This means the
// orders_select RLS policy's staff branch (is_org_member + app_current_role())
// cannot apply here (app.current_role is never stamped on this request's
// session), so in practice only the purchasing learner
// (orders.learner_id = app_current_user_id(), which needs no org context
// at all) can successfully load their own order through this endpoint. An
// owner/teacher wanting to inspect an arbitrary order should do so via an
// org-scoped admin view (task-9, out of this task's scope), not this
// polling endpoint. Flagging this as an assumption per the task doc's
// instruction to note gaps rather than invent new middleware here.
func OrderStatus(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		ctx := c.Request.Context()

		order, err := d.Orders.Get(ctx, tx, c.Param("orderId"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if order.LearnerID != ac.UserID {
			// Under RLS this branch is unreachable for a genuine
			// cross-learner request (the SELECT itself would already
			// return ErrNotFound) — kept as defense-in-depth for the
			// no-RLS/AdminDB test/dev paths, matching this codebase's
			// everywhere-else philosophy of not relying on RLS alone.
			c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
			return
		}

		resp := gin.H{
			"order_id": order.ID,
			"status":   order.Status,
		}
		if entitlement, entErr := d.Entitlements.GetByOrderID(ctx, tx, order.ID); entErr == nil && entitlement.Status == models.EntitlementStatusActive {
			if offer, offErr := d.Offers.Get(ctx, tx, order.OfferID); offErr == nil {
				resp["redirect_url"] = "/courses/" + offer.CourseID + "/learn"
			}
		} else if entErr != nil && !errors.Is(entErr, models.ErrNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, resp)
	}
}
