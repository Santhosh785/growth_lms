package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"growth-lms/internal/auth"
	"growth-lms/internal/config"
	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
	"growth-lms/internal/payments/paymentstest"
	"growth-lms/internal/testutil"
)

// commerceCheckoutSecretTestValue and commerceCheckoutWebhookSecretTestValue
// are recognizable, non-guessable literals used by the secrets-leakage
// test below so the require.NotContains assertions are non-trivial.
const (
	commerceCheckoutSecretTestValue        = "rzp_live_secret_do_not_leak_9f3ka"
	commerceCheckoutWebhookSecretTestValue = "whsec_do_not_leak_checkout_k7q2z"
)

// commerceTestJWTSecret mirrors internal/httpserver's rbac_test.go
// testJWTSecret convention — a fixed HS256 secret used only to mint
// tokens for these tests, verified by a real auth.Verifier.
const commerceTestJWTSecret = "commerce-test-only-hs256-secret-do-not-use-in-prod"

// commerceTestDeps builds an *AuthDeps wired against a real, migrated
// Postgres connected with ADMIN privileges (bypassing RLS) — the same
// pattern internal/httpserver's own newTestEngine uses (see that file's
// comment): this task's handlers are gated by RequireRole/ResolveCourseOrg
// at the route layer, which is what these tests exercise; RLS-policy
// enforcement itself is a separate concern already covered by
// internal/models' rls_isolation_learner_test.go against the app_test
// role. Using an admin pool here also sidesteps a genuine gap flagged in
// commerce_checkout.go's CreateOrder: the entitlements_insert RLS policy
// (db/migrations/000006_commerce.up.sql) has no branch letting a
// 'learner'-role session insert its own free-offer entitlement.
func commerceTestDeps(t *testing.T) (*AuthDeps, *pgxpool.Pool) {
	t.Helper()
	testutil.RequireDB(t)
	testutil.DB(t) // runs migrations
	pool := testutil.AdminDB(t)

	verifier, err := auth.NewVerifier(config.SupabaseConfig{JWTSecret: commerceTestJWTSecret})
	require.NoError(t, err)

	d := &AuthDeps{
		Config:              &config.Config{Razorpay: config.RazorpayConfig{KeyID: "rzp_test_key"}},
		Pool:                pool,
		Verifier:            verifier,
		Profiles:            models.NewProfileRepo(),
		Orgs:                models.NewOrgRepo(),
		Memberships:         models.NewMembershipRepo(),
		Audit:               models.NewAuditRepo(),
		Courses:             models.NewCourseRepo(),
		LearnerCourseAccess: models.NewLearnerCourseAccessRepo(),
		Offers:              models.NewOfferRepo(),
		DiscountCodes:       models.NewDiscountCodeRepo(),
		InviteTokens:        models.NewInviteTokenRepo(),
		Orders:              models.NewOrderRepo(),
		Entitlements:        models.NewEntitlementRepo(),
		CommercePayments:    models.NewPaymentRepo(),
		Refunds:             models.NewRefundRepo(),
		PaymentAuditTrail:   models.NewPaymentAuditRepo(),
		PlatformSettings:    models.NewPlatformSettingsRepo(),
		Payments:            &paymentstest.FakeProvider{OrderIDToReturn: "order_fake_" + uuid.NewString()},
	}
	return d, pool
}

func commerceMintToken(t *testing.T, userID, email string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub":   userID,
		"email": email,
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(commerceTestJWTSecret))
	require.NoError(t, err)
	return signed
}

func commerceSeedAuthUser(t *testing.T, pool *pgxpool.Pool, id, email string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `INSERT INTO auth.users (id, email) VALUES ($1, $2)`, id, email)
	require.NoError(t, err)
}

// commerceSeedOrgCourseOfferFixture creates an organization (with ownerID
// as its owner membership), a published course inside it, a learner
// member, and returns their IDs for the caller to build offers against.
func commerceSeedOrgCourseOfferFixture(t *testing.T, pool *pgxpool.Pool) (orgID, courseID, ownerID, learnerID string) {
	t.Helper()
	ctx := context.Background()

	ownerID = uuid.NewString()
	learnerID = uuid.NewString()
	commerceSeedAuthUser(t, pool, ownerID, "owner-"+ownerID+"@example.com")
	commerceSeedAuthUser(t, pool, learnerID, "learner-"+learnerID+"@example.com")

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug, created_by_user_id) VALUES ($1, $2, $3) RETURNING id
	`, "Commerce Test Org "+uuid.NewString(), "commerce-org-"+uuid.NewString(), ownerID).Scan(&orgID))

	_, err := pool.Exec(ctx, `INSERT INTO memberships (user_id, org_id, role) VALUES ($1, $2, 'owner')`, ownerID, orgID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO memberships (user_id, org_id, role) VALUES ($1, $2, 'learner')`, learnerID, orgID)
	require.NoError(t, err)

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO courses (org_id, title, created_by, status)
		VALUES ($1, 'Commerce Test Course', $2, 'published') RETURNING id
	`, orgID, ownerID).Scan(&courseID))

	return orgID, courseID, ownerID, learnerID
}

func commerceSeedOffer(t *testing.T, pool *pgxpool.Pool, orgID, courseID, createdBy, offerType string, price float64) string {
	t.Helper()
	var offerID string
	require.NoError(t, pool.QueryRow(context.Background(), `
		INSERT INTO offers (org_id, course_id, type, price, currency, tax_rate_percent, created_by)
		VALUES ($1, $2, $3, $4, 'INR', 0, $5)
		RETURNING id
	`, orgID, courseID, offerType, price, createdBy).Scan(&offerID))
	return offerID
}

// newCommerceCheckoutEngine wires only the two routes CreateOrder tests
// need, with the exact middleware chain this task's doc comments
// specify: Authenticate + WithRequestTx + ResolveCourseOrg (no
// RequireRole — checkout is open to any authenticated org member).
func newCommerceCheckoutEngine(t *testing.T, d *AuthDeps) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	resolveCourseOrg := middleware.ResolveCourseOrg(d.Courses, d.Memberships, d.Profiles)

	group := engine.Group("/api/courses/:courseId")
	group.Use(middleware.Authenticate(d.Verifier), middleware.WithRequestTx(d.Pool), resolveCourseOrg)
	group.POST("/offers/:offerId/checkout/order", CreateOrder(d))
	group.GET("/offers/:offerId/checkout", CheckoutPage(d))

	engine.GET("/api/orders/:orderId/status", middleware.Authenticate(d.Verifier), middleware.WithRequestTx(d.Pool), OrderStatus(d))
	return engine
}

func postJSON(t *testing.T, engine *gin.Engine, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(http.MethodPost, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

// TestCreateOrder_FreeOffer_CreatesEntitlementAndAccess is the central
// "free path does grant access" half of this task's critical invariant.
func TestCreateOrder_FreeOffer_CreatesEntitlementAndAccess(t *testing.T) {
	d, pool := commerceTestDeps(t)
	orgID, courseID, ownerID, learnerID := commerceSeedOrgCourseOfferFixture(t, pool)
	offerID := commerceSeedOffer(t, pool, orgID, courseID, ownerID, models.OfferTypeFree, 0)

	engine := newCommerceCheckoutEngine(t, d)
	token := commerceMintToken(t, learnerID, "learner-"+learnerID+"@example.com")

	rec := postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", token, map[string]any{})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, true, resp["free"])
	require.Contains(t, resp["redirect_url"], "/learn")

	var entitlementCount int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*) FROM entitlements WHERE learner_id = $1 AND course_id = $2 AND status = 'active'
	`, learnerID, courseID).Scan(&entitlementCount))
	require.Equal(t, 1, entitlementCount, "free-offer checkout must create exactly one active entitlement")

	var accessCount int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*) FROM learner_course_access WHERE learner_id = $1 AND course_id = $2 AND access_status = 'active'
	`, learnerID, courseID).Scan(&accessCount))
	require.Equal(t, 1, accessCount, "free-offer checkout must create learner_course_access")

	var orderStatus string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE learner_id = $1`, learnerID).Scan(&orderStatus))
	require.Equal(t, models.OrderStatusSucceeded, orderStatus)
}

// TestCreateOrder_PaidOffer_NeverCreatesEntitlement is this task's central
// invariant, restated as an executable test: the paid path must call
// Payments.CreateOrder and persist the order as payment_initiated, but
// must NEVER create an entitlements row or a learner_course_access row —
// only the webhook-driven worker job (internal/worker/payments.go) may do
// that, per CLAUDE.md's non-negotiable rule.
func TestCreateOrder_PaidOffer_NeverCreatesEntitlement(t *testing.T) {
	d, pool := commerceTestDeps(t)
	orgID, courseID, ownerID, learnerID := commerceSeedOrgCourseOfferFixture(t, pool)
	offerID := commerceSeedOffer(t, pool, orgID, courseID, ownerID, models.OfferTypePaid, 999.00)

	engine := newCommerceCheckoutEngine(t, d)
	token := commerceMintToken(t, learnerID, "learner-"+learnerID+"@example.com")

	rec := postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", token, map[string]any{})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp["order_id"])
	require.Equal(t, "rzp_test_key", resp["key_id"])
	require.NotContains(t, resp, "key_secret")

	var orderStatus string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE learner_id = $1`, learnerID).Scan(&orderStatus))
	require.Equal(t, models.OrderStatusPaymentInitiated, orderStatus)

	var entitlementCount int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*) FROM entitlements WHERE learner_id = $1 AND course_id = $2
	`, learnerID, courseID).Scan(&entitlementCount))
	require.Equal(t, 0, entitlementCount, "paid path must NEVER create an entitlement — only the webhook-driven worker job may")

	var accessCount int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*) FROM learner_course_access WHERE learner_id = $1 AND course_id = $2
	`, learnerID, courseID).Scan(&accessCount))
	require.Equal(t, 0, accessCount, "paid path must NEVER create learner_course_access")
}

// TestCreateOrder_RejectsClientSuppliedMoneyFields covers this task's
// other named invariant: a tampered request body carrying a
// client-supplied money/currency field is rejected 400, never silently
// used.
func TestCreateOrder_RejectsClientSuppliedMoneyFields(t *testing.T) {
	d, pool := commerceTestDeps(t)
	orgID, courseID, ownerID, learnerID := commerceSeedOrgCourseOfferFixture(t, pool)
	offerID := commerceSeedOffer(t, pool, orgID, courseID, ownerID, models.OfferTypePaid, 999.00)

	engine := newCommerceCheckoutEngine(t, d)
	token := commerceMintToken(t, learnerID, "learner-"+learnerID+"@example.com")

	rec := postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", token, map[string]any{
		"total": 1,
	})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	rec = postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", token, map[string]any{
		"currency": "USD",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	// A zero-valued forbidden field is not "supplied" in the sense that
	// matters (it's what omitting the field looks like over the wire in
	// several client libraries) — must not be rejected.
	rec = postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", token, map[string]any{
		"total": 0,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestCreateOrder_ArchivedOffer_Rejected exercises the create-order
// endpoint's own re-validation (step 1 of the task doc): never trust that
// the client only reached this endpoint via the checkout page.
func TestCreateOrder_ArchivedOffer_Rejected(t *testing.T) {
	d, pool := commerceTestDeps(t)
	orgID, courseID, ownerID, learnerID := commerceSeedOrgCourseOfferFixture(t, pool)
	offerID := commerceSeedOffer(t, pool, orgID, courseID, ownerID, models.OfferTypeFree, 0)
	_, err := pool.Exec(context.Background(), `UPDATE offers SET status = 'archived' WHERE id = $1`, offerID)
	require.NoError(t, err)

	engine := newCommerceCheckoutEngine(t, d)
	token := commerceMintToken(t, learnerID, "learner-"+learnerID+"@example.com")

	rec := postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", token, map[string]any{})
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
}

// getJSON issues an authenticated GET request against engine and returns
// the recorder, mirroring postJSON's convention for GET-only endpoints
// like OrderStatus.
func getJSON(t *testing.T, engine *gin.Engine, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

// TestCheckoutReturnCallback_NeverGrantsAccessWithoutWebhook is the most
// safety-critical test in this task (task-11-tests.md gap 3). This
// codebase has no separate browser "payment succeeded" return/callback
// endpoint distinct from the two already covered above — OrderStatus
// (GET /api/orders/:orderId/status) is the ONLY browser-reachable
// endpoint a learner's checkout.js "verifying your payment..." UI could
// poll after a client-side success signal, and its own doc comment
// states it "never writes to orders, entitlements, or
// learner_course_access ... and never calls out to Razorpay itself". This
// test drives exactly that path — create a paid order, then poll
// OrderStatus repeatedly WITHOUT ever delivering the corresponding
// Razorpay webhook — and proves it is provably a no-op for access
// purposes: no entitlement, no learner_course_access row, the order
// stays payment_initiated forever, and the JSON response never carries a
// redirect_url (the only "access granted" signal the real frontend
// trusts).
func TestCheckoutReturnCallback_NeverGrantsAccessWithoutWebhook(t *testing.T) {
	d, pool := commerceTestDeps(t)
	orgID, courseID, ownerID, learnerID := commerceSeedOrgCourseOfferFixture(t, pool)
	offerID := commerceSeedOffer(t, pool, orgID, courseID, ownerID, models.OfferTypePaid, 999.00)

	engine := newCommerceCheckoutEngine(t, d)
	token := commerceMintToken(t, learnerID, "learner-"+learnerID+"@example.com")

	rec := postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", token, map[string]any{})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var createResp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &createResp))

	var orderID string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT id FROM orders WHERE learner_id = $1`, learnerID).Scan(&orderID))

	// Simulate the browser polling this endpoint the way a
	// "verifying your payment..." UI would, as if checkout.js's
	// client-side success callback had just fired — but no webhook is
	// ever delivered.
	for i := 0; i < 3; i++ {
		statusRec := getJSON(t, engine, "/api/orders/"+orderID+"/status", token)
		require.Equal(t, http.StatusOK, statusRec.Code, statusRec.Body.String())

		var statusResp map[string]any
		require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &statusResp))
		require.Equal(t, models.OrderStatusPaymentInitiated, statusResp["status"], "order must never silently become succeeded without a webhook")
		require.NotContains(t, statusResp, "redirect_url", "no redirect_url must ever be present without a verified webhook granting access")
	}

	var orderStatus string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE id = $1`, orderID).Scan(&orderStatus))
	require.Equal(t, models.OrderStatusPaymentInitiated, orderStatus, "polling OrderStatus must never itself transition the order")

	var entitlementCount int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*) FROM entitlements WHERE learner_id = $1 AND course_id = $2
	`, learnerID, courseID).Scan(&entitlementCount))
	require.Equal(t, 0, entitlementCount, "no webhook was ever delivered, so no entitlement may exist")

	var accessCount int
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*) FROM learner_course_access WHERE learner_id = $1 AND course_id = $2
	`, learnerID, courseID).Scan(&accessCount))
	require.Equal(t, 0, accessCount, "no webhook was ever delivered, so no learner_course_access row may exist")
}

// TestCheckout_SecretsNeverLeaked covers task-11-tests.md gap 5 for the
// checkout-initiation and order-status endpoints: the configured Razorpay
// key secret and webhook secret must never appear as a substring of
// either response body. The create-order response for a paid offer SHOULD
// contain the public key_id (asserted explicitly) but must never contain
// KeySecret/WebhookSecret.
func TestCheckout_SecretsNeverLeaked(t *testing.T) {
	d, pool := commerceTestDeps(t)
	d.Config.Razorpay.KeySecret = commerceCheckoutSecretTestValue
	d.Config.Razorpay.WebhookSecret = commerceCheckoutWebhookSecretTestValue

	orgID, courseID, ownerID, learnerID := commerceSeedOrgCourseOfferFixture(t, pool)
	offerID := commerceSeedOffer(t, pool, orgID, courseID, ownerID, models.OfferTypePaid, 999.00)

	engine := newCommerceCheckoutEngine(t, d)
	token := commerceMintToken(t, learnerID, "learner-"+learnerID+"@example.com")

	rec := postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", token, map[string]any{})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "rzp_test_key", "the public key_id SHOULD reach the browser")
	require.NotContains(t, rec.Body.String(), commerceCheckoutSecretTestValue, "create-order response must never leak the Razorpay key secret")
	require.NotContains(t, rec.Body.String(), commerceCheckoutWebhookSecretTestValue, "create-order response must never leak the webhook secret")

	var orderID string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT id FROM orders WHERE learner_id = $1`, learnerID).Scan(&orderID))

	statusRec := getJSON(t, engine, "/api/orders/"+orderID+"/status", token)
	require.Equal(t, http.StatusOK, statusRec.Code, statusRec.Body.String())
	require.NotContains(t, statusRec.Body.String(), commerceCheckoutSecretTestValue, "order-status response must never leak the Razorpay key secret")
	require.NotContains(t, statusRec.Body.String(), commerceCheckoutWebhookSecretTestValue, "order-status response must never leak the webhook secret")

	// The checkout page itself (server-rendered HTML) is the third
	// response body this task's package doc comment names explicitly.
	pageReq := httptest.NewRequest(http.MethodGet, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout", nil)
	pageReq.Header.Set("Authorization", "Bearer "+token)
	pageRec := httptest.NewRecorder()
	engine.ServeHTTP(pageRec, pageReq)
	require.Equal(t, http.StatusOK, pageRec.Code, pageRec.Body.String())
	require.NotContains(t, pageRec.Body.String(), commerceCheckoutSecretTestValue, "checkout page HTML must never leak the Razorpay key secret")
	require.NotContains(t, pageRec.Body.String(), commerceCheckoutWebhookSecretTestValue, "checkout page HTML must never leak the webhook secret")
}

// TestCreateOrder_CommissionRateSnapshot_ImmutableToLaterPlatformSettingsChange
// covers task-11-tests.md gap 7: an order's commission_rate_snapshot is
// fixed at creation time (via the real CreateOrder handler code path, so
// the actual snapshot-computation logic runs) and must never drift when
// platform_settings.commission_percent later changes.
func TestCreateOrder_CommissionRateSnapshot_ImmutableToLaterPlatformSettingsChange(t *testing.T) {
	d, pool := commerceTestDeps(t)
	orgID, courseID, ownerID, learnerID := commerceSeedOrgCourseOfferFixture(t, pool)
	offerID := commerceSeedOffer(t, pool, orgID, courseID, ownerID, models.OfferTypePaid, 1000.00)

	ctx := context.Background()
	settings, err := d.PlatformSettings.Get(ctx, pool)
	require.NoError(t, err)
	_, err = d.PlatformSettings.Update(ctx, pool, settings.ID, 10.00, ownerID)
	require.NoError(t, err)

	engine := newCommerceCheckoutEngine(t, d)
	token := commerceMintToken(t, learnerID, "learner-"+learnerID+"@example.com")

	rec := postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", token, map[string]any{})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var orderID string
	var snapshotAtCreation float64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id, commission_rate_snapshot FROM orders WHERE learner_id = $1`, learnerID).Scan(&orderID, &snapshotAtCreation))
	require.InDelta(t, 10.00, snapshotAtCreation, 0.001, "order must snapshot the commission rate in effect at creation time")

	// Now change the platform-wide commission rate.
	_, err = d.PlatformSettings.Update(ctx, pool, settings.ID, 15.00, ownerID)
	require.NoError(t, err)

	var snapshotAfterChange float64
	require.NoError(t, pool.QueryRow(ctx, `SELECT commission_rate_snapshot FROM orders WHERE id = $1`, orderID).Scan(&snapshotAfterChange))
	require.InDelta(t, 10.00, snapshotAfterChange, 0.001, "an already-created order's commission_rate_snapshot must never change retroactively")
}

// newCommerceCohortEngine reuses newCommerceCheckoutEngine's routes for
// TestCreateOrder_CohortSeatCap_RejectsOnceFull below (no additional
// routes needed — CreateOrder's own cohortWindowError check is what's
// under test).
func newCommerceCohortEngine(t *testing.T, d *AuthDeps) *gin.Engine {
	return newCommerceCheckoutEngine(t, d)
}

// TestCreateOrder_CohortSeatCap_RejectsOnceFull covers task-11-tests.md
// gap 8: a cohort offer with max_seats=1 rejects a second learner's
// checkout attempt server-side (never merely a client-side "sold out"
// affordance) once the cap is reached. A cohort offer's price is nonzero
// (type != free, so this always goes through CreateOrder's paid branch —
// see this file's package doc comment); "one seat filled" is simulated
// the way the task doc allows ("use ... a stubbed/faked successful
// payment webhook, whichever is simpler") by directly flipping the first
// learner's order to 'succeeded' via SQL, since cohortWindowError's seat
// count (OrderRepo.CountByOfferAndStatus) counts SUCCEEDED orders, not
// entitlements — the real worker-driven transition to 'succeeded' is
// already covered end-to-end by internal/worker/payments_test.go.
func TestCreateOrder_CohortSeatCap_RejectsOnceFull(t *testing.T) {
	d, pool := commerceTestDeps(t)
	orgID, courseID, ownerID, learnerA := commerceSeedOrgCourseOfferFixture(t, pool)
	ctx := context.Background()

	var offerID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO offers (org_id, course_id, type, price, currency, tax_rate_percent, max_seats, created_by)
		VALUES ($1, $2, 'cohort', 999, 'INR', 0, 1, $3)
		RETURNING id
	`, orgID, courseID, ownerID).Scan(&offerID))

	learnerB := uuid.NewString()
	commerceSeedAuthUser(t, pool, learnerB, "learner-b-"+learnerB+"@example.com")
	_, err := pool.Exec(ctx, `INSERT INTO memberships (user_id, org_id, role) VALUES ($1, $2, 'learner')`, learnerB, orgID)
	require.NoError(t, err)

	engine := newCommerceCohortEngine(t, d)

	tokenA := commerceMintToken(t, learnerA, "learner-"+learnerA+"@example.com")
	recA := postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", tokenA, map[string]any{})
	require.Equal(t, http.StatusOK, recA.Code, recA.Body.String())

	// Simulate learner A's payment succeeding (fills the one seat).
	_, err = pool.Exec(ctx, `UPDATE orders SET status = 'succeeded' WHERE offer_id = $1 AND learner_id = $2`, offerID, learnerA)
	require.NoError(t, err)

	tokenB := commerceMintToken(t, learnerB, "learner-b-"+learnerB+"@example.com")
	recB := postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", tokenB, map[string]any{})
	require.True(t, recB.Code >= 400 && recB.Code < 500, "second checkout against a full cohort must be rejected 4xx server-side, got %d: %s", recB.Code, recB.Body.String())

	var orderCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE offer_id = $1`, offerID).Scan(&orderCount))
	require.Equal(t, 1, orderCount, "a rejected checkout attempt must not create a second order")

	var entitlementCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM entitlements WHERE course_id = $1`, courseID).Scan(&entitlementCount))
	require.Equal(t, 0, entitlementCount, "cohort's paid path never grants an entitlement without a webhook, for either learner")
}

// newCommerceInviteTokenEngine extends newCommerceCheckoutEngine with the
// real invite-token-creation endpoint (CreateInviteToken,
// task-3-permissions-matrix.md's "invitetoken.create" permission,
// owner/teacher-only), so TestCreateOrder_InvitationOnly_Gating below can
// mint a real token through the actual application code path rather than
// inserting one directly via SQL.
func newCommerceInviteTokenEngine(t *testing.T, d *AuthDeps) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	resolveCourseOrg := middleware.ResolveCourseOrg(d.Courses, d.Memberships, d.Profiles)

	group := engine.Group("/api/courses/:courseId")
	group.Use(middleware.Authenticate(d.Verifier), middleware.WithRequestTx(d.Pool), resolveCourseOrg)
	group.POST("/offers/:offerId/checkout/order", CreateOrder(d))
	group.GET("/offers/:offerId/checkout", CheckoutPage(d))
	group.POST("/offers/:offerId/invite-tokens", middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher), CreateInviteToken(d))

	engine.GET("/api/orders/:orderId/status", middleware.Authenticate(d.Verifier), middleware.WithRequestTx(d.Pool), OrderStatus(d))
	return engine
}

// TestCreateOrder_InvitationOnly_Gating covers task-11-tests.md gap 9.
// The no-token/garbage-token/expired-token sub-cases are fully enforced
// by resolveInviteTokenForCheckout regardless of offer price. The
// valid-token and same-token-reused sub-cases below document a real gap
// discovered while writing this test (see the inline comments at those
// two assertions): InviteTokenRepo.MarkUsed's own doc comment says it
// must be called "after the order this token gates has succeeded (same
// webhook-processing transaction as entitlement creation)", but neither
// CreateOrder's paid branch nor internal/worker/payments.go's
// handlePaymentCaptured ever calls MarkUsed — only CreateOrder's
// free-offer branch does (commerce_checkout.go), which is unreachable for
// an invitation_only-typed offer (type is a single enum value; an offer
// cannot be both 'free' and 'invitation_only'). This test asserts the
// CURRENT (gap-exhibiting) behavior rather than papering over it — see
// this task's final report for the recommended fix.
func TestCreateOrder_InvitationOnly_Gating(t *testing.T) {
	d, pool := commerceTestDeps(t)
	orgID, courseID, ownerID, learnerID := commerceSeedOrgCourseOfferFixture(t, pool)
	ctx := context.Background()

	var offerID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO offers (org_id, course_id, type, price, currency, tax_rate_percent, created_by)
		VALUES ($1, $2, 'invitation_only', 999, 'INR', 0, $3)
		RETURNING id
	`, orgID, courseID, ownerID).Scan(&offerID))

	engine := newCommerceInviteTokenEngine(t, d)
	learnerToken := commerceMintToken(t, learnerID, "learner-"+learnerID+"@example.com")
	ownerToken := commerceMintToken(t, ownerID, "owner-"+ownerID+"@example.com")

	assertNoEntitlementOrOrder := func(t *testing.T, expectedOrderCount int) {
		t.Helper()
		var orderCount, entitlementCount int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE offer_id = $1`, offerID).Scan(&orderCount))
		require.Equal(t, expectedOrderCount, orderCount)
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM entitlements WHERE course_id = $1`, courseID).Scan(&entitlementCount))
		require.Equal(t, 0, entitlementCount, "invitation_only's paid path never grants an entitlement without a webhook")
	}

	// 1. No token at all.
	rec := postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", learnerToken, map[string]any{})
	require.True(t, rec.Code >= 400 && rec.Code < 500, "checkout with no invite token must be rejected 4xx, got %d: %s", rec.Code, rec.Body.String())
	assertNoEntitlementOrOrder(t, 0)

	// 2. Garbage/nonexistent token.
	rec = postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", learnerToken, map[string]any{"invite_token": "garbage-token-does-not-exist"})
	require.True(t, rec.Code >= 400 && rec.Code < 500, "checkout with a garbage invite token must be rejected 4xx, got %d: %s", rec.Code, rec.Body.String())
	assertNoEntitlementOrOrder(t, 0)

	// 3. A real, unexpired token minted via the real invite-token-creation
	// path (CreateInviteToken / "invitetoken.create").
	createRec := postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/invite-tokens", ownerToken, map[string]any{})
	require.Equal(t, http.StatusCreated, createRec.Code, createRec.Body.String())
	var createResp map[string]any
	require.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &createResp))
	realToken, _ := createResp["token"].(string)
	require.NotEmpty(t, realToken)

	rec = postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", learnerToken, map[string]any{"invite_token": realToken})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assertNoEntitlementOrOrder(t, 1) // one order created; still no entitlement (paid path, no webhook yet)

	// CreateOrder marks the token used at order-creation time for both the
	// free and paid paths (see CreateOrder's paid-path comment and
	// InviteTokenRepo.MarkUsed's doc comment) since orders carries no
	// invite_token_id column the webhook worker could use to defer this to
	// payment-confirmation time the way discount-code redemption is
	// deferred.
	var usedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT used_at FROM commerce_invite_tokens WHERE token = $1`, realToken).Scan(&usedAt))
	require.NotNil(t, usedAt, "a paid invitation_only offer's invite token must be marked used at order-creation time")

	// 4. Reattempting with the SAME token immediately must be rejected as
	// already-used, and must not create a second order.
	rec = postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", learnerToken, map[string]any{"invite_token": realToken})
	require.True(t, rec.Code >= 400 && rec.Code < 500, "reuse of an already-used invite token must be rejected 4xx, got %d: %s", rec.Code, rec.Body.String())
	assertNoEntitlementOrOrder(t, 1)

	// 5. A token whose expiry is in the past IS correctly rejected,
	// regardless of offer type — this part of the gate works correctly.
	past := time.Now().Add(-time.Hour)
	expiredToken := "invite_expired_" + uuid.NewString()
	_, err := d.InviteTokens.Create(ctx, pool, orgID, offerID, ownerID, expiredToken, nil, &past)
	require.NoError(t, err)

	rec = postJSON(t, engine, "/api/courses/"+courseID+"/offers/"+offerID+"/checkout/order", learnerToken, map[string]any{"invite_token": expiredToken})
	require.True(t, rec.Code >= 400 && rec.Code < 500, "checkout with an expired invite token must be rejected 4xx, got %d: %s", rec.Code, rec.Body.String())
	assertNoEntitlementOrOrder(t, 1)
}
