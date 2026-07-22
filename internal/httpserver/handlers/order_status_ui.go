// Task 10 (routes-wiring): the order-status "processing" page — the one
// piece of net-new UI glue this task authors rather than merely wires up.
// It's the HTML page a learner's browser sits on between clicking "Pay" in
// Razorpay's checkout.js widget (templates/checkout.html) and the
// Razorpay webhook (handlers.RazorpayWebhook, task-7) actually landing and
// the webhook-driven worker job (internal/worker/payments.go, task-8)
// confirming the order and creating the entitlement.
//
// Design choice (per plans/task-6-implementation/task-10-routes-wiring.md's
// option (a), the preferred one): commerce_checkout.go's OrderStatus is a
// JSON endpoint (GET /api/orders/:orderId/status) and htmx's hx-get can't
// swap a raw JSON response into the DOM. Rather than touch that handler,
// this file adds a second, HTML-specific pair of handlers:
//   - OrderStatusPage renders the static polling page itself.
//   - OrderStatusFragment is the htmx polling target: it re-runs the same
//     order/entitlement lookup OrderStatus performs and either renders a
//     small "still processing" fragment (htmx keeps polling) or, once the
//     entitlement is active, responds with the HX-Redirect header — htmx
//     follows it as a full client-side navigation automatically.
//
// Both handlers are READ-ONLY, matching OrderStatus's own hard rule: never
// write to orders/entitlements/learner_course_access, and never treat a
// client-side "payment succeeded" signal (Razorpay's checkout.js success
// callback, or anything else reachable from this page) as proof of
// payment. Every entitlement mutation still flows only through the
// webhook handler (task-7) or the admin-grant handler (task-6's
// GrantAccess, itself audit-logged) — see CLAUDE.md's "never from browser
// return URLs" rule.
package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/httpserver/templates"
	"growth-lms/internal/models"
)

// OrderStatusPage renders the "processing your payment" page. It performs
// no order lookup itself — the polling fragment below does that on every
// htmx tick — so the page always renders successfully even before the
// order has settled into any particular state.
//
// Route: GET /orders/:orderId/status (HTML) — deliberately the flat
// /orders/... prefix, not /api/..., since templates/checkout.html's
// Razorpay success handler already navigates here
// (`window.location.href = "/orders/" + data.order_id + "/status"`); this
// task mounts the HTML page at exactly that path rather than editing the
// existing checkout template. Middleware: Authenticate + WithRequestTx
// only, no CSRF — GET-only, nothing here mutates.
func OrderStatusPage(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		_ = templates.OrderStatus.Execute(c.Writer, gin.H{
			"OrderID": c.Param("orderId"),
		})
	}
}

// OrderStatusFragment is the htmx hx-trigger="every 2s" polling target for
// OrderStatusPage. It mirrors OrderStatus's (commerce_checkout.go) own
// read-only order/entitlement lookup rather than calling it directly (that
// handler writes a JSON response to the same c.Writer this handler needs
// for HTML, and duplicating ~10 lines of read-only lookup is simpler than
// content-negotiating one handler across two very different response
// shapes here). Same RLS/ownership caveat as OrderStatus: no
// ResolveOrg/ResolveCourseOrg middleware, so only the purchasing learner
// (orders.learner_id = app_current_user_id()) can load their own order
// through this endpoint.
//
// Route: GET /orders/:orderId/status-fragment. Middleware: Authenticate +
// WithRequestTx only, no CSRF.
func OrderStatusFragment(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		tx, _ := middleware.RequestTxFromGin(c)
		ac, _ := middleware.AuthContextFromGin(c)
		ctx := c.Request.Context()

		order, err := d.Orders.Get(ctx, tx, c.Param("orderId"))
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				c.Header("Content-Type", "text/html; charset=utf-8")
				c.String(http.StatusNotFound, `<div id="order-status">Order not found.</div>`)
				return
			}
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusInternalServerError, orderStatusPollingFragment(order))
			return
		}
		if order.LearnerID != ac.UserID {
			// Unreachable under a genuine RLS session (the SELECT above
			// would already return ErrNotFound) — kept as defense-in-depth
			// for no-RLS/AdminDB paths, matching OrderStatus's own
			// precedent and this codebase's everywhere-else philosophy.
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusNotFound, `<div id="order-status">Order not found.</div>`)
			return
		}

		c.Header("Content-Type", "text/html; charset=utf-8")

		entitlement, entErr := d.Entitlements.GetByOrderID(ctx, tx, order.ID)
		if entErr == nil && entitlement.Status == models.EntitlementStatusActive {
			offer, offErr := d.Offers.Get(ctx, tx, order.OfferID)
			if offErr == nil {
				// htmx follows HX-Redirect as a full client-side
				// navigation, replacing this page entirely — the body is
				// never actually swapped in, so its content doesn't
				// matter.
				c.Header("HX-Redirect", "/courses/"+offer.CourseID+"/learn")
				c.String(http.StatusOK, "")
				return
			}
		} else if entErr != nil && !errors.Is(entErr, models.ErrNotFound) {
			c.String(http.StatusInternalServerError, orderStatusPollingFragment(order))
			return
		}

		if order.Status == models.OrderStatusFailed || order.Status == models.OrderStatusAbandoned {
			c.String(http.StatusOK, `<div id="order-status">Payment did not complete. Please contact support or try checking out again.</div>`)
			return
		}

		// Still pending/payment_initiated/succeeded-but-not-yet-entitled
		// (a brief gap: the worker job persists the order status and
		// entitlement in one transaction, but a caller could theoretically
		// poll between two very close ticks) — keep polling.
		c.String(http.StatusOK, orderStatusPollingFragment(order))
	}
}

// orderStatusPollingFragment re-renders the same polling markup
// OrderStatusPage's initial page body contains, so htmx's hx-swap="outerHTML"
// keeps the polling loop alive across ticks. order may be nil (an internal
// error occurred before it was loaded); the fragment doesn't read any of
// its fields, so this is safe either way.
func orderStatusPollingFragment(order *models.Order) string {
	orderID := ""
	if order != nil {
		orderID = order.ID
	}
	return `<div id="order-status"
     hx-get="/orders/` + orderID + `/status-fragment"
     hx-trigger="every 2s"
     hx-swap="outerHTML">
  Processing your payment&hellip;
</div>`
}
