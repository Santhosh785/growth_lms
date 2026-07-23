// First-party page bootstrap (Task 12 PWA). Registers the service worker so
// the app is installable and has an offline fallback. Kept tiny and dependency
// free; a no-op where service workers are unsupported.
(function () {
  if ('serviceWorker' in navigator) {
    window.addEventListener('load', function () {
      navigator.serviceWorker.register('/sw.js').catch(function () {
        /* registration is best-effort; ignore failures */
      });
    });
  }
})();
