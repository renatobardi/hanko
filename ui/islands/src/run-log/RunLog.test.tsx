import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RunLog } from "./RunLog";
import type { RunEvent } from "./store";

const msgs = {
  "log.search": "Buscar no log",
  "log.filter": "Filtrar por tipo",
  "log.follow": "Ir para o fim",
  "log.paused": "Pausado",
  "log.following": "Seguindo o fim",
  "log.older": "Carregar eventos anteriores",
  "log.count": "{total} eventos",
  "log.count.filtered": "{shown} de {total} eventos",
  "log.nomatch": "Nenhum evento corresponde ao filtro.",
  "log.empty": "Sem eventos registrados.",
  "log.waiting": "Aguardando eventos…",
  "log.live": "ao vivo",
};

const ev = (seq: number, kind = "stdout", text = `linha ${seq}`, payload?: unknown): RunEvent => ({
  id: `id-${seq}`,
  run_id: "run-1",
  seq,
  kind,
  text,
  payload,
  occurred_at: "2026-10-15T12:00:00Z",
});
const range = (a: number, b: number) => Array.from({ length: b - a + 1 }, (_, i) => ev(a + i));

/** API falsa: o log inteiro em memória, com tail/before/after como o servidor. */
function fakeAPI(all: RunEvent[]) {
  const calls: string[] = [];
  const fetchImpl = vi.fn(async (url: string) => {
    calls.push(url);
    const u = new URL(url, "http://x");
    const limit = Number(u.searchParams.get("limit") ?? "200");
    let items: RunEvent[];
    if (u.searchParams.get("tail") === "true") items = all.slice(-limit);
    else if (u.searchParams.has("before")) {
      const before = Number(u.searchParams.get("before"));
      items = all.filter((e) => e.seq < before).slice(-limit);
    } else {
      const after = Number(u.searchParams.get("after") ?? "0");
      items = all.filter((e) => e.seq > after).slice(0, limit);
    }
    return new Response(JSON.stringify({ items, limit }), { status: 200, headers: { "Content-Type": "application/json" } });
  });
  return { fetchImpl, calls };
}

class FakeES {
  static instances: FakeES[] = [];
  url: string;
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  listeners = new Map<string, ((m: MessageEvent) => void)[]>();
  constructor(url: string) {
    this.url = url;
    FakeES.instances.push(this);
  }
  addEventListener(type: string, fn: (m: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  close() {
    this.closed = true;
  }
  emit(type: string, data: unknown) {
    for (const fn of this.listeners.get(type) ?? []) fn({ data: JSON.stringify(data) } as MessageEvent);
  }
}

const base = { apiBase: "/api/v1/companies/acme", runId: "run-1", tz: "UTC", lang: "pt-BR", msgs };
const rowCount = () => document.querySelectorAll(".log-line").length;
const viewport = () => screen.getByRole("log");

beforeEach(() => {
  FakeES.instances = [];
  Object.defineProperty(HTMLElement.prototype, "clientHeight", { configurable: true, get: () => 240 });
});

function setup(all: RunEvent[], live = false) {
  const api = fakeAPI(all);
  const utils = render(<RunLog {...base} live={live} pageSize={500} fetchImpl={api.fetchImpl} eventSourceImpl={FakeES as unknown as typeof EventSource} />);
  return { ...api, ...utils };
}

async function scrollTo(top: number) {
  const el = viewport();
  Object.defineProperty(el, "scrollTop", { configurable: true, writable: true, value: top });
  await act(async () => {
    fireEvent.scroll(el);
  });
}

describe("RunLog", () => {
  it("carrega o fim do log e renderiza só a janela visível", async () => {
    const { calls } = setup(range(1, 10000));
    await waitFor(() => expect(rowCount()).toBeGreaterThan(0));
    expect(calls[0]).toContain("tail=true&limit=500");
    expect(rowCount()).toBeLessThan(40); // 500 carregados, 240px de janela + folga
    expect(screen.getByText("500 eventos")).toBeTruthy();
    // segue o fim: a última linha está na janela
    expect(document.querySelector('[data-seq="10000"]')).toBeTruthy();
    expect(document.querySelector('[data-seq="9501"]')).toBeNull();
  });

  it("segue o SSE sem duplicar o que o replay repete", async () => {
    setup(range(1, 20), true);
    await waitFor(() => expect(FakeES.instances.length).toBe(1));
    const es = FakeES.instances[0]!;
    expect(es.url).toBe("/api/v1/companies/acme/runs/run-1/stream?after=20");
    await act(async () => {
      for (const e of [ev(19), ev(20), ev(21), ev(22), ev(22)]) es.emit("run.event", e);
      await new Promise((r) => setTimeout(r, 80));
    });
    expect(screen.getByText("22 eventos")).toBeTruthy();
    expect(document.querySelectorAll('[data-seq="22"]').length).toBe(1);
    expect(document.querySelectorAll('[data-seq="21"]').length).toBe(1);
  });

  it("fecha o stream quando a execução termina", async () => {
    setup(range(1, 3), true);
    await waitFor(() => expect(FakeES.instances.length).toBe(1));
    const es = FakeES.instances[0]!;
    await act(async () => {
      es.emit("run.event", ev(4));
      es.emit("run.status", { status: "done" });
    });
    expect(es.closed).toBe(true);
    await waitFor(() => expect(screen.getByText("4 eventos")).toBeTruthy());
  });

  it("pausa o seguimento ao rolar para cima e retoma pelo botão", async () => {
    setup(range(1, 400), true);
    await waitFor(() => expect(rowCount()).toBeGreaterThan(0));
    await scrollTo(2000);
    expect(screen.getByRole("button", { name: /Ir para o fim/ })).toBeTruthy();
    const es = FakeES.instances[0]!;
    await act(async () => {
      es.emit("run.event", ev(401));
      await new Promise((r) => setTimeout(r, 80));
    });
    // pausado: a janela continua onde o usuário está, a linha nova não é puxada para a tela
    expect(document.querySelector('[data-seq="401"]')).toBeNull();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /Ir para o fim/ }));
    });
    expect(document.querySelector('[data-seq="401"]')).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Ir para o fim/ })).toBeNull();
  });

  it("carrega páginas anteriores ao chegar no topo, sem repetir", async () => {
    const { calls } = setup(range(1, 1200));
    await waitFor(() => expect(rowCount()).toBeGreaterThan(0));
    await scrollTo(0);
    await waitFor(() => expect(screen.getByText("1000 eventos")).toBeTruthy());
    expect(calls.some((c) => c.includes("before=701&limit=500"))).toBe(true);
    await scrollTo(0);
    await waitFor(() => expect(screen.getByText("1200 eventos")).toBeTruthy());
    // tudo carregado: some o botão de histórico
    expect(screen.queryByRole("button", { name: /Carregar eventos anteriores/ })).toBeNull();
  });

  it("filtra por tipo e busca no texto", async () => {
    const all = [ev(1, "assistant", "Vou listar os arquivos"), ev(2, "tool_use", "Bash: ls -la", { cmd: "ls -la" }), ev(3, "stderr", "erro: permissão negada"), ev(4, "assistant", "Pronto")];
    setup(all);
    await waitFor(() => expect(rowCount()).toBe(4));
    fireEvent.click(screen.getByRole("button", { name: /^assistant/ }));
    await waitFor(() => expect(rowCount()).toBe(2));
    expect(screen.getByText("2 de 4 eventos")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /^assistant/ }));
    await waitFor(() => expect(rowCount()).toBe(4));
    fireEvent.change(screen.getByLabelText("Buscar no log"), { target: { value: "PERMISSÃO" } });
    await waitFor(() => expect(rowCount()).toBe(1));
    expect(document.querySelector("mark")?.textContent?.toLowerCase()).toBe("permissão");
    fireEvent.change(screen.getByLabelText("Buscar no log"), { target: { value: "nada disso existe" } });
    await waitFor(() => expect(screen.getByText("Nenhum evento corresponde ao filtro.")).toBeTruthy());
  });

  it("expande o payload de uma tool call", async () => {
    setup([ev(1, "assistant", "oi"), ev(2, "tool_use", "Bash: ls", { name: "Bash", input: { command: "ls -la" } })]);
    await waitFor(() => expect(rowCount()).toBe(2));
    expect(document.querySelector(".log-payload")).toBeNull();
    const btn = document.querySelector('[data-seq="2"] button.tx-btn') as HTMLButtonElement;
    expect(btn.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(btn);
    await waitFor(() => expect(document.querySelector(".log-payload")).toBeTruthy());
    expect(document.querySelector(".log-payload")!.textContent).toContain('"command": "ls -la"');
    expect((document.querySelector('[data-seq="2"] button.tx-btn') as HTMLButtonElement).getAttribute("aria-expanded")).toBe("true");
    fireEvent.click(document.querySelector('[data-seq="2"] button.tx-btn')!);
    await waitFor(() => expect(document.querySelector(".log-payload")).toBeNull());
  });

  it("mostra o erro de carga e tenta de novo", async () => {
    let n = 0;
    const fetchImpl = vi.fn(async () => {
      n++;
      if (n === 1) return new Response(JSON.stringify({ title: "Forbidden", detail: "sem acesso" }), { status: 403 });
      return new Response(JSON.stringify({ items: [ev(1)] }), { status: 200 });
    });
    render(<RunLog {...base} live={false} fetchImpl={fetchImpl} />);
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("sem acesso"));
    fireEvent.click(screen.getByRole("button", { name: "log.retry" }));
    await waitFor(() => expect(rowCount()).toBe(1));
  });
});
