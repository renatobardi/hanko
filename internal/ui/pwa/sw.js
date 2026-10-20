// Hanko service worker — instalável e com página offline, sem cache de dados.
// Regra de ouro: HTML autenticado, API, SSE, auth, convites e qualquer método que
// não seja GET NUNCA passam pelo cache. Só guardamos assets estáticos versionados.
// O servidor substitui __HANKO_VERSION__ pela versão do binário: cada deploy gera
// um sw.js diferente, o que dispara a atualização e a troca do cache.
"use strict";

var VERSION = "__HANKO_VERSION__";
var CACHE = "hanko-static-" + VERSION;
var OFFLINE_PATHS = ["/offline?hl=pt-BR", "/offline?hl=en"];

// Pré-cache: a página offline (nos dois idiomas) e o que ela precisa para renderizar.
var PRECACHE = OFFLINE_PATHS.concat([
  "/static/hanko.css?v=" + VERSION,
  "/static/hanko.js?v=" + VERSION,
  "/static/favicon.svg",
  "/static/vendor/htmx.min.js"
]);

// Prefixos de caminho que o SW nunca intercepta (o navegador segue direto à rede). As páginas da
// aplicação (/c/…) são interceptadas só para o fallback offline de navegação; nada de HTML é guardado.
var NEVER_CACHE_PREFIXES = [
  "/api/", "/auth/", "/convite/", "/entrar", "/sair", "/idioma",
  "/mcp", "/memory-mcp", "/metrics", "/healthz", "/readyz"
];
// Sufixos de caminho nunca interceptados (streams SSE do log ao vivo).
var NEVER_CACHE_SUFFIXES = ["/sse"];

function isExcluded(url, request) {
  var p = url.pathname;
  if (request.method !== "GET") return true;
  if ((request.headers.get("accept") || "").indexOf("text/event-stream") !== -1) return true;
  for (var i = 0; i < NEVER_CACHE_SUFFIXES.length; i++) {
    if (p.slice(-NEVER_CACHE_SUFFIXES[i].length) === NEVER_CACHE_SUFFIXES[i]) return true;
  }
  // /static/ é a única área cacheável; tudo que casa com a lista de exclusão sai.
  if (p.indexOf("/static/") === 0) return false;
  for (var j = 0; j < NEVER_CACHE_PREFIXES.length; j++) {
    if (p.indexOf(NEVER_CACHE_PREFIXES[j]) === 0) return true;
  }
  return false;
}

self.addEventListener("install", function (e) {
  e.waitUntil(
    caches.open(CACHE).then(function (c) {
      return c.addAll(PRECACHE.map(function (u) { return new Request(u, { cache: "reload" }); }));
    }).then(function () { return self.skipWaiting(); })
  );
});

self.addEventListener("activate", function (e) {
  e.waitUntil(
    caches.keys().then(function (keys) {
      return Promise.all(keys.filter(function (k) {
        return k.indexOf("hanko-static-") === 0 && k !== CACHE;
      }).map(function (k) { return caches.delete(k); }));
    }).then(function () { return self.clients.claim(); })
  );
});

function offlinePage() {
  var lang = (self.navigator.language || "").toLowerCase().indexOf("en") === 0 ? "en" : "pt-BR";
  return caches.open(CACHE).then(function (c) {
    return c.match("/offline?hl=" + lang);
  }).then(function (r) {
    return r || new Response("offline", { status: 503, headers: { "Content-Type": "text/plain; charset=utf-8" } });
  });
}

self.addEventListener("fetch", function (e) {
  var req = e.request;
  var url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  if (isExcluded(url, req)) return;

  // Assets estáticos: cache-first quando versionados (?v=) ou pré-cacheados; só 200 é guardado.
  if (url.pathname.indexOf("/static/") === 0) {
    var cacheable = url.searchParams.has("v") || PRECACHE.indexOf(url.pathname + url.search) !== -1;
    if (!cacheable) return;
    e.respondWith(
      caches.open(CACHE).then(function (c) {
        return c.match(req).then(function (hit) {
          if (hit) return hit;
          return fetch(req).then(function (res) {
            if (res.status === 200 && res.type === "basic") c.put(req, res.clone());
            return res;
          });
        });
      })
    );
    return;
  }

  // Navegação: sempre rede; sem rede, a página offline. A resposta HTML nunca é guardada.
  if (req.mode === "navigate") {
    e.respondWith(fetch(req).catch(offlinePage));
  }
});

// Web push (ADR-0042): o servidor manda {title, body, url, tag}; mostramos a notificação e, ao tocar, abrimos a
// tela. Só caminhos relativos da própria origem; nada do payload vira HTML.
self.addEventListener("push", function (e) {
  var data = {};
  try {
    data = e.data ? e.data.json() : {};
  } catch (_) {
    data = { title: "Hanko", body: e.data ? e.data.text() : "" };
  }
  var title = data.title || "Hanko";
  var url = typeof data.url === "string" && data.url.charAt(0) === "/" && data.url.charAt(1) !== "/" ? data.url : "/";
  e.waitUntil(self.registration.showNotification(title, {
    body: data.body || "",
    tag: data.tag || undefined,
    icon: "/static/icons/apple-touch-icon.png",
    data: { url: url }
  }));
});

self.addEventListener("notificationclick", function (e) {
  e.notification.close();
  var url = (e.notification.data && e.notification.data.url) || "/";
  e.waitUntil(self.clients.matchAll({ type: "window", includeUncontrolled: true }).then(function (list) {
    for (var i = 0; i < list.length; i++) {
      var c = list[i];
      if (c.url.indexOf(self.location.origin) === 0 && "focus" in c) {
        if ("navigate" in c) c.navigate(url);
        return c.focus();
      }
    }
    return self.clients.openWindow(url);
  }));
});
