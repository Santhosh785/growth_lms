// Catalog load test: the public, anonymous course-catalog surfaces a
// prospective learner hits before signing up. These are the highest-traffic,
// fully-cacheable read paths, so they get the tightest latency SLO.
//
//   k6 run -e BASE_URL=https://staging.example.com -e ORG_SLUG=acme loadtest/catalog.js

import http from 'k6/http';
import { check, group } from 'k6';
import { BASE_URL, ORG_SLUG, thresholds, rampingStages } from './lib.js';

export const options = {
  stages: rampingStages(),
  thresholds: thresholds(300),
};

export default function () {
  group('public org home', function () {
    const res = http.get(`${BASE_URL}/o/${ORG_SLUG}`);
    check(res, { 'home 200': (r) => r.status === 200 });
  });

  group('embeddable catalog', function () {
    const res = http.get(`${BASE_URL}/embed/o/${ORG_SLUG}/catalog`);
    check(res, {
      'catalog 200': (r) => r.status === 200,
      'catalog is framable': (r) => !r.headers['X-Frame-Options'],
    });
  });

  group('sitemap', function () {
    const res = http.get(`${BASE_URL}/o/${ORG_SLUG}/sitemap.xml`);
    check(res, { 'sitemap 200': (r) => r.status === 200 });
  });
}
