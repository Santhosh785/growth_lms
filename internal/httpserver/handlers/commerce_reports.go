// Task 6 (commerce-handlers): the creator-facing revenue reporting
// endpoint — narrower than the admin dashboard (task-9), scoped to one
// org's own orders via ResolveOrg + RLS, net of platform commission,
// segmented by currency.
package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

// defaultRevenueReportWindow is applied when ?from/?to are both unset —
// "some sane window", per the task doc, chosen as the last 90 days.
const defaultRevenueReportWindow = 90 * 24 * time.Hour

type revenueCourseRow struct {
	CourseID      string  `json:"course_id"`
	GrossTotal    float64 `json:"gross_total"`
	Commission    float64 `json:"commission_total"`
	NetTotal      float64 `json:"net_total"`
	RefundedTotal float64 `json:"refunded_total"`
	OrderCount    int     `json:"order_count"`
}

type revenueOfferTypeRow struct {
	OfferType     string  `json:"offer_type"`
	GrossTotal    float64 `json:"gross_total"`
	Commission    float64 `json:"commission_total"`
	NetTotal      float64 `json:"net_total"`
	RefundedTotal float64 `json:"refunded_total"`
	OrderCount    int     `json:"order_count"`
}

type revenueCurrencyBucket struct {
	GrossTotal    float64                         `json:"gross_total"`
	Commission    float64                         `json:"commission_total"`
	NetTotal      float64                         `json:"net_total"`
	RefundedTotal float64                         `json:"refunded_total"`
	OrderCount    int                             `json:"order_count"`
	ByCourse      map[string]*revenueCourseRow    `json:"by_course"`
	ByOfferType   map[string]*revenueOfferTypeRow `json:"by_offer_type"`
}

// RevenueReport returns per-course/per-offer-type revenue, net of
// platform commission, segmented by currency (a top-level map keyed by
// currency code — chosen over a flat list-with-currency-field shape for
// this task; documented here since the task doc leaves the choice open).
// INR and USD figures are NEVER summed together under any circumstance.
//
// Only counts orders whose status is models.OrderStatusSucceeded (the
// verified-payment-success terminal state Task 4/5's order-state-machine
// defines — see order.go's status-constants doc comment), and nets out
// any succeeded refund against the same order from that order's
// contribution.
//
// Query params: from/to (RFC3339 timestamps; default to the last 90 days
// if both are unset — see defaultRevenueReportWindow), offer_type
// (optional filter), course_id (optional filter).
//
// Expected route: GET /api/orgs/:org_slug/reports/revenue, middleware
// chain ResolveOrg + RequireRole(auth.RoleOwner) — revenue visibility is
// owner-only (see ownerOnlyCommerceDomainActions's "report.revenue.view"
// in internal/auth/permissions.go). RLS on the orders table independently
// scopes ListByOrg to this org's own rows — defense-in-depth, matching
// this codebase's philosophy everywhere else.
func RevenueReport(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		oc, _ := middleware.OrgContextFromGin(c)
		tx, _ := middleware.RequestTxFromGin(c)
		ctx := c.Request.Context()

		to := time.Now()
		from := to.Add(-defaultRevenueReportWindow)
		if v := c.Query("from"); v != "" {
			parsed, err := time.Parse(time.RFC3339, v)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid from timestamp, expected RFC3339"})
				return
			}
			from = parsed
		}
		if v := c.Query("to"); v != "" {
			parsed, err := time.Parse(time.RFC3339, v)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid to timestamp, expected RFC3339"})
				return
			}
			to = parsed
		}
		offerTypeFilter := c.Query("offer_type")
		courseIDFilter := c.Query("course_id")

		orders, err := d.Orders.ListByOrg(ctx, tx, oc.OrgID, from, to)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		offerCache := map[string]*models.Offer{}
		buckets := map[string]*revenueCurrencyBucket{}

		for _, order := range orders {
			if order.Status != models.OrderStatusSucceeded {
				continue
			}

			offer, cached := offerCache[order.OfferID]
			if !cached {
				offer, err = d.Offers.Get(ctx, tx, order.OfferID)
				if err != nil {
					if errors.Is(err, models.ErrNotFound) {
						continue
					}
					c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
					return
				}
				offerCache[order.OfferID] = offer
			}
			if offerTypeFilter != "" && offer.Type != offerTypeFilter {
				continue
			}
			if courseIDFilter != "" && offer.CourseID != courseIDFilter {
				continue
			}

			refundedTotal, err := sumSucceededRefundsForOrder(ctx, tx, d, order.ID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
				return
			}

			bucket, ok := buckets[order.Currency]
			if !ok {
				bucket = &revenueCurrencyBucket{
					ByCourse:    map[string]*revenueCourseRow{},
					ByOfferType: map[string]*revenueOfferTypeRow{},
				}
				buckets[order.Currency] = bucket
			}

			netContribution := order.Total - order.CommissionAmount - refundedTotal

			bucket.GrossTotal += order.Total
			bucket.Commission += order.CommissionAmount
			bucket.RefundedTotal += refundedTotal
			bucket.NetTotal += netContribution
			bucket.OrderCount++

			courseRow, ok := bucket.ByCourse[offer.CourseID]
			if !ok {
				courseRow = &revenueCourseRow{CourseID: offer.CourseID}
				bucket.ByCourse[offer.CourseID] = courseRow
			}
			courseRow.GrossTotal += order.Total
			courseRow.Commission += order.CommissionAmount
			courseRow.RefundedTotal += refundedTotal
			courseRow.NetTotal += netContribution
			courseRow.OrderCount++

			offerTypeRow, ok := bucket.ByOfferType[offer.Type]
			if !ok {
				offerTypeRow = &revenueOfferTypeRow{OfferType: offer.Type}
				bucket.ByOfferType[offer.Type] = offerTypeRow
			}
			offerTypeRow.GrossTotal += order.Total
			offerTypeRow.Commission += order.CommissionAmount
			offerTypeRow.RefundedTotal += refundedTotal
			offerTypeRow.NetTotal += netContribution
			offerTypeRow.OrderCount++
		}

		c.JSON(http.StatusOK, gin.H{
			"from":       from,
			"to":         to,
			"currencies": buckets,
		})
	}
}

// sumSucceededRefundsForOrder looks up the order's payment (if any) and
// sums every refund on it whose status is 'succeeded' — a 'pending'
// refund (see commerce_refunds.go's RefundOrder, which only ever creates
// 'pending' rows) does not yet net out of revenue, only a webhook-
// confirmed succeeded one does.
func sumSucceededRefundsForOrder(ctx context.Context, tx models.Querier, d *AuthDeps, orderID string) (float64, error) {
	payment, err := d.CommercePayments.GetByOrderID(ctx, tx, orderID)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	refunds, err := d.Refunds.GetByPaymentID(ctx, tx, payment.ID)
	if err != nil {
		return 0, err
	}
	var total float64
	for _, rf := range refunds {
		if rf.Status == models.RefundStatusSucceeded {
			total += rf.Amount
		}
	}
	return total, nil
}
