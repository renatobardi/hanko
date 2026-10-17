// Hanko UI — progressive enhancement. The app works without this file; it only
// makes htmx swap validation errors and keeps the live run log scrolled.
(function () {
  "use strict";

  // Browser errors → Hanko → OTLP (ADR-0058 §C). No third-party service: the report goes to the same origin, where
  // the server turns it into a span with an "exception" event. At most 5 per page, never twice the same one.
  (function () {
    var sent = 0;
    var seen = {};
    function report(kind, name, message, stack, source, line, col) {
      try {
        var key = kind + "|" + message + "|" + line + "|" + col;
        if (sent >= 5 || seen[key]) return;
        seen[key] = true;
        sent++;
        var body = JSON.stringify({
          kind: kind, name: name || "Error", message: String(message || "").slice(0, 500),
          stack: String(stack || "").slice(0, 4000), source: source || "", line: line || 0, col: col || 0,
          url: location.href
        });
        var blob = new Blob([body], { type: "application/json" });
        if (!(navigator.sendBeacon && navigator.sendBeacon("/telemetry/errors", blob))) {
          fetch("/telemetry/errors", { method: "POST", body: blob, keepalive: true, credentials: "same-origin" }).catch(function () {});
        }
      } catch (_) { /* reporting must never throw */ }
    }
    window.addEventListener("error", function (e) {
      if (!e.message && !e.error) return; // resource load errors carry neither
      report("error", e.error && e.error.name, (e.error && e.error.message) || e.message, e.error && e.error.stack, e.filename, e.lineno, e.colno);
    });
    window.addEventListener("unhandledrejection", function (e) {
      var r = e.reason;
      report("unhandledrejection", r && r.name, (r && r.message) || String(r), r && r.stack, "", 0, 0);
    });
  })();

  // htmx ignores 4xx by default; our handlers answer errors with a fragment.
  document.addEventListener("htmx:beforeSwap", function (e) {
    var s = e.detail.xhr && e.detail.xhr.status;
    if (s === 403 || s === 404 || s === 409 || s === 422 || s === 429 || s === 501) {
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

  // "/" focuses the search: on the Buscar screen it focuses the field; elsewhere it opens that screen.
  document.addEventListener("keydown", function (e) {
    if (e.key !== "/" || e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return;
    var t = e.target;
    if (t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))) return;
    var field = document.getElementById("q-search");
    if (field) {
      e.preventDefault();
      field.focus();
      field.select();
      return;
    }
    var link = document.querySelector('.sidebar a[href$="/buscar"]');
    if (link) {
      e.preventDefault();
      link.click();
    }
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

  // Islands (ADR-0005/ADR-0026): <div data-island="name" data-…> is enhanced by /static/islands/<name>.js
  // (React, built from ui/islands). The host's own markup inside the element is the no-JS version and
  // stays when the bundle fails to load. Same-origin module scripts only; no inline code.
  var ISLANDS = { "run-log": 1, "org-chart": 1, "anchor-editor": 1 };
  var islandsV = "";
  try {
    islandsV = new URL(document.currentScript.src).searchParams.get("v") || "";
  } catch (e) {}
  function loadIslands() {
    var els = document.querySelectorAll("[data-island]");
    Array.prototype.forEach.call(els, function (el) {
      var name = el.getAttribute("data-island");
      if (!ISLANDS[name] || el.dataset.islandState) return;
      el.dataset.islandState = "loading";
      import("/static/islands/" + name + ".js" + (islandsV ? "?v=" + encodeURIComponent(islandsV) : ""))
        .then(function (m) {
          m.mount(el);
          el.dataset.islandState = "ready";
        })
        .catch(function () {
          el.dataset.islandState = "failed";
        });
    });
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", loadIslands);
  else loadIslands();

  // Tasks: os seletores de visão (agrupar, ordenar, filtros) se aplicam ao mudar.
  document.addEventListener("change", function (e) {
    var sel = e.target;
    if (!sel.matches || !sel.matches("form[data-autosubmit] select")) return;
    if (sel.form.requestSubmit) sel.form.requestSubmit();
    else sel.form.submit();
  });

  // Quadro de tasks: arrastar um card para outra coluna muda o status (POST no mesmo endpoint do
  // formulário da task, como htmx). Se o servidor recusar, o card volta e o erro vai para #flash.
  var dragged = null;
  function boardCount(col, delta) {
    var c = col.querySelector("[data-count]");
    if (c) c.textContent = String(Math.max(0, (parseInt(c.textContent, 10) || 0) + delta));
    var empty = col.querySelector(".board-empty");
    if (empty) empty.classList.toggle("hidden", col.querySelectorAll(".board-card").length > 0);
  }
  document.addEventListener("dragstart", function (e) {
    var card = e.target.closest && e.target.closest(".board-card[data-task]");
    if (!card || card.getAttribute("draggable") !== "true") return;
    dragged = card;
    card.classList.add("is-dragging");
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", card.dataset.task);
  });
  document.addEventListener("dragend", function () {
    if (dragged) dragged.classList.remove("is-dragging");
    dragged = null;
    Array.prototype.forEach.call(document.querySelectorAll(".board-col.drop-over"), function (c) {
      c.classList.remove("drop-over");
    });
  });
  document.addEventListener("dragover", function (e) {
    if (!dragged) return;
    var col = e.target.closest && e.target.closest(".board-col[data-status]");
    if (!col) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    col.classList.add("drop-over");
  });
  document.addEventListener("dragleave", function (e) {
    var col = e.target.closest && e.target.closest(".board-col");
    if (col && !col.contains(e.relatedTarget)) col.classList.remove("drop-over");
  });
  document.addEventListener("drop", function (e) {
    if (!dragged) return;
    var col = e.target.closest && e.target.closest(".board-col[data-status]");
    if (!col) return;
    e.preventDefault();
    col.classList.remove("drop-over");
    var card = dragged;
    var from = card.closest(".board-col");
    if (from === col) return;
    var next = card.nextSibling;
    col.appendChild(card);
    boardCount(from, -1);
    boardCount(col, 1);
    var body = new URLSearchParams();
    body.set("status", col.dataset.status);
    card.classList.add("is-saving");
    fetch(card.getAttribute("href"), {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded", "HX-Request": "true" },
      body: body.toString()
    })
      .then(function (res) {
        if (res.ok) return;
        return res.text().then(function (html) {
          from.insertBefore(card, next && next.parentNode === from ? next : null);
          boardCount(col, -1);
          boardCount(from, 1);
          var flash = document.getElementById("flash");
          if (flash) flash.innerHTML = html;
        });
      })
      .catch(function () {
        from.insertBefore(card, next && next.parentNode === from ? next : null);
        boardCount(col, -1);
        boardCount(from, 1);
      })
      .then(function () {
        card.classList.remove("is-saving");
      });
  });

  // Exportar: os checkboxes viram parâmetros do link de download (mesma origem, sessão do navegador).
  document.addEventListener("change", function (e) {
    var form = e.target.closest && e.target.closest("form[data-export]");
    if (!form) return;
    var link = form.querySelector("[data-export-link]");
    var qs = new URLSearchParams();
    Array.prototype.forEach.call(form.querySelectorAll("input[type=checkbox]:checked"), function (c) {
      qs.set(c.name, "true");
    });
    var s = qs.toString();
    link.setAttribute("href", form.dataset.export + (s ? "?" + s : ""));
  });

  // Importar: envia o arquivo à API (JSON no corpo) e abre a empresa criada; o erro da API aparece no formulário.
  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form.matches || !form.matches("form[data-import]")) return;
    e.preventDefault();
    var file = form.querySelector("input[type=file]").files[0];
    if (!file) return;
    var qs = new URLSearchParams();
    var slug = form.querySelector("[name=slug]").value.trim();
    if (slug) qs.set("slug", slug);
    qs.set("paused", form.querySelector("[name=active]").checked ? "false" : "true");
    var errEl = form.querySelector("[data-import-error]");
    var btn = form.querySelector("button[type=submit]");
    btn.disabled = true;
    errEl.classList.add("d-none");
    fetch(form.getAttribute("action") + "?" + qs.toString(), {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: file
    })
      .then(function (res) {
        return res.json().then(function (body) {
          if (res.ok && body.company && body.company.slug) {
            window.location.href = "/c/" + body.company.slug + "/";
            return;
          }
          errEl.textContent = (body && (body.detail || body.title)) || String(res.status);
          errEl.classList.remove("d-none");
        });
      })
      .catch(function (err) {
        errEl.textContent = String(err);
        errEl.classList.remove("d-none");
      })
      .then(function () {
        btn.disabled = false;
      });
  });

  // PWA: register the service worker (scope "/") only in a secure context (https or localhost).
  // It caches versioned static assets and serves an offline page; it never caches data.
  if ("serviceWorker" in navigator && window.isSecureContext) {
    window.addEventListener("load", function () {
      navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch(function () {});
    });
  }
})();

/* ---- sala (ADR-0033) ---- */
// Rascunho por sala no sessionStorage, botões "@slug" que inserem a citação e o Feed do time recolhido no
// celular. Tudo opcional: sem script a sala funciona igual.
(function () {
  "use strict";

  function store() {
    try {
      return window.sessionStorage;
    } catch (e) {
      return null;
    }
  }

  function restore(root) {
    var base = root && root.querySelectorAll ? root : document;
    var s = store();
    base.querySelectorAll("form[data-draft]").forEach(function (f) {
      var ta = f.querySelector("textarea");
      if (!s || !ta || ta.value) return;
      try {
        var v = s.getItem(f.getAttribute("data-draft"));
        if (v) ta.value = v;
      } catch (e) {}
    });
    if (window.matchMedia && window.matchMedia("(max-width: 1100px)").matches) {
      base.querySelectorAll("details.room-feed[open]:not([data-folded])").forEach(function (d) {
        d.setAttribute("data-folded", "1");
        d.removeAttribute("open");
      });
    }
  }

  document.addEventListener("input", function (e) {
    var f = e.target.closest && e.target.closest("form[data-draft]");
    var s = store();
    if (!f || !s || e.target.tagName !== "TEXTAREA") return;
    try {
      s.setItem(f.getAttribute("data-draft"), e.target.value);
    } catch (err) {}
  });

  document.addEventListener("htmx:afterRequest", function (e) {
    var f = e.target.closest && e.target.closest("form[data-draft]");
    var s = store();
    if (!f || !s || !e.detail.successful) return;
    try {
      s.removeItem(f.getAttribute("data-draft"));
    } catch (err) {}
  });

  document.addEventListener("click", function (e) {
    var b = e.target.closest && e.target.closest("[data-mention]");
    if (!b) return;
    var f = b.closest("form");
    var ta = f && f.querySelector("textarea");
    if (!ta) return;
    var t = b.getAttribute("data-mention") + " ";
    var p = typeof ta.selectionStart === "number" ? ta.selectionStart : ta.value.length;
    ta.value = ta.value.slice(0, p) + t + ta.value.slice(p);
    ta.focus();
    ta.selectionStart = ta.selectionEnd = p + t.length;
    ta.dispatchEvent(new Event("input", { bubbles: true }));
  });

  document.addEventListener("htmx:load", function (e) {
    restore(e.target);
  });
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", function () {
      restore(document);
    });
  } else {
    restore(document);
  }
})();

// ---- pipelines (ADR-0034) ----
// Quadro de um pipeline: arrastar um case para outra etapa faz o mesmo POST do formulário "Mover para" da página
// do case (etapa de destino e a versão que o quadro mostrou). Se o servidor recusar (aprovação, bloqueio, modo
// estrito, versão), o card volta e o erro vai para #flash; no sucesso o card guarda a versão nova.
(function () {
  "use strict";
  var dragged = null;
  function count(col, delta) {
    var c = col.querySelector("[data-count]");
    if (c) c.textContent = String(Math.max(0, (parseInt(c.textContent, 10) || 0) + delta));
    var empty = col.querySelector(".board-empty");
    if (empty) empty.classList.toggle("hidden", col.querySelectorAll(".board-card").length > 0);
  }
  document.addEventListener("dragstart", function (e) {
    var card = e.target.closest && e.target.closest(".board-card[data-case]");
    if (!card || card.getAttribute("draggable") !== "true") return;
    dragged = card;
    card.classList.add("is-dragging");
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("text/plain", card.dataset.case);
  });
  document.addEventListener("dragend", function () {
    if (dragged) dragged.classList.remove("is-dragging");
    dragged = null;
    Array.prototype.forEach.call(document.querySelectorAll(".board-col.drop-over"), function (c) {
      c.classList.remove("drop-over");
    });
  });
  document.addEventListener("dragover", function (e) {
    if (!dragged) return;
    var col = e.target.closest && e.target.closest(".board-col[data-stage]");
    if (!col) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    col.classList.add("drop-over");
  });
  document.addEventListener("drop", function (e) {
    if (!dragged) return;
    var col = e.target.closest && e.target.closest(".board-col[data-stage]");
    if (!col) return;
    e.preventDefault();
    col.classList.remove("drop-over");
    var card = dragged;
    var from = card.closest(".board-col");
    if (from === col) return;
    var next = card.nextSibling;
    col.appendChild(card);
    count(from, -1);
    count(col, 1);
    var body = new URLSearchParams();
    body.set("to_stage", col.dataset.stage);
    body.set("expected_version", card.dataset.version || "");
    card.classList.add("is-saving");
    function undo(html) {
      from.insertBefore(card, next && next.parentNode === from ? next : null);
      count(col, -1);
      count(from, 1);
      var flash = document.getElementById("flash");
      if (flash && html) flash.innerHTML = html;
    }
    fetch(card.dataset.move, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded", "HX-Request": "true" },
      body: body.toString()
    })
      .then(function (res) {
        if (res.ok) {
          var v = res.headers.get("X-Case-Version");
          if (v) card.dataset.version = v;
          return;
        }
        return res.text().then(undo);
      })
      .catch(function () {
        undo("");
      })
      .then(function () {
        card.classList.remove("is-saving");
      });
  });
})();
