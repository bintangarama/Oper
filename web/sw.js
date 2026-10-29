const CACHE_NAME = 'oper-shell-v3';

self.addEventListener('install', (e) => {
  self.skipWaiting();
});

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys().then((keys) => Promise.all(
      keys.map((k) => caches.delete(k))
    )).then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);

  // Jangan pernah cache request API, SSE, atau halaman navigasi HTML agar UI selalu update live
  if (url.pathname.startsWith('/api/') || url.pathname === '/healthz' || e.request.mode === 'navigate' || url.pathname === '/' || url.pathname === '/index.html') {
    return;
  }

  // Cache-first hanya untuk assets statis seperti icon
  e.respondWith(
    caches.match(e.request).then((cached) => cached || fetch(e.request))
  );
});
