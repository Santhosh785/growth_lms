// Shared configuration and SLO thresholds for the growth-LMS k6 load tests
// (plan.md Task 11: "Load tests for catalog, player, checkout, and webhook
// endpoints"). Each scenario file imports from here so the SLOs are defined
// once and every script fails the run the same way when they are breached.
//
// All targets are parameterized via environment variables so the same scripts
// run against local, staging, or a dedicated load environment without edits:
//
//   BASE_URL        base origin, e.g. https://staging.example.com  (default http://localhost:8080)
//   ORG_SLUG        org slug for public catalog / checkout          (default "demo")
//   COURSE_ID       course UUID for player / checkout               (required by player.js, checkout.js)
//   OFFER_ID        offer UUID for checkout                         (required by checkout.js)
//   AUTH_TOKEN      learner bearer/session token for authed routes  (required by player.js, checkout.js)
//   CSRF_TOKEN      CSRF token matching the session cookie          (checkout.js state-changing POST)
//   WEBHOOK_SECRET  Razorpay webhook secret for signing payloads    (required by webhook.js)

export const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
export const ORG_SLUG = __ENV.ORG_SLUG || 'demo';
export const COURSE_ID = __ENV.COURSE_ID || '';
export const OFFER_ID = __ENV.OFFER_ID || '';
export const AUTH_TOKEN = __ENV.AUTH_TOKEN || '';
export const CSRF_TOKEN = __ENV.CSRF_TOKEN || '';
export const WEBHOOK_SECRET = __ENV.WEBHOOK_SECRET || '';

// Default SLOs. Read endpoints are held to a tighter p95 than the
// write/checkout path, which does more work per request.
export function thresholds(p95ms) {
  return {
    http_req_failed: ['rate<0.01'], // < 1% errors
    http_req_duration: [`p(95)<${p95ms}`],
  };
}

// A modest ramp that most CI-adjacent load environments can sustain. Override
// VUS / DURATION per run for a heavier soak.
export function rampingStages() {
  return [
    { duration: __ENV.RAMP_UP || '30s', target: Number(__ENV.VUS || 50) },
    { duration: __ENV.HOLD || '1m', target: Number(__ENV.VUS || 50) },
    { duration: __ENV.RAMP_DOWN || '15s', target: 0 },
  ];
}

// authHeaders returns headers for a learner-authenticated request, or throws a
// clear message if AUTH_TOKEN was not supplied.
export function authHeaders(extra) {
  if (!AUTH_TOKEN) {
    throw new Error('AUTH_TOKEN is required for this scenario; export a valid learner token');
  }
  return Object.assign(
    {
      Authorization: `Bearer ${AUTH_TOKEN}`,
      Cookie: `lms_session=${AUTH_TOKEN}`,
    },
    extra || {},
  );
}
