// Growth LMS service worker (Task 12 PWA foundation).
//
// Deliberately conservative: it precaches only first-party STATIC assets and a
// generic offline fallback. It never caches navigations, API responses, or any
// authenticated HTML — on a shared device that could serve one user's page (or
// a signed-in page after logout) to someone else. Dynamic content always goes
// to the network; the cache exists only to make the app installable and to
// show a friendly offline page when the network is down.

const CACHE = 'growth-lms-static-v1';
const PRECACHE = [
  '/static/htmx.min.js',
  '/static/icon.svg',
  '/offline.html',
];

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE).then((c) => c.addAll(PRECACHE)));
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  // Drop caches from older versions.
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))),
    ),
  );
  self.clients.claim();
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;

  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return; // never touch cross-origin

  // Static assets: cache-first (they're immutable / versioned).
  if (url.pathname.startsWith('/static/')) {
    event.respondWith(
      caches.match(req).then((hit) => hit || fetch(req).then((res) => {
        const copy = res.clone();
        caches.open(CACHE).then((c) => c.put(req, copy));
        return res;
      })),
    );
    return;
  }

  // Navigations: network-only, with the offline page as a fallback. Never
  // cache the response — it may be user-specific.
  if (req.mode === 'navigate') {
    event.respondWith(fetch(req).catch(() => caches.match('/offline.html')));
    return;
  }

  // Everything else (API/XHR): straight to the network, no caching.
});
