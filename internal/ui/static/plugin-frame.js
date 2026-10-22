// Ponte postMessage das abas de plugin (ADR-0045).
//
// A página do plugin roda num iframe com sandbox="allow-scripts" (origem opaca, sem cookies, sem rede). Ela fala com
// o Hanko só por postMessage; este script, na página hospedeira, valida cada mensagem e a encaminha ao Hanko por
// fetch same-origin, que a entrega ao próprio plugin (page_call). Nada além disso atravessa.
//
// Protocolo (versão 1), mensagens do iframe para cá:
//   {hanko: 1, type: "call", id, method, params}   → responde {hanko: 1, id, ok, result | error}
//   {hanko: 1, type: "resize", height}              → ajusta a altura do quadro
(function () {
  "use strict";
  var frame = document.getElementById("plugin-frame");
  if (!frame) return;
  var bridge = frame.getAttribute("data-bridge") || "";
  var methodRe = /^[a-z][a-z0-9_.]{0,63}$/;
  var inflight = 0;
  var MAX_INFLIGHT = 8;
  var MAX_BODY = 256 * 1024;

  function reply(id, payload) {
    var w = frame.contentWindow;
    if (!w) return;
    payload.hanko = 1;
    payload.id = id;
    // o quadro tem origem opaca ("null"): o único alvo possível é "*", e a mensagem vai só a esta janela
    w.postMessage(payload, "*");
  }

  function fail(id, message) {
    reply(id, { ok: false, error: message });
  }

  window.addEventListener("message", function (ev) {
    // só o iframe desta página; qualquer outra janela é ignorada
    if (ev.source !== frame.contentWindow) return;
    var m = ev.data;
    if (!m || typeof m !== "object" || m.hanko !== 1) return;
    if (m.type === "resize") {
      var h = Number(m.height);
      if (isFinite(h) && h > 0) frame.style.height = Math.min(Math.max(Math.ceil(h), 120), 20000) + "px";
      return;
    }
    if (m.type !== "call") return;
    var id = m.id;
    if (typeof id !== "number" && typeof id !== "string") return;
    if (typeof m.method !== "string" || !methodRe.test(m.method)) return fail(id, "método inválido");
    if (inflight >= MAX_INFLIGHT) return fail(id, "chamadas demais ao mesmo tempo");
    var body;
    try {
      body = JSON.stringify({ method: m.method, params: m.params === undefined ? null : m.params });
    } catch (e) {
      return fail(id, "parâmetros não serializáveis");
    }
    if (body.length > MAX_BODY) return fail(id, "parâmetros grandes demais");
    inflight++;
    fetch(bridge, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
      body: body,
    })
      .then(function (r) {
        return r.text().then(function (t) {
          var j = null;
          try { j = JSON.parse(t); } catch (e) { /* resposta que não é JSON */ }
          if (r.ok && j && "result" in j) return reply(id, { ok: true, result: j.result });
          fail(id, (j && j.error) || "o plugin recusou a chamada (" + r.status + ")");
        });
      })
      .catch(function () { fail(id, "sem resposta do Hanko"); })
      .then(function () { inflight--; });
  });
})();
