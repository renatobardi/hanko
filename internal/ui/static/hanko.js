// Hanko UI — progressive enhancement. The app works without this file; it only
// makes htmx swap validation errors and keeps the live run log scrolled.
(function () {
  "use strict";

  // htmx ignores 4xx by default; our handlers answer errors with a fragment.
  document.addEventListener("htmx:beforeSwap", function (e) {
    var s = e.detail.xhr && e.detail.xhr.status;
    if (s === 403 || s === 404 || s === 409 || s === 422 || s === 501) {
      e.detail.shouldSwap = true;
      e.detail.isError = false;
    }
  });

  // Mobile drawer: close after navigating.
  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest(".sidebar a");
    if (!a) return;
    var t = document.getElementById("nav-toggle");
    if (t && window.matchMedia("(max-width: 900px)").matches) t.checked = false;
  });

  // Cron presets: choosing a template fills the expression and lets htmx refresh the hint.
  document.addEventListener("change", function (e) {
    var sel = e.target;
    if (!sel.matches || !sel.matches("select[data-cron-preset]")) return;
    var input = document.getElementById(sel.getAttribute("data-cron-preset"));
    if (!input || !sel.value) return;
    input.value = sel.value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
    sel.selectedIndex = 0;
  });

  // Live log: stick to the bottom unless the reader scrolled up.
  function watchLog() {
    var log = document.getElementById("run-log");
    if (!log || log.dataset.watched) return;
    log.dataset.watched = "1";
    var stick = true;
    log.addEventListener("scroll", function () {
      stick = log.scrollHeight - log.scrollTop - log.clientHeight < 40;
    });
    log.scrollTop = log.scrollHeight;
    new MutationObserver(function () {
      var empty = document.getElementById("log-empty");
      if (empty && log.querySelector(".log-line")) empty.remove();
      if (stick) log.scrollTop = log.scrollHeight;
    }).observe(log, { childList: true });
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", watchLog);
  else watchLog();

  // PWA: register the service worker (scope "/") only in a secure context (https or localhost).
  // It caches versioned static assets and serves an offline page; it never caches data.
  if ("serviceWorker" in navigator && window.isSecureContext) {
    window.addEventListener("load", function () {
      navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch(function () {});
    });
  }
})();
