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

  // htmx ignores 4xx/5xx by default; our handlers answer errors with a fragment. s.fail retargets it to #flash, so on a
  // request that changes something any status carrying HX-Retarget is safe to swap (a poll's GET keeps retrying quietly).
  function isGet(e) {
    var cfg = e.detail && e.detail.requestConfig;
    return !!cfg && String(cfg.verb).toLowerCase() === "get";
  }
  document.addEventListener("htmx:beforeSwap", function (e) {
    var x = e.detail.xhr;
    var s = x && x.status;
    if (s === 403 || s === 404 || s === 409 || s === 422 || s === 429 || s === 501 || (s >= 400 && !isGet(e) && x.getResponseHeader("HX-Retarget"))) {
      e.detail.shouldSwap = true;
      e.detail.isError = false;
    }
  });

  // An error nobody swapped (a proxy's 502, a 500 without our fragment) or a request that never reached the server
  // used to vanish: the person saw nothing and refreshed to find out. A request that changes something now says so in
  // #flash (polls are GETs and retry on their own).
  function flashError(e, key, status) {
    var flash = document.getElementById("flash");
    if (!flash || isGet(e)) return;
    var box = document.createElement("div");
    box.className = "alert danger";
    box.setAttribute("role", "alert");
    var msg = document.createElement("div");
    msg.textContent = (flash.getAttribute(key) || "") + (status ? " (HTTP " + status + ")" : "");
    box.appendChild(msg);
    flash.textContent = "";
    flash.appendChild(box);
  }
  document.addEventListener("htmx:responseError", function (e) {
    flashError(e, "data-err-server", e.detail.xhr && e.detail.xhr.status);
  });
  document.addEventListener("htmx:sendError", function (e) {
    flashError(e, "data-err-net", 0);
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

  // Drag and drop de anexos (ADR-0052): [data-dropzone] (uma caixa ou um formulário inteiro) aceita arquivos soltos e
  // os põe no <input type="file" multiple> dela; sem este script o seletor de arquivos continua funcionando. Com
  // data-dropzone-submit, soltar ou escolher envia o formulário na hora.
  function dropzoneOf(el) {
    return el && el.closest ? el.closest("[data-dropzone]") : null;
  }
  function dzInput(z) {
    return z.querySelector('input[type="file"]');
  }
  var FILE_ICON = '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5z"></path><path d="M14 2v6h6"></path></svg>';
  var X_ICON = '<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M18 6 6 18M6 6l12 12"></path></svg>';
  function fmtSize(n) {
    return n >= 1048576 ? (n / 1048576).toFixed(1) + " MiB" : n >= 1024 ? Math.round(n / 1024) + " KiB" : n + " B";
  }
  // A prévia dos arquivos escolhidos, colados ou soltos: miniatura da imagem, ou ícone com a extensão; nome e tamanho no
  // title (mouse por cima) e no aria-label; o X remove só aquele arquivo.
  function dzRender(z) {
    var list = z.querySelector("[data-dz-list]");
    var input = dzInput(z);
    if (!list || !input) return;
    list.textContent = "";
    Array.prototype.forEach.call(input.files, function (f, i) {
      var li = document.createElement("li");
      var box = document.createElement("span");
      box.className = "att-thumb";
      box.title = f.name + " · " + fmtSize(f.size);
      box.setAttribute("aria-label", box.title);
      if (/^image\/(png|jpeg|gif|webp)$/.test(f.type) && f.size <= 5 * 1048576) {
        // a miniatura é desenhada num canvas (recorte quadrado, como object-fit: cover), sem atribuir uma URL derivada do
        // arquivo a um elemento HTML; se a imagem não decodifica, cai no ícone
        var cv = document.createElement("canvas");
        cv.width = cv.height = 144;
        box.appendChild(cv);
        window
          .createImageBitmap(f)
          .then(function (bm) {
            var side = Math.min(bm.width, bm.height);
            cv.getContext("2d").drawImage(bm, (bm.width - side) / 2, (bm.height - side) / 2, side, side, 0, 0, 144, 144);
            if (bm.close) bm.close();
          })
          .catch(function () {
            cv.remove();
            box.insertAdjacentHTML("afterbegin", FILE_ICON);
          });
      } else {
        box.insertAdjacentHTML("beforeend", FILE_ICON);
        var m = /\.([A-Za-z0-9]{1,5})$/.exec(f.name);
        if (m) {
          var ext = document.createElement("span");
          ext.className = "att-ext";
          ext.textContent = m[1].toUpperCase();
          box.appendChild(ext);
        }
      }
      var rm = document.createElement("button");
      rm.type = "button";
      rm.className = "dz-remove";
      rm.setAttribute("data-dz-remove", String(i));
      rm.setAttribute("aria-label", (list.getAttribute("data-remove-label") || "Remover anexo") + ": " + f.name);
      rm.innerHTML = X_ICON;
      box.appendChild(rm);
      li.appendChild(box);
      list.appendChild(li);
    });
  }
  function dzSetFiles(input, files) {
    try {
      var dt = new DataTransfer();
      files.forEach(function (f) {
        dt.items.add(f);
      });
      input.files = dt.files;
    } catch (err) {
      return false;
    }
    return true;
  }
  document.addEventListener("click", function (e) {
    var b = e.target.closest && e.target.closest("[data-dz-remove]");
    if (!b) return;
    var z = dropzoneOf(b);
    var input = z && dzInput(z);
    if (!input) return;
    e.preventDefault();
    var drop = Number(b.getAttribute("data-dz-remove"));
    dzSetFiles(
      input,
      Array.prototype.filter.call(input.files, function (_, i) {
        return i !== drop;
      })
    );
    dzRender(z);
  });
  // Colar (Ctrl/Cmd+V) uma imagem ou arquivo da área de transferência anexa e mostra a miniatura; texto colado segue
  // para a caixa. Só se intercepta quando há arquivo e nenhum texto: "Copiar imagem" do navegador, prints e arquivos do
  // Finder/Explorer trazem arquivo (e às vezes um <img> em HTML), enquanto uma tabela do Excel ou texto formatado trazem
  // texto e continuam colando como texto. Print sem nome ganha um nome legível.
  document.addEventListener("paste", function (e) {
    var z = dropzoneOf(e.target);
    var cd = e.clipboardData;
    var input = z && dzInput(z);
    if (!input || !cd || !cd.files || !cd.files.length || cd.getData("text/plain").trim() !== "") return;
    var now = new Date();
    var p2 = function (n) {
      return String(n).padStart(2, "0");
    };
    var stamp = now.getFullYear() + "-" + p2(now.getMonth() + 1) + "-" + p2(now.getDate()) + "-" + p2(now.getHours()) + p2(now.getMinutes());
    var added = Array.prototype.map.call(cd.files, function (f, i) {
      if (f.name && !/^image\.(png|jpe?g|gif|webp)$/i.test(f.name)) return f;
      var ext = (f.type.split("/")[1] || "png").replace("jpeg", "jpg");
      return new File([f], "colado-" + stamp + (cd.files.length > 1 ? "-" + (i + 1) : "") + "." + ext, { type: f.type, lastModified: Date.now() });
    });
    var all = Array.prototype.slice.call(input.files).concat(added);
    if (!dzSetFiles(input, all)) return;
    e.preventDefault();
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
  document.addEventListener("dragover", function (e) {
    var z = dropzoneOf(e.target);
    if (!z || !e.dataTransfer || Array.prototype.indexOf.call(e.dataTransfer.types || [], "Files") < 0) return;
    e.preventDefault();
    z.classList.add("is-over");
  });
  document.addEventListener("dragleave", function (e) {
    var z = dropzoneOf(e.target);
    if (z && !z.contains(e.relatedTarget)) z.classList.remove("is-over");
  });
  document.addEventListener("drop", function (e) {
    var z = dropzoneOf(e.target);
    if (!z) return;
    z.classList.remove("is-over");
    var input = dzInput(z);
    if (!input || !e.dataTransfer || !e.dataTransfer.files || !e.dataTransfer.files.length) return;
    e.preventDefault();
    try {
      var dt = new DataTransfer();
      var i;
      for (i = 0; i < input.files.length; i++) dt.items.add(input.files[i]);
      for (i = 0; i < e.dataTransfer.files.length; i++) dt.items.add(e.dataTransfer.files[i]);
      input.files = dt.files;
    } catch (err) {
      input.files = e.dataTransfer.files;
    }
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
  document.addEventListener("change", function (e) {
    var input = e.target;
    if (!input.matches || !input.matches('input[type="file"]')) return;
    var z = dropzoneOf(input);
    if (!z) return;
    dzRender(z);
    if (z.hasAttribute("data-dropzone-submit") && input.files.length) {
      var form = z.tagName === "FORM" ? z : z.closest("form");
      if (form && form.requestSubmit) form.requestSubmit();
    }
  });

  // Compositores (form[data-composer]: chat, sala e comentário da task). Só o envio do próprio formulário, com resposta
  // 2xx, limpa o texto e os arquivos, e só o que foi enviado: o autosave do rascunho (hx-put da caixa de texto) também
  // dispara htmx:afterRequest, que sobe até o formulário; um erro trocado para o #flash (409, 422) não pode apagar o que
  // a pessoa escreveu; e o que ela digitou ou anexou durante o envio fica. Ao sair o envio, a atualização da lista em
  // andamento é cancelada (ver livePoll nas views): uma resposta atrasada trocaria a lista nova por uma velha.
  function composerOf(e) {
    var f = e.detail && e.detail.elt;
    return f && f.tagName === "FORM" && f.hasAttribute("data-composer") ? f : null;
  }
  function sent(e) {
    var x = e.detail && e.detail.xhr;
    return !!x && x.status >= 200 && x.status < 300;
  }
  function fileNames(input) {
    var out = [];
    for (var i = 0; input && i < input.files.length; i++) out.push(input.files[i].name + ":" + input.files[i].size);
    return out.join("|");
  }
  var outbox = new WeakMap(); // form -> {text, files} do envio em andamento
  document.addEventListener("htmx:beforeRequest", function (e) {
    var f = composerOf(e);
    if (!f) return;
    var ta = f.querySelector("textarea");
    outbox.set(f, { text: ta ? ta.value : "", files: fileNames(f.querySelector('input[type="file"]')) });
    var sel = f.getAttribute("hx-target");
    var live = sel && document.querySelector(sel);
    if (live && window.htmx) window.htmx.trigger(live, "htmx:abort");
  });
  document.addEventListener("htmx:afterRequest", function (e) {
    var f = composerOf(e);
    var out = f && outbox.get(f);
    if (!f) return;
    outbox.delete(f);
    if (!out || !sent(e)) return;
    var ta = f.querySelector("textarea");
    if (ta && ta.value === out.text) {
      ta.value = "";
      var st = f.querySelector(".draft-state");
      if (st) st.textContent = "";
    }
    var input = f.querySelector('input[type="file"]');
    if (input && fileNames(input) === out.files) {
      input.value = "";
      input.dispatchEvent(new Event("change", { bubbles: true }));
    }
  });

  // Enter envia; Shift+Enter quebra a linha (Ctrl/Cmd+Enter também envia). No celular (teclado virtual) o Enter quebra a
  // linha e o envio é pelo botão. Não envia no meio de uma composição (acento, IME) nem uma caixa vazia sem anexo.
  document.addEventListener("keydown", function (e) {
    var ta = e.target;
    if (e.key !== "Enter" || e.isComposing || e.keyCode === 229 || !ta.matches || !ta.matches("textarea")) return;
    var form = ta.closest("form[data-composer], form[data-enter-send]");
    if (!form) return;
    var modifier = e.ctrlKey || e.metaKey;
    if (!modifier && (e.shiftKey || e.altKey || (window.matchMedia && window.matchMedia("(pointer: coarse)").matches))) return;
    e.preventDefault();
    var file = form.querySelector('input[type="file"]');
    if (!ta.value.trim() && !(file && file.files.length)) return;
    var send = form.querySelector('button[type="submit"]');
    if (send && send.disabled) return;
    if (form.requestSubmit) form.requestSubmit();
  });

  // Caixa fixa embaixo (padrão) com alfinete para soltá-la, e setas flutuantes para o início e o fim da conversa. A
  // escolha do alfinete fica no navegador; a conversa abre no fim e acompanha mensagem nova só se você estiver perto do fim.
  var PIN_KEY = "hanko.composer.pinned";
  function pinStored() {
    try {
      return window.localStorage.getItem(PIN_KEY) !== "0";
    } catch (err) {
      return true;
    }
  }
  function applyPin(pinned) {
    document.querySelectorAll("form[data-composer]").forEach(function (f) {
      f.classList.toggle("is-pinned", pinned);
      var b = f.querySelector("[data-composer-pin]");
      if (b) b.setAttribute("aria-pressed", pinned ? "true" : "false");
    });
  }
  document.addEventListener("click", function (e) {
    var b = e.target.closest && e.target.closest("[data-composer-pin]");
    if (!b) return;
    var next = b.getAttribute("aria-pressed") !== "true";
    try {
      window.localStorage.setItem(PIN_KEY, next ? "1" : "0");
    } catch (err) {}
    applyPin(next);
  });
  function docBottomGap() {
    return document.documentElement.scrollHeight - (window.scrollY + window.innerHeight);
  }
  // Feed do time (coluna da direita da sala): fixo por padrão, com o mesmo alfinete; a escolha também fica no navegador.
  var FEED_PIN_KEY = "hanko.feed.pinned";
  function feedPinStored() {
    try {
      return window.localStorage.getItem(FEED_PIN_KEY) !== "0";
    } catch (err) {
      return true;
    }
  }
  function applyFeedPin(pinned) {
    document.querySelectorAll("[data-feed-aside]").forEach(function (aside) {
      aside.classList.toggle("is-pinned", pinned);
      var b = aside.querySelector("[data-feed-pin]");
      if (b) b.setAttribute("aria-pressed", pinned ? "true" : "false");
    });
  }
  document.addEventListener("click", function (e) {
    var b = e.target.closest && e.target.closest("[data-feed-pin]");
    if (!b) return;
    e.preventDefault(); // o botão mora no <summary>: não recolhe o card
    var next = b.getAttribute("aria-pressed") !== "true";
    try {
      window.localStorage.setItem(FEED_PIN_KEY, next ? "1" : "0");
    } catch (err) {}
    applyFeedPin(next);
  });
  // Setas: uma por vez, centradas na coluna da conversa logo acima da caixa. Na metade de cima aparece a que desce ao
  // fim; na de baixo, a que sobe ao início. Conversa curta (cabe em poucas telas) não mostra nenhuma.
  var ARROW_MIN = 120; // px de rolagem mínima para a seta valer a pena
  function updateArrows() {
    document.querySelectorAll("[data-scroll-arrows]").forEach(function (box) {
      var up = box.querySelector('[data-scroll-to="top"]');
      var down = box.querySelector('[data-scroll-to="bottom"]');
      var room = document.documentElement.scrollHeight - window.innerHeight;
      var longEnough = room > 2 * ARROW_MIN;
      var upper = window.scrollY < room / 2;
      if (down) down.classList.toggle("is-on", longEnough && upper && docBottomGap() > ARROW_MIN);
      if (up) up.classList.toggle("is-on", longEnough && !upper && window.scrollY > ARROW_MIN);
      var form = box.closest("form");
      if (form) {
        var r = form.getBoundingClientRect();
        box.style.left = r.left + r.width / 2 + "px";
        box.style.bottom = (r.top < window.innerHeight ? Math.max(16, window.innerHeight - r.top + 12) : 16) + "px";
      }
    });
  }
  document.addEventListener("click", function (e) {
    var b = e.target.closest && e.target.closest("[data-scroll-to]");
    if (!b) return;
    var top = b.getAttribute("data-scroll-to") === "top" ? 0 : document.documentElement.scrollHeight;
    window.scrollTo({ top: top, behavior: window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
  });
  var scrollTick = false;
  function onScroll() {
    if (scrollTick) return;
    scrollTick = true;
    window.requestAnimationFrame(function () {
      scrollTick = false;
      updateArrows();
    });
  }
  window.addEventListener("scroll", onScroll, { passive: true });
  window.addEventListener("resize", onScroll);
  var followBottom = false; // perto do fim (ou acabou de enviar) quando a lista é trocada: acompanha a mensagem nova
  document.addEventListener("htmx:beforeSwap", function (e) {
    var t = e.detail && e.detail.target;
    if (t && (t.id === "chat-thread" || t.id === "room-thread" || t.id === "thread")) followBottom = followBottom || docBottomGap() < 160;
  });
  document.addEventListener("htmx:afterSettle", function (e) {
    var t = e.detail && e.detail.target;
    var id = t && t.id;
    if (id === "chat-thread" || id === "room-thread" || id === "thread") {
      if (followBottom) window.scrollTo({ top: document.documentElement.scrollHeight });
      followBottom = false;
    }
    updateArrows();
  });
  document.addEventListener("htmx:afterRequest", function (e) {
    var f = e.detail && e.detail.elt;
    var st = e.detail && e.detail.xhr ? e.detail.xhr.status : 0;
    if (f && f.tagName === "FORM" && f.hasAttribute("data-composer") && st >= 200 && st < 300) followBottom = true;
  });
  function initConversation() {
    applyFeedPin(feedPinStored());
    if (!document.querySelector("form[data-composer]")) return;
    applyPin(pinStored());
    // o body sobrevive à navegação do hx-boost: guarda o caminho da conversa aberta, não um simples "já abri uma"
    if (document.body.getAttribute("data-conv-opened") !== location.pathname && document.querySelector("#chat-thread, #room-thread")) {
      document.body.setAttribute("data-conv-opened", location.pathname);
      window.scrollTo({ top: document.documentElement.scrollHeight });
    }
    updateArrows();
  }
  document.addEventListener("htmx:load", initConversation);
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", initConversation);
  else initConversation();

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

  // só o envio do próprio formulário (2xx) apaga o rascunho local, nunca o autosave da caixa de texto nem um erro
  document.addEventListener("htmx:afterRequest", function (e) {
    var f = e.detail.elt;
    var s = store();
    var x = e.detail.xhr;
    if (!f || !f.matches || !f.matches("form[data-draft]") || !s || !x || x.status < 200 || x.status >= 300) return;
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
  // Avisos ao vivo da pessoa (ADR-0042): toasts por SSE e o selo de não lidos no menu.
  (function () {
    var body = document.body;
    if (!body || !body.hasAttribute("data-notify") || !window.EventSource) return;
    var box = document.getElementById("toasts");
    function safePath(u) {
      return typeof u === "string" && u.charAt(0) === "/" && u.charAt(1) !== "/" ? u : "";
    }
    function toast(p) {
      if (!box) return;
      var el = document.createElement(safePath(p.url) ? "a" : "div");
      el.className = "toast";
      if (safePath(p.url)) el.setAttribute("href", p.url);
      var t = document.createElement("b");
      t.textContent = p.title || "";
      el.appendChild(t);
      if (p.body) {
        var b = document.createElement("span");
        b.textContent = p.body;
        el.appendChild(b);
      }
      box.appendChild(el);
      while (box.children.length > 4) box.removeChild(box.firstChild);
      setTimeout(function () {
        if (el.parentNode) el.parentNode.removeChild(el);
      }, 9000);
    }
    var es = new EventSource(body.getAttribute("data-notify-url"));
    es.addEventListener("notify", function (e) {
      var p;
      try {
        p = JSON.parse(e.data);
      } catch (_) {
        return;
      }
      toast(p);
      if (window.htmx) window.htmx.trigger(document.body, "refresh-inbox");
    });
    window.addEventListener("pagehide", function () {
      es.close();
    });
  })();

  // Web push (ADR-0042): ativa as notificações neste aparelho e manda a assinatura ao servidor.
  document.addEventListener("click", function (e) {
    var btn = e.target.closest && e.target.closest("[data-push-enable]");
    if (!btn) return;
    var box = btn.closest("[data-push-box]");
    var status = box && box.querySelector("[data-push-status]");
    function say(msg) {
      if (status) status.textContent = msg;
    }
    if (!("serviceWorker" in navigator) || !("PushManager" in window) || !window.Notification) {
      say(btn.getAttribute("data-unsupported") || "");
      return;
    }
    function key(b64) {
      var pad = "=".repeat((4 - (b64.length % 4)) % 4);
      var raw = atob((b64 + pad).replace(/-/g, "+").replace(/_/g, "/"));
      var out = new Uint8Array(raw.length);
      for (var i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
      return out;
    }
    Notification.requestPermission()
      .then(function (perm) {
        if (perm !== "granted") throw new Error("denied");
        return navigator.serviceWorker.ready;
      })
      .then(function (reg) {
        return reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key(box.getAttribute("data-vapid")) });
      })
      .then(function (sub) {
        var j = sub.toJSON();
        return fetch(box.getAttribute("data-url"), {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ endpoint: j.endpoint, keys: j.keys })
        });
      })
      .then(function (res) {
        if (!res.ok) throw new Error("http " + res.status);
        window.location.reload();
      })
      .catch(function (err) {
        say((err && err.message === "denied" ? btn.getAttribute("data-denied") : btn.getAttribute("data-failed")) || "");
      });
  });
})();
