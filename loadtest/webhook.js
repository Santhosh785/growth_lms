// Webhook load test: the Razorpay payment webhook — the single most important
// endpoint to keep responsive, because a dropped or slow webhook means a
// paying learner never gets access. Each request is signed with a real
// HMAC-SHA256 signature so it passes VerifyWebhookSignature and exercises the
// full record + enqueue path (not just signature rejection).
//
//   k6 run -e BASE_URL=... -e WEBHOOK_SECRET=... loadtest/webhook.js
//
// Use a NON-PRODUCTION secret and a staging target: this posts synthetic
// payment.captured events and will create webhook_event rows.

import http from 'k6/http';
import crypto from 'k6/crypto';
import { check, fail } from 'k6';
import { BASE_URL, WEBHOOK_SECRET, thresholds, rampingStages } from './lib.js';

export const options = {
  stages: rampingStages(),
  thresholds: thresholds(400),
};

export function setup() {
  if (!WEBHOOK_SECRET) {
    fail('WEBHOOK_SECRET is required for the webhook load test');
  }
}

export default function () {
  // Unique event id per iteration so the handler's dedup (ON CONFLICT) treats
  // each as a fresh event rather than a replay.
  const eventId = `evt_load_${__VU}_${__ITER}_${Date.now()}`;
  const body = JSON.stringify({
    event: 'payment.captured',
    payload: {
      payment: {
        entity: {
          id: `pay_load_${__VU}_${__ITER}`,
          amount: 49900,
          currency: 'INR',
          status: 'captured',
        },
      },
    },
  });

  const signature = crypto.hmac('sha256', WEBHOOK_SECRET, body, 'hex');
  const res = http.post(`${BASE_URL}/api/webhooks/razorpay`, body, {
    headers: {
      'Content-Type': 'application/json',
      'X-Razorpay-Signature': signature,
      'x-razorpay-event-id': eventId,
    },
  });

  // The handler acknowledges valid signed events promptly (2xx) even though
  // fulfilment happens asynchronously on the worker. 429 is tolerated: the
  // route is deliberately rate-limited, and hitting that ceiling under a load
  // test is expected, not a failure of the endpoint.
  check(res, {
    'accepted or rate-limited': (r) => (r.status >= 200 && r.status < 300) || r.status === 429,
  });
}
