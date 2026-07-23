// Player load test: the authenticated, entitlement-gated course player an
// enrolled learner uses during a lesson. Requires a real learner token and an
// enrolled course, so it is skipped cleanly if those are not provided.
//
//   k6 run -e BASE_URL=... -e AUTH_TOKEN=... -e COURSE_ID=... loadtest/player.js

import http from 'k6/http';
import { check, group, fail } from 'k6';
import { BASE_URL, COURSE_ID, authHeaders, thresholds, rampingStages } from './lib.js';

export const options = {
  stages: rampingStages(),
  // The player joins progress + entitlement + content, so it carries a looser
  // p95 than the static catalog.
  thresholds: thresholds(500),
};

export function setup() {
  if (!COURSE_ID) {
    fail('COURSE_ID is required for the player load test');
  }
}

export default function () {
  const headers = authHeaders();

  group('player', function () {
    const res = http.get(`${BASE_URL}/courses/${COURSE_ID}/player`, { headers });
    check(res, { 'player 200': (r) => r.status === 200 });
  });

  group('course progress', function () {
    const res = http.get(`${BASE_URL}/courses/${COURSE_ID}/progress`, { headers });
    check(res, { 'progress 200': (r) => r.status === 200 });
  });
}
