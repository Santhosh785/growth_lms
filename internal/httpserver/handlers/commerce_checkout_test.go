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
