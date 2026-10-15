import { describe, expect, it } from "vitest";
import { highlightParts } from "./format";
import { filterEvents, lowerBoundSeq, mergeEvents, type RunEvent } from "./store";
import { makeLayout, visibleRange, ROW_H, EXPANDED_EXTRA } from "./virtual";

const ev = (seq: number, kind = "stdout", text = `linha ${seq}`): RunEvent => ({
  id: `id-${seq}`,
  run_id: "r",
  seq,
  kind,
  text,
  occurred_at: "2026-10-15T12:00:00Z",
});
const range = (a: number, b: number) => Array.from({ length: b - a + 1 }, (_, i) => ev(a + i));
const seqs = (l: RunEvent[]) => l.map((e) => e.seq);

describe("mergeEvents", () => {
  it("concatena o que vem depois e deduplica por seq", () => {
    const base = range(1, 5);
    const out = mergeEvents(base, range(6, 8));
    expect(seqs(out)).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
  });

  it("devolve a mesma referência quando nada é novo (replay do SSE)", () => {
    const base = range(1, 5);
    expect(mergeEvents(base, range(3, 5))).toBe(base);
    expect(mergeEvents(base, [])).toBe(base);
  });

  it("junta sobreposição sem perder nem repetir", () => {
    const out = mergeEvents(range(1, 5), range(4, 9));
    expect(seqs(out)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9]);
  });

  it("página mais antiga entra antes", () => {
    const out = mergeEvents(range(11, 15), range(6, 10));
    expect(seqs(out)).toEqual([6, 7, 8, 9, 10, 11, 12, 13, 14, 15]);
  });

  it("aceita lote fora de ordem e repetido dentro dele", () => {
    const out = mergeEvents(range(1, 2), [ev(5), ev(3), ev(4), ev(4)]);
    expect(seqs(out)).toEqual([1, 2, 3, 4, 5]);
  });

  it("10 mil eventos em lotes cruzados terminam exatos", () => {
    let list: RunEvent[] = [];
    list = mergeEvents(list, range(9001, 10000)); // tail
    for (let a = 1; a <= 9000; a += 500) list = mergeEvents(list, range(a, a + 499)); // history
    list = mergeEvents(list, range(9900, 10000)); // SSE replay overlapping the tail
    expect(list.length).toBe(10000);
    expect(list[0]!.seq).toBe(1);
    expect(list[9999]!.seq).toBe(10000);
    expect(seqs(list).every((s, i) => s === i + 1)).toBe(true);
  });
});

describe("filtro e busca", () => {
  const list = [ev(1, "assistant", "Olá mundo"), ev(2, "tool_use", "Bash: ls -la"), ev(3, "stderr", "falhou"), ev(4, "assistant", "Pronto")];
  it("esconde tipos", () => {
    expect(seqs(filterEvents(list, { hidden: new Set(["assistant"]), query: "" }))).toEqual([2, 3]);
  });
  it("busca sem diferenciar caixa", () => {
    expect(seqs(filterEvents(list, { hidden: new Set(), query: "MUNDO" }))).toEqual([1]);
  });
  it("combina tipo e busca; sem filtro devolve a própria lista", () => {
    expect(seqs(filterEvents(list, { hidden: new Set(["stderr"]), query: "bash" }))).toEqual([2]);
    expect(filterEvents(list, { hidden: new Set(), query: "  " })).toBe(list);
  });
  it("lowerBoundSeq", () => {
    expect(lowerBoundSeq(list, 3)).toBe(2);
    expect(lowerBoundSeq(list, 99)).toBe(4);
  });
});

describe("virtualização", () => {
  it("sem expandidas, offset e índice são lineares", () => {
    const l = makeLayout(10000, []);
    expect(l.total).toBe(10000 * ROW_H);
    expect(l.offsetOf(10)).toBe(10 * ROW_H);
    expect(l.indexAt(10 * ROW_H + 3)).toBe(10);
    expect(l.heightOf(3)).toBe(ROW_H);
  });

  it("linhas expandidas empurram as seguintes", () => {
    const l = makeLayout(100, [5, 20]);
    expect(l.heightOf(5)).toBe(ROW_H + EXPANDED_EXTRA);
    expect(l.offsetOf(5)).toBe(5 * ROW_H);
    expect(l.offsetOf(6)).toBe(6 * ROW_H + EXPANDED_EXTRA);
    expect(l.offsetOf(21)).toBe(21 * ROW_H + 2 * EXPANDED_EXTRA);
    expect(l.total).toBe(100 * ROW_H + 2 * EXPANDED_EXTRA);
    // dentro da área extra da linha 5 ainda é a linha 5
    expect(l.indexAt(5 * ROW_H + 100)).toBe(5);
    expect(l.indexAt(6 * ROW_H + EXPANDED_EXTRA)).toBe(6);
  });

  it("renderiza só a janela (+ folga) de 10 mil linhas", () => {
    const l = makeLayout(10000, [30]);
    const [s, e] = visibleRange(l, 10000, 5000, 480);
    expect(e - s).toBeLessThan(60);
    expect(s).toBeLessThanOrEqual(l.indexAt(5000));
    expect(e).toBeGreaterThan(l.indexAt(5000 + 480));
    expect(visibleRange(makeLayout(0, []), 0, 0, 480)).toEqual([0, 0]);
    const [a, b] = visibleRange(l, 10000, 10000 * ROW_H, 480);
    expect(b).toBe(10000);
    expect(a).toBeGreaterThan(9900);
  });
});

describe("highlightParts", () => {
  const marked = (text: string, q: string) => highlightParts(text, q).filter((p) => p.match).map((p) => p.text);
  const joined = (text: string, q: string) => highlightParts(text, q).map((p) => p.text).join("");

  it("marca sem diferenciar caixa e preserva o texto", () => {
    expect(marked("Erro no DEPLOY: deploy falhou", "deploy")).toEqual(["DEPLOY", "deploy"]);
    expect(joined("Erro no DEPLOY: deploy falhou", "deploy")).toBe("Erro no DEPLOY: deploy falhou");
  });

  it("offsets corretos quando a minúscula muda de tamanho (İ)", () => {
    // "İ".toLowerCase() tem 2 unidades ("i" + U+0307): o texto baixado deslocaria a marca em 1 posição
    const text = "İstanbul deploy İ deploy";
    expect(marked(text, "deploy")).toEqual(["deploy", "deploy"]);
    expect(joined(text, "deploy")).toBe(text);
    const parts = highlightParts(text, "deploy");
    expect(parts[0]).toEqual({ text: "İstanbul ", match: false });
  });

  it("caracteres especiais da busca são literais", () => {
    expect(marked("a.b a+b (x) [y] a\\b", ".")).toEqual(["."]);
    expect(marked("a.b a+b (x) [y]", "a+b")).toEqual(["a+b"]);
    expect(marked("f(x) [y] $1 ^ | *", "(x)")).toEqual(["(x)"]);
    expect(marked("tab\\ola", "\\o")).toEqual(["\\o"]);
  });

  it("sem busca ou sem acerto devolve o texto inteiro, sem marca", () => {
    expect(highlightParts("abc", "  ")).toEqual([{ text: "abc", match: false }]);
    expect(highlightParts("abc", "z")).toEqual([{ text: "abc", match: false }]);
    expect(highlightParts("", "z")).toEqual([{ text: "", match: false }]);
  });

  it("acerto colado nas pontas", () => {
    expect(highlightParts("abab", "ab")).toEqual([
      { text: "ab", match: true },
      { text: "ab", match: true },
    ]);
  });
});
