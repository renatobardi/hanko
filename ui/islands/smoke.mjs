// Fumaça do artefato que vai no binário: carrega cada bundle minificado de internal/ui/static/islands num DOM
// (jsdom), monta com props data-* e confere o resultado. Roda depois do build (`npm test` encadeia os dois).
import { JSDOM } from "jsdom";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import vm from "node:vm";
import { dirname, resolve } from "node:path";
import { pathToFileURL, fileURLToPath } from "node:url";
import assert from "node:assert/strict";

const dir = resolve(dirname(fileURLToPath(import.meta.url)), "../../internal/ui/static/islands");
assert.ok(existsSync(dir), "rode `npm run build` antes");
const files = readdirSync(dir).filter((f) => !f.startsWith("vendor-")).sort();
assert.deepEqual(files, ["anchor-editor.js", "org-chart.js", "run-log.js"], "teto de 3 ilhas (ADR-0005)");

// O script do host (hanko.js) tem de ser JavaScript válido: um erro de sintaxe desliga TODO o JS da página (carregador
// das ilhas, quadro arrastável, atalhos, relato de erros do navegador). O Go não enxerga isso, então a fumaça confere.
new vm.Script(readFileSync(resolve(dir, "..", "hanko.js"), "utf8"), { filename: "hanko.js" });

const dom = new JSDOM("<!doctype html><body></body>", { url: "http://hanko.test/" });
const g = globalThis;
g.window = dom.window;
g.document = dom.window.document;
for (const k of ["HTMLElement", "Element", "Node", "MutationObserver", "Event", "MouseEvent", "KeyboardEvent", "SVGElement", "getComputedStyle", "requestAnimationFrame", "cancelAnimationFrame"]) {
  if (!(k in g) || k === "Event") Object.defineProperty(g, k, { value: dom.window[k], configurable: true, writable: true });
}
Object.defineProperty(g, "navigator", { value: dom.window.navigator, configurable: true });
g.IS_REACT_ACT_ENVIRONMENT = false;

const tick = (ms = 60) => new Promise((r) => setTimeout(r, ms));
const load = (name) => import(pathToFileURL(resolve(dir, name + ".js")).href);
const mk = (attrs, html = "") => {
  const el = document.createElement("div");
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  el.innerHTML = html;
  document.body.append(el);
  return el;
};

// org-chart
{
  const el = mk(
    {
      "data-api": "/api/v1/companies/acme",
      "data-can-edit": "1",
      "data-msgs": "{}",
      "data-nodes": JSON.stringify([{ id: "a", slug: "a", name: "Ada", title: "CEO", parent: "", status: "Ociosa", cls: "idle", meta: "", href: "/p/a", gate: false, initials: "AD" }]),
    },
    "<p>fallback sem JS</p>",
  );
  (await load("org-chart")).mount(el);
  await tick();
  assert.ok(!el.textContent.includes("fallback sem JS"), "org-chart: fallback não foi substituído");
  assert.ok(el.querySelector("[data-node='a']"), "org-chart: nó não renderizado");
}

// anchor-editor
{
  const el = mk({
    "data-api": "/api/v1/companies/acme",
    "data-task": "HNK-1",
    "data-target": "task_description",
    "data-doc": "Olá mundo",
    "data-can-comment": "0",
    "data-anchors": JSON.stringify([{ id: "c", quote: "mundo", start: 4, end: 9, status: "exact", author: "A", when: "", body: "x" }]),
  });
  (await load("anchor-editor")).mount(el);
  await tick();
  assert.equal(el.querySelector("mark")?.textContent, "mundo", "anchor-editor: trecho destacado");
}

// run-log
{
  const calls = [];
  g.fetch = async (u) => {
    calls.push(String(u));
    return new Response(JSON.stringify({ items: [] }), { status: 200 });
  };
  const el = mk({ "data-api": "/api/v1/companies/acme", "data-run": "r1", "data-live": "0", "data-msgs": "{}" });
  (await load("run-log")).mount(el);
  await tick(120);
  assert.equal(calls[0], "/api/v1/companies/acme/runs/r1/events?tail=true&limit=500");
  assert.ok(el.querySelector(".log-island"), "run-log: não montou");
}

console.log("smoke: 3 ilhas montam a partir do bundle commitado");
process.exit(0);
