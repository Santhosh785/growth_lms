// Checkout load test: the paid-enrollment path — render the checkout page,
// then create the payment order (POST). The order-create is a state-changing,
// CSRF-protected call, so it needs both AUTH_TOKEN and CSRF_TOKEN.
//
// NOTE: this exercises order *creation* only. Access is never granted here —
// entitlement is granted solely by the verified Razorpay webhook (see
// webhook.js), never by this call, per the plan's non-negotiable rule.
//
//   k6 run -e BASE_URL=... -e AUTH_TOKEN=... -e CSRF_TOKEN=... \
//          -e COURSE_ID=... -e OFFER_ID=... loadtest/checkout.js

import http from 'k6/http';
import { check, group, fail } from 'k6';
import { BASE_URL, COURSE_ID, OFFER_ID, CSRF_TOKEN, authHeaders, thresholds, rampingStages } from './lib.js';

export const options = {
  // Checkout traffic is spikier and lower-volume than catalog; a smaller ramp
  // with a stricter error budget matches its criticality.
  stages: rampingStages(),
  thresholds: thresholds(700),
};

export function setup() {
  if (!COURSE_ID || !OFFER_ID) {
    fail('COURSE_ID and OFFER_ID are required for the checkout load test');
  }
}

export default function () {
  const base = `${BASE_URL}/courses/${COURSE_ID}/offers/${OFFER_ID}`;

  group('checkout page', function () {
    const res = http.get(`${base}/checkout`, { headers: authHeaders() });
    check(res, { 'checkout page 200': (r) => r.status === 200 });
  });

  group('create order', function () {
    const headers = authHeaders({
      'Content-Type': 'application/json',
      'X-CSRF-Token': CSRF_TOKEN,
    });
    const res = http.post(`${base}/checkout/order`, '{}', { headers });
    // 200/201 on success; 409 is acceptable under load if the learner already
    // has an open order for this offer (idempotent create).
    check(res, {
      'order created or idempotent': (r) => r.status === 200 || r.status === 201 || r.status === 409,
    });
  });
}
