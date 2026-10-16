import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { OrgChart } from "./OrgChart";
import { CARD_H, CARD_W, GAP_X, GAP_Y, PAD, checkMove, layoutTree, type OrgNode } from "./layout";

const node = (id: string, parent = "", over: Partial<OrgNode> = {}): OrgNode => ({
  id,
  slug: id,
  name: id.toUpperCase(),
  title: "Cargo",
  parent,
  status: "Ociosa",
  cls: "idle",
  meta: "",
  href: `/c/acme/personas/${id}`,
  gate: false,
  initials: id.slice(0, 2).toUpperCase(),
  ...over,
});

// ceo <- cto <- eng, qa <- cto ; solo (outro topo)
const fixture = (): OrgNode[] => [node("ceo"), node("cto", "ceo"), node("eng", "cto", { cls: "run", status: "Executando" }), node("qa", "cto"), node("solo")];

describe("layout", () => {
  it("folhas em colunas consecutivas e pai centrado, como o host", () => {
    const l = layoutTree(fixture(), new Set());
    const at = (id: string) => l.byId.get(id)!;
    expect(at("eng").x).toBe(PAD);
    expect(at("qa").x).toBe(PAD + CARD_W + GAP_X);
    expect(at("cto").x).toBe((at("eng").x + at("qa").x) / 2);
    expect(at("ceo").x).toBe(at("cto").x);
    expect(at("eng").y).toBe(PAD + 2 * (CARD_H + GAP_Y));
    expect(at("solo").x).toBe(PAD + 2 * (CARD_W + GAP_X));
    expect(l.edges.length).toBe(3);
    expect(l.width).toBe(PAD * 2 + 3 * CARD_W + 2 * GAP_X);
  });

  it("colapsar esconde o ramo e conta os descendentes", () => {
    const l = layoutTree(fixture(), new Set(["cto"]));
    expect(l.byId.has("eng")).toBe(false);
    expect(l.byId.get("cto")!.hiddenBelow).toBe(2);
    expect(l.edges.length).toBe(1);
    expect(l.width).toBe(PAD * 2 + 2 * CARD_W + GAP_X); // cto e solo viram folhas
  });

  it("checkMove recusa a própria persona, ciclos e o mesmo pai", () => {
    const ns = fixture();
    expect(checkMove(ns, "ceo", "ceo")).toEqual({ ok: false, reason: "self" });
    expect(checkMove(ns, "ceo", "eng")).toEqual({ ok: false, reason: "cycle" });
    expect(checkMove(ns, "cto", "qa")).toEqual({ ok: false, reason: "cycle" });
    expect(checkMove(ns, "eng", "cto")).toEqual({ ok: false, reason: "same" });
    expect(checkMove(ns, "eng", "ceo")).toEqual({ ok: true });
    expect(checkMove(ns, "eng", "")).toEqual({ ok: true });
    expect(checkMove(ns, "solo", "qa")).toEqual({ ok: true });
  });
});

const msgs = { "org.moved": "{name} agora reporta a {to}", "org.moved.top": "{name} foi para o topo", "org.err.cycle": "ciclo: {to} está abaixo de {name}", "org.err.api": "falhou: {err}", "org.drop.top": "topo", "org.hint.edit": "Arraste" };

// o jsdom não tem layout: o palco mede 0, então o zoom inicial é 1 com origem (8, 8) e client = grafo + 8
const pt = (id: string, nodes: OrgNode[] = fixture()) => {
  const p = layoutTree(nodes, new Set()).byId.get(id)!;
  return { clientX: p.x + 8 + CARD_W / 2, clientY: p.y + 8 + CARD_H / 2 };
};

function drag(from: string, to: string, nodes = fixture()) {
  const stage = document.querySelector(".org-stage")!;
  const src = document.querySelector(`[data-node="${from}"] rect`)!;
  const s = pt(from, nodes);
  const d = pt(to, nodes);
  fireEvent.pointerDown(src, { ...s, button: 0, pointerId: 1 });
  fireEvent.pointerMove(stage, { clientX: s.clientX + 30, clientY: s.clientY + 30, pointerId: 1 });
  fireEvent.pointerMove(stage, { ...d, pointerId: 1 });
  fireEvent.pointerUp(stage, { ...d, pointerId: 1 });
}

const okResponse = () => new Response(JSON.stringify({ id: "x" }), { status: 200 });

describe("OrgChart", () => {
  it("arrastar uma persona para outra faz PATCH reports_to e atualiza a árvore", async () => {
    const fetchImpl = vi.fn(async () => okResponse());
    render(<OrgChart nodes={fixture()} canEdit apiBase="/api/v1/companies/acme" msgs={msgs} fetchImpl={fetchImpl} />);
    act(() => drag("eng", "ceo"));
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledTimes(1));
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/companies/acme/personas/eng");
    expect(init.method).toBe("PATCH");
    expect(JSON.parse(init.body as string)).toEqual({ reports_to: "ceo" });
    await waitFor(() => expect(screen.getByText("ENG agora reporta a CEO")).toBeTruthy());
    // eng subiu um nível: agora na mesma linha que o cto
    const tf = (id: string) => document.querySelector(`[data-node="${id}"]`)!.getAttribute("transform");
    expect(tf("eng")?.split(" ")[1]).toBe(tf("cto")?.split(" ")[1]);
  });

  it("soltar na zona de topo faz clear_reports_to", async () => {
    const fetchImpl = vi.fn(async () => okResponse());
    render(<OrgChart nodes={fixture()} canEdit apiBase="/api/v1/companies/acme" msgs={msgs} fetchImpl={fetchImpl} />);
    const stage = document.querySelector(".org-stage")!;
    const s = pt("cto");
    act(() => {
      fireEvent.pointerDown(document.querySelector('[data-node="cto"] rect')!, { ...s, button: 0, pointerId: 1 });
      fireEvent.pointerMove(stage, { clientX: s.clientX + 40, clientY: s.clientY + 40, pointerId: 1 });
    });
    const zone = document.querySelector(".org-root-drop") as HTMLElement;
    expect(zone).toBeTruthy();
    // sem layout no jsdom, a zona mede 0; devolve um retângulo para o teste de acerto
    zone.getBoundingClientRect = () => ({ left: 0, top: 0, right: 500, bottom: 40, width: 500, height: 40, x: 0, y: 0, toJSON() {} }) as DOMRect;
    act(() => {
      fireEvent.pointerMove(stage, { clientX: 100, clientY: 10, pointerId: 1 });
      fireEvent.pointerUp(stage, { clientX: 100, clientY: 10, pointerId: 1 });
    });
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledTimes(1));
    const [, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
    expect(JSON.parse(init.body as string)).toEqual({ clear_reports_to: true });
  });

  it("recusa ciclo no cliente, sem chamar a API", async () => {
    const fetchImpl = vi.fn(async () => okResponse());
    render(<OrgChart nodes={fixture()} canEdit apiBase="/api/v1/companies/acme" msgs={msgs} fetchImpl={fetchImpl} />);
    act(() => drag("ceo", "eng"));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toBe("ciclo: ENG está abaixo de CEO"));
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it("desfaz o movimento e mostra o erro quando a API recusa", async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify({ title: "Unprocessable", detail: "would create a cycle" }), { status: 422 }));
    render(<OrgChart nodes={fixture()} canEdit apiBase="/api/v1/companies/acme" msgs={msgs} fetchImpl={fetchImpl} />);
    const before = document.querySelector('[data-node="eng"]')!.getAttribute("transform");
    act(() => drag("eng", "ceo"));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("would create a cycle"));
    expect(document.querySelector('[data-node="eng"]')!.getAttribute("transform")).toBe(before);
  });

  it("viewer não arrasta", async () => {
    const fetchImpl = vi.fn(async () => okResponse());
    render(<OrgChart nodes={fixture()} canEdit={false} apiBase="/api/v1/companies/acme" msgs={msgs} fetchImpl={fetchImpl} />);
    act(() => drag("eng", "ceo"));
    expect(document.querySelector(".org-root-drop")).toBeNull();
    expect(document.querySelector(".movable")).toBeNull();
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it("colapsa e expande ramos; mostra o indicador de run ativo", () => {
    render(<OrgChart nodes={fixture()} canEdit={false} apiBase="" msgs={msgs} />);
    expect(document.querySelector(".st-run .org-pulse")).toBeTruthy();
    expect(document.querySelectorAll("[data-node]").length).toBe(5);
    fireEvent.click(document.querySelector('[data-node="cto"] [data-toggle]')!);
    expect(document.querySelectorAll("[data-node]").length).toBe(3);
    expect(document.querySelector('[data-node="cto"] [data-toggle]')!.textContent).toBe("+2");
    fireEvent.click(document.querySelector('[data-node="cto"] [data-toggle]')!);
    expect(document.querySelectorAll("[data-node]").length).toBe(5);
  });

  it("zoom pelos botões e pan no fundo", () => {
    render(<OrgChart nodes={fixture()} canEdit={false} apiBase="" msgs={msgs} />);
    const g = () => document.querySelector(".org-canvas > g")!.getAttribute("transform")!;
    expect(g()).toBe("translate(8 8) scale(1)");
    fireEvent.click(screen.getAllByRole("button", { name: "org.zoom.in" })[0]!);
    expect(g()).toContain("scale(1.2)");
    const [x0, y0] = g().match(/translate\(([\d.]+) ([\d.]+)\)/)!.slice(1).map(Number) as [number, number];
    const stage = document.querySelector(".org-stage")!;
    fireEvent.pointerDown(document.querySelector(".org-canvas")!, { clientX: 300, clientY: 300, button: 0, pointerId: 1 });
    fireEvent.pointerMove(stage, { clientX: 340, clientY: 320, pointerId: 1 });
    fireEvent.pointerUp(stage, { clientX: 340, clientY: 320, pointerId: 1 });
    const [x1, y1] = g().match(/translate\(([\d.]+) ([\d.]+)\)/)!.slice(1).map(Number) as [number, number];
    expect([x1 - x0, y1 - y0]).toEqual([40, 20]);
  });
});

describe("glifo da persona", () => {
  it("desenha o símbolo do host pelo ícone e cai no robô sem ícone", () => {
    const { container } = render(<OrgChart nodes={[node("ceo", "", { icon: "briefcase" }), node("cto", "ceo")]} canEdit={false} apiBase="/api/v1/companies/acme" msgs={{}} />);
    const uses = Array.from(container.querySelectorAll("use.org-glyph")).map((u) => u.getAttribute("href"));
    expect(uses).toEqual(expect.arrayContaining(["#pg-briefcase", "#pg-bot"]));
    expect(container.querySelector(".org-ini")).toBeNull();
  });
});

